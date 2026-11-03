// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package finder

import (
	"math"
	"runtime/debug"
	"slices"
	"sync"
	"time"
)

// A dirCheck lstats the directories of the cached database to find the ones
// that changed. Lookups in one parent directory can wait for each other at the
// file server, as they do on FUSE shares, so the check stats several
// contiguous ranges of the database at once, each range in database order.
// Concurrent lookups then come from different parts of the tree.
//
// A new range is a database block that no range covers yet, or else the upper
// half of the running range with the most directories left. The number of
// ranges running at once comes from a rangeCountSearch.
type dirCheck struct {
	stat    func(path string) statResponse
	workers sync.WaitGroup

	mutex      sync.Mutex
	ranges     []*dirRange // ranges with directories left to stat
	running    int         // workers that stat a range
	rangeCount rangeCountSearch
}

// a checkedBlock contains the directories of one database block and the stat
// of each one
type checkedBlock struct {
	dirs    []dirFullInfo
	stats   []statResponse
	pending sync.WaitGroup // one count per range of the block that is not done
}

// a dirRange is the part [next, end) of a block that is left to stat
type dirRange struct {
	block   *checkedBlock
	next    int
	end     int
	claimed bool // whether a worker stats this range
}

// A rangeStep is what a worker does after takeDir.
type rangeStep struct{ name string }

var (
	// statDir: the worker stats the directory at the index from takeDir.
	statDir = rangeStep{"stat directory"}
	// rangeFinished: no directory of the range is left to stat.
	rangeFinished = rangeStep{"range finished"}
	// rangeReleased: more workers run than the range count allows, so the
	// worker stops. The rest of the range is in the list, unclaimed.
	rangeReleased = rangeStep{"range released"}
)

// newDirCheck returns a check that stats with stat, starting with
// initialRanges ranges at once.
func newDirCheck(stat func(path string) statResponse, initialRanges int) *dirCheck {
	limit := blockingThreadShare()
	target := min(initialRanges, limit)
	return &dirCheck{
		stat: stat,
		rangeCount: rangeCountSearch{
			limit:         limit,
			target:        target,
			phase:         phaseSettling,
			blocksAwaited: target,
			measured:      make(map[int]*measuredRate),
			windowStart:   time.Now(),
		},
	}
}

// blockingThreadShare returns how many OS threads the check may block in
// lstat at once. The Go runtime ends the process when it needs a thread over
// its limit. Half of the limit is a choice made here, so the stats leave
// threads for the rest of soong_ui. Reading the limit sets and restores it for
// the whole process, so the finder reads it once.
var blockingThreadShare = sync.OnceValue(func() int {
	// The runtime reports its limit only when the limit is set.
	limit := debug.SetMaxThreads(math.MaxInt32)
	debug.SetMaxThreads(limit)
	return max(limit/2, 1)
})

// statAll lstats the directories of one database block and returns one stat
// per directory, in the same order.
func (c *dirCheck) statAll(dirs []dirFullInfo) []statResponse {
	block := &checkedBlock{dirs: dirs, stats: make([]statResponse, len(dirs))}
	if len(dirs) > 0 {
		block.pending.Add(1)
		c.mutex.Lock()
		c.ranges = append(c.ranges, &dirRange{block: block, end: len(dirs)})
		c.rangeCount.countBlock(len(dirs))
		c.startWorkers()
		c.mutex.Unlock()
	}
	block.pending.Wait()
	return block.stats
}

// wait returns once every worker has stopped.
func (c *dirCheck) wait() {
	c.workers.Wait()
}

// rangesAtOnce returns the number of ranges that the check runs at once.
func (c *dirCheck) rangesAtOnce() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.rangeCount.target
}

// startWorkers starts workers until the target number of ranges runs or
// claimLargestShare returns nil. c.mutex must be held.
func (c *dirCheck) startWorkers() {
	for c.rangeCount.allows(c.running + 1) {
		r := c.claimLargestShare()
		if r == nil {
			return
		}
		c.running++
		c.workers.Add(1)
		go c.statRanges(r)
	}
}

// claimLargestShare returns the range with the most directories for one
// worker: a range that no worker stats yet as a whole, or the upper half of a
// running one. It returns nil when no range has a directory for one more
// worker. c.mutex must be held.
func (c *dirCheck) claimLargestShare() *dirRange {
	var largest *dirRange
	for _, r := range c.ranges {
		if largest == nil || r.share() > largest.share() {
			largest = r
		}
	}
	if largest == nil || largest.share() == 0 {
		return nil
	}
	claimed := largest.claim()
	if claimed != largest {
		// The worker that stats largest has not finished it, so the pending
		// count is above zero.
		claimed.block.pending.Add(1)
		c.ranges = append(c.ranges, claimed)
	}
	return claimed
}

// share returns how many directories of r a worker stats when
// claimLargestShare returns r.
func (r *dirRange) share() int {
	if r.claimed {
		return (r.end - r.next) / 2
	}
	return r.end - r.next
}

// claim marks r as claimed and returns it when no worker stats r, or else
// splits r and returns its upper half, of share() directories, as a new range.
func (r *dirRange) claim() *dirRange {
	if !r.claimed {
		r.claimed = true
		return r
	}
	upperHalf := &dirRange{
		block:   r.block,
		next:    r.end - r.share(),
		end:     r.end,
		claimed: true,
	}
	r.end = upperHalf.next
	return upperHalf
}

// statRanges stats r and then further ranges until claimNextRange returns nil
// or the worker releases a range.
func (c *dirCheck) statRanges(r *dirRange) {
	defer c.workers.Done()
	for ; r != nil; r = c.claimNextRange() {
		if c.statRange(r) == rangeReleased {
			return
		}
		r.block.pending.Done()
	}
}

// statRange stats the directories of r in order and returns rangeFinished or
// rangeReleased.
func (c *dirCheck) statRange(r *dirRange) rangeStep {
	completedStats := 0
	for {
		i, step := c.takeDir(r, completedStats)
		if step != statDir {
			return step
		}
		r.block.stats[i] = c.stat(r.block.dirs[i].Path)
		completedStats = 1
	}
}

// claimNextRange returns the next range for a worker that finished one, or
// nil when the worker stops.
func (c *dirCheck) claimNextRange() *dirRange {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.rangeCount.allows(c.running) {
		if r := c.claimLargestShare(); r != nil {
			return r
		}
	}
	c.running--
	return nil
}

// takeDir counts completedStats, the stats completed since the previous call
// by the worker that stats r, and returns what that worker does next, with
// the index of the next directory of r to stat when the step is statDir.
func (c *dirCheck) takeDir(r *dirRange, completedStats int) (int, rangeStep) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.rangeCount.countStats(completedStats) {
		c.startWorkers()
	}
	if r.next == r.end {
		return 0, rangeFinished
	}
	if !c.rangeCount.allows(c.running) {
		r.claimed = false
		c.running--
		return 0, rangeReleased
	}
	i := r.next
	r.next++
	if r.next == r.end {
		c.ranges = slices.DeleteFunc(c.ranges, func(listed *dirRange) bool { return listed == r })
	}
	return i, statDir
}

// A rangeCountSearch finds the number of ranges to stat at once. It doubles
// the number for as long as the completed stats per second rise, and goes back
// to the number with the highest rate once the rate stops rising. The rate of
// a number covers every window measured with it, so a single fast window does
// not decide the search, and a number that was fast once is measured again
// when the search goes back to it.
//
// The rate is measured over windows that end after as many stats as the
// largest database block has directories, or as the target number of ranges
// when that is more. A window of one stat per range takes about one stat
// latency (the number of ranges equals the number of stats in flight, by
// Little's law), which is too short to tell two numbers apart. The
// window right after a change of the number is not measured, because each
// added range finishes a first stat in it. Neither are the windows before as
// many blocks as the first number of ranges arrived, so the measured ranges
// are spread over the tree, not cut from the first one or two blocks.
type rangeCountSearch struct {
	limit         int                   // the most ranges that may run at once
	target        int                   // the number of ranges to run at once
	measured      map[int]*measuredRate // by number of ranges
	misses        int                   // measured windows in a row not above the best rate
	phase         searchPhase
	blocksAwaited int // blocks to arrive before the windows are measured

	completedStats   int
	windowLength     int // stats in a window, unless the target is larger
	windowStart      time.Time
	windowStartStats int
}

// a measuredRate is the sum of the windows measured with one number of ranges
type measuredRate struct {
	stats   int
	elapsed time.Duration
}

func (m *measuredRate) rate() float64 {
	return float64(m.stats) / m.elapsed.Seconds()
}

// best returns the number of ranges with the highest rate measured so far.
func (s *rangeCountSearch) best() (ranges int, rate float64) {
	for count, measured := range s.measured {
		if r := measured.rate(); r > rate || r == rate && count < ranges {
			ranges, rate = count, r
		}
	}
	return ranges, rate
}

// A searchPhase is the state of a rangeCountSearch: whether the running
// window is measured.
type searchPhase struct{ name string }

var (
	// phaseSettling: the window follows a change of the target, or blocks
	// are still awaited.
	phaseSettling = searchPhase{"settling"}
	// phaseMeasuring: the rate of the window decides the target.
	phaseMeasuring = searchPhase{"measuring"}
)

// missesBeforeFallBack is how many measured windows in a row, none above the
// best rate, make the search go back to the best number. A few slow lookups can
// slow down a single window.
const missesBeforeFallBack = 2

// allows returns whether a worker may start or keep running while running
// workers, itself included, run at once.
func (s *rangeCountSearch) allows(running int) bool {
	return running <= s.target
}

// countBlock records that one more block of the database arrived, with dirs
// directories.
func (s *rangeCountSearch) countBlock(dirs int) {
	s.blocksAwaited = max(s.blocksAwaited-1, 0)
	s.windowLength = max(s.windowLength, dirs)
}

// countStats records completed stats and returns whether the target rose.
func (s *rangeCountSearch) countStats(completed int) (raised bool) {
	s.completedStats += completed
	windowStats := s.completedStats - s.windowStartStats
	if windowStats < max(s.target, s.windowLength) {
		return false
	}
	now := time.Now()
	window := measuredRate{stats: windowStats, elapsed: now.Sub(s.windowStart)}
	s.windowStart, s.windowStartStats = now, s.completedStats
	if s.phase == phaseSettling {
		if s.blocksAwaited == 0 {
			s.phase = phaseMeasuring
		}
		return false
	}
	return s.adjustTarget(window)
}

// adjustTarget adds a measured window to the rate of the target and sets the
// target from the rates. It returns whether the target rose.
func (s *rangeCountSearch) adjustTarget(window measuredRate) (raised bool) {
	_, bestRate := s.best()
	measured := s.measured[s.target]
	if measured == nil {
		measured = &measuredRate{}
		s.measured[s.target] = measured
	}
	measured.stats += window.stats
	measured.elapsed += window.elapsed
	if measured.elapsed > 0 && measured.rate() > bestRate {
		s.misses = 0
		return s.setTarget(min(2*s.target, s.limit))
	}
	s.misses++
	if s.misses < missesBeforeFallBack {
		return false
	}
	s.misses = 0
	bestRanges, _ := s.best()
	return s.setTarget(bestRanges)
}

// setTarget changes the target to target and returns whether it rose. The
// window after a change is not measured.
func (s *rangeCountSearch) setTarget(target int) (raised bool) {
	if target == s.target {
		return false
	}
	raised = target > s.target
	s.target, s.phase = target, phaseSettling
	return raised
}
