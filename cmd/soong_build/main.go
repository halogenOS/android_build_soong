// Copyright 2015 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"android/soong/android"
	"android/soong/android/allowlists"
	"android/soong/finder/fs"
	"android/soong/shared"
	uiMetrics "android/soong/ui/metrics"

	androidProtobuf "google.golang.org/protobuf/android"

	"github.com/google/blueprint"
	"github.com/google/blueprint/bootstrap"
	"github.com/google/blueprint/deptools"
	"github.com/google/blueprint/metrics"
	"github.com/google/blueprint/pathtools"
)

var (
	topDir           string
	availableEnvFile string
	usedEnvFile      string

	delveListen string
	delvePath   string

	cmdlineArgs android.CmdArgs
)

const configCacheFile = "config.cache"

type ConfigCache struct {
	SoongBuildFileTimestamp int64
	// BuildOS, BuildFromSourceStub and RunGoTests are configuration values that modules read
	// directly from config fields, which records no read for the cache to compare.
	BuildOS             string
	BuildFromSourceStub bool
	RunGoTests          bool
	// KatiEnabled, KatiSuffix and SplitAllVariants are the build mode. An
	// analysis for one mode leaves out what another mode needs, such as the
	// Make data of modules and the variants that Make may depend on.
	KatiEnabled      bool
	KatiSuffix       string
	SplitAllVariants bool
}

func init() {
	// Flags that make sense in every mode
	flag.StringVar(&topDir, "top", "", "Top directory of the Android source tree")
	flag.StringVar(&cmdlineArgs.SoongOutDir, "soong_out", "", "Soong output directory (usually $TOP/out/soong)")
	flag.StringVar(&availableEnvFile, "available_env", "", "File containing available environment variables")
	flag.StringVar(&usedEnvFile, "used_env", "", "File containing used environment variables")
	flag.StringVar(&cmdlineArgs.OutDir, "out", "", "the ninja builddir directory")
	flag.StringVar(&cmdlineArgs.ModuleListFile, "l", "", "file that lists filepaths to parse")
	flag.StringVar(&cmdlineArgs.KatiSuffix, "kati_suffix", "", "the suffix for kati and ninja files, so that different configurations don't clobber each other")
	flag.BoolVar(&cmdlineArgs.KatiEnabled, "kati_enabled", false, "If the main kati build phase is enabled. False for soong-only builds")

	// Debug flags
	flag.StringVar(&delveListen, "delve_listen", "", "Delve port to listen on for debugging")
	flag.StringVar(&delvePath, "delve_path", "", "Path to Delve. Only used if --delve_listen is set")
	flag.StringVar(&cmdlineArgs.Cpuprofile, "cpuprofile", "", "write cpu profile to file")
	flag.StringVar(&cmdlineArgs.TraceFile, "trace", "", "write trace to file")
	flag.StringVar(&cmdlineArgs.Memprofile, "memprofile", "", "write memory profile to file")
	flag.BoolVar(&cmdlineArgs.NoGC, "nogc", false, "turn off GC for debugging")
	flag.Int64Var(&cmdlineArgs.MemoryLimit, "memory_limit", 0, "soft memory limit of the Go runtime in bytes, unless GOMEMLIMIT is set; 0 sets half of the memory available when soong_build starts")

	// Flags representing various modes soong_build can run in
	flag.StringVar(&cmdlineArgs.DocFile, "soong_docs", "", "build documentation file to output")
	flag.StringVar(&cmdlineArgs.OutFile, "o", "build.ninja", "the Ninja file to output")
	flag.StringVar(&cmdlineArgs.SoongVariables, "soong_variables", "soong.variables", "the file contains all build variables")
	flag.BoolVar(&cmdlineArgs.EmptyNinjaFile, "empty-ninja-file", false, "write out a 0-byte ninja file")
	flag.BoolVar(&cmdlineArgs.BuildFromSourceStub, "build-from-source-stub", false, "build Java stubs from source files instead of API text files")
	flag.BoolVar(&cmdlineArgs.EnsureAllowlistIntegrity, "ensure-allowlist-integrity", false, "verify that allowlisted modules are mixed-built")
	flag.StringVar(&cmdlineArgs.ModuleDebugFile, "soong_module_debug", "", "soong module debug info file to write")
	// Flags that probably shouldn't be flags of soong_build, but we haven't found
	// the time to remove them yet
	flag.BoolVar(&cmdlineArgs.RunGoTests, "t", false, "build and run go tests during bootstrap")
	flag.BoolVar(&cmdlineArgs.IncrementalBuildActions, "incremental-build-actions", false, "generate build actions incrementally")
	flag.StringVar(&cmdlineArgs.PartialAnalysisTargets, "partial-analysis-targets", "", "partial analysis targets")
	flag.BoolVar(&cmdlineArgs.IncrementalProviderTest, "incremental-provider-test", false, "test incremental providers restoring")
	flag.StringVar(&cmdlineArgs.IncrementalDebugFile, "incremental-debug-file", "", "incremental debug file")

	// Disable deterministic randomization in the protobuf package, so incremental
	// builds with unrelated Soong changes don't trigger large rebuilds (since we
	// write out text protos in command lines, and command line changes trigger
	// rebuilds).
	androidProtobuf.DisableRand()
}

func newNameResolver(config android.Config) *android.NameResolver {
	return android.NewNameResolver(config)
}

func newContext(configuration android.Config) *android.Context {
	ctx := android.NewContext(configuration)
	ctx.SetNameInterface(newNameResolver(configuration))
	ctx.SetAllowMissingDependencies(configuration.AllowMissingDependencies())
	ctx.AddSourceRootDirs(configuration.SourceRootDirs()...)
	return ctx
}

func needToWriteNinjaHint(ctx *android.Context) bool {
	switch ctx.Config().GetenvWithDefault("SOONG_GENERATES_NINJA_HINT", "") {
	case "always":
		return true
	case "depend":
		if _, err := os.Stat(shared.JoinPath(topDir, ctx.Config().OutDir(), ".ninja_log")); errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

func writeNinjaHint(ctx *android.Context) error {
	ctx.BeginEvent("ninja_hint")
	defer ctx.EndEvent("ninja_hint")
	// The current predictor focuses on reducing false negatives.
	// If there are too many false positives (e.g., most modules are marked as positive),
	// real long-running jobs cannot run early.
	// Therefore, the model should be adjusted in this case.
	// The model should also be adjusted if there are critical false negatives.
	predicate := func(j *blueprint.WeightedOutputsModuleInfo) (prioritized bool, weight int) {
		prioritized = false
		weight = 0
		for prefix, w := range allowlists.HugeModuleTypePrefixMap {
			if strings.HasPrefix(j.Type, prefix) {
				prioritized = true
				weight = w
				return
			}
		}
		input_size := j.DepsCount + j.SrcsCount

		// Current threshold is an arbitrary value which only consider recall rather than accuracy.
		if input_size > allowlists.INPUT_SIZE_THRESHOLD {
			prioritized = true
			weight += ((input_size) / allowlists.INPUT_SIZE_THRESHOLD) * allowlists.DEFAULT_PRIORITIZED_WEIGHT

			// To prevent some modules from having too large a priority value.
			if weight > allowlists.HIGH_PRIORITIZED_WEIGHT {
				weight = allowlists.HIGH_PRIORITIZED_WEIGHT
			}
		}
		return
	}

	outputsMap := ctx.Context.GetWeightedOutputsFromPredicate(predicate)
	var outputBuilder strings.Builder
	for output, weight := range outputsMap {
		outputBuilder.WriteString(fmt.Sprintf("%s,%d\n", output, weight))
	}
	weightListFile := shared.JoinPath(topDir, ctx.Config().OutDir(), ".ninja_weight_list")

	err := os.WriteFile(weightListFile, []byte(outputBuilder.String()), 0644)
	if err != nil {
		return fmt.Errorf("could not write ninja weight list file %s", err)
	}
	return nil
}

func writeMetrics(ctx *android.Context, configuration android.Config, eventHandler *metrics.EventHandler, metricsDir string) {
	if len(metricsDir) < 1 {
		fmt.Fprintf(os.Stderr, "\nMissing required env var for generating soong metrics: LOG_DIR\n")
		os.Exit(1)
	}
	metricsFile := filepath.Join(metricsDir, "soong_build_metrics.pb")
	err := android.WriteMetrics(ctx, configuration, eventHandler, metricsFile)
	maybeQuit(err, "error writing soong_build metrics %s", metricsFile)
}

func writeDepFile(outputFile string, eventHandler *metrics.EventHandler, ninjaDeps []string) {
	eventHandler.Begin("ninja_deps")
	defer eventHandler.End("ninja_deps")
	depFile := shared.JoinPath(topDir, outputFile+".d")
	err := deptools.WriteDepFile(depFile, outputFile, ninjaDeps)
	maybeQuit(err, "error writing depfile '%s'", depFile)
}

// Check if the soong_build binary, the build mode or a config field read directly by modules
// changed, in which case the analysis runs in full. A change of any other configuration value
// makes only the modules and singletons that read it run again, as the cache compares the
// configuration values read by each of them.
func incrementalValid(config android.Config, splitAllVariants bool, configCacheFile string) (*ConfigCache, bool) {
	var newConfigCache ConfigCache
	newConfigCache.SoongBuildFileTimestamp = getFileTimestamp(shared.JoinPath(topDir, config.HostToolDir(), "soong_build"))
	newConfigCache.KatiEnabled = cmdlineArgs.KatiEnabled
	newConfigCache.KatiSuffix = cmdlineArgs.KatiSuffix
	newConfigCache.SplitAllVariants = splitAllVariants
	newConfigCache.BuildOS = config.BuildOS.Name
	newConfigCache.BuildFromSourceStub = cmdlineArgs.BuildFromSourceStub
	newConfigCache.RunGoTests = cmdlineArgs.RunGoTests

	file, err := os.Open(configCacheFile)
	if err != nil && os.IsNotExist(err) {
		return &newConfigCache, false
	}
	maybeQuit(err, "")
	defer file.Close()

	var configCache ConfigCache
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&configCache)
	if err != nil {
		fmt.Printf("Failed to parse config cache: %s.  Continuing with non-incremental analysis.", err.Error())
		return &newConfigCache, false
	}

	return &newConfigCache, newConfigCache == configCache
}

func getFileTimestamp(file string) int64 {
	stat, err := os.Stat(file)
	if err == nil {
		return stat.ModTime().UnixMilli()
	} else if !os.IsNotExist(err) {
		maybeQuit(err, "")
	}
	return 0
}

func writeConfigCache(configCache *ConfigCache, configCacheFile string) {
	file, err := os.Create(configCacheFile)
	maybeQuit(err, "")
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(*configCache)
	maybeQuit(err, "")
}

// runSoongOnlyBuild runs the standard Soong build in a number of different modes.
// It returns the path to the output file (usually the ninja file) and the deps that need
// to trigger a soong rerun.
func runSoongOnlyBuild(ctx *android.Context) (string, []string) {
	ctx.EventHandler.Begin("soong_build")
	defer ctx.EventHandler.End("soong_build")

	var stopBefore bootstrap.StopBefore
	switch ctx.Config().BuildMode {
	case android.GenerateDocFile:
		stopBefore = bootstrap.StopBeforePrepareBuildActions
	default:
		stopBefore = bootstrap.DoEverything
	}

	ninjaDeps, err := bootstrap.RunBlueprint(cmdlineArgs.Args, stopBefore, ctx.Context, ctx.Config())
	maybeQuit(err, "")

	// Convert the Soong module graph into Bazel BUILD files.
	switch ctx.Config().BuildMode {
	case android.GenerateDocFile:
		// TODO: we could make writeDocs() return the list of documentation files
		// written and add them to the .d file. Then soong_docs would be re-run
		// whenever one is deleted.
		err := writeDocs(ctx, shared.JoinPath(topDir, cmdlineArgs.DocFile))
		maybeQuit(err, "error building Soong documentation")
		return cmdlineArgs.DocFile, ninjaDeps
	default:
		// The actual output (build.ninja) was written in the RunBlueprint() call
		// above
		if needToWriteNinjaHint(ctx) {
			writeNinjaHint(ctx)
		}
		return cmdlineArgs.OutFile, ninjaDeps
	}
}

// soong_ui dumps the available environment variables to
// soong.environment.available . Then soong_build itself is run with an empty
// environment so that the only way environment variables can be accessed is
// using Config, which tracks access to them.

// At the end of the build, a file called soong.environment.used is written
// containing the current value of all used environment variables. The next
// time soong_ui is run, it checks whether any environment variables that was
// used had changed and if so, it deletes soong.environment.used to cause a
// rebuild.
func parseAvailableEnv() map[string]string {
	if availableEnvFile == "" {
		fmt.Fprintf(os.Stderr, "--available_env not set\n")
		os.Exit(1)
	}
	result, err := shared.EnvFromFile(shared.JoinPath(topDir, availableEnvFile))
	maybeQuit(err, "error reading available environment file '%s'", availableEnvFile)
	return result
}

// halfOfAvailableMemory returns the heap limit that leaves the other half of
// the memory available at start to the memory of soong_build outside its heap
// and to the rest of the machine. Without a limit, the garbage collector lets
// the heap grow to twice its live size. soong_build reads the available memory
// itself: a value on its command line would change the command of the analysis
// step, and so rerun the analysis, whenever the free memory of the machine
// changes.
func halfOfAvailableMemory() int64 {
	memInfo, err := uiMetrics.NewMemInfo(fs.OsFs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not read the available memory, the heap has no limit: %s\n", err)
		return 0
	}
	return int64(memInfo.MemAvailable / 2)
}

func main() {
	flag.Parse()

	if cmdlineArgs.MemoryLimit == 0 {
		cmdlineArgs.MemoryLimit = halfOfAvailableMemory()
	}

	if cmdlineArgs.Memprofile == "" {
		// Go enables memory profile collection automatically if any references
		// are linked in to the binary.
		// Disable collection of memory profiles if we won't save one to disk.
		runtime.MemProfileRate = 0
	}
	soongStartTime := time.Now()

	shared.ReexecWithDelveMaybe(delveListen, delvePath)
	android.InitSandbox(topDir)

	availableEnv := parseAvailableEnv()

	if availableEnv["SOONG_ENFORCE_NO_REANALYSIS"] == "true" {
		fmt.Fprintln(os.Stderr, "Reanalysis will run due to build graph or product configuration change.")
		os.Exit(1)
	}

	configuration, err := android.NewConfig(cmdlineArgs, availableEnv)
	maybeQuit(err, "")
	if configuration.Getenv("ALLOW_MISSING_DEPENDENCIES") == "true" {
		configuration.SetAllowMissingDependencies()
	}

	// Bypass configuration.Getenv, as LOG_DIR does not need to be dependency tracked. By definition, it will
	// change between every CI build, so tracking it would require re-running Soong for every build.
	metricsDir := availableEnv["LOG_DIR"]

	ctx := newContext(configuration)
	android.StartBackgroundMetrics(configuration)

	if configuration.Getenv("SOONG_SPLIT_ALL_VARIANTS") == "true" ||
		configuration.Getenv("RUN_BUILD_TESTS") == "true" ||
		// Soong does not have sufficient information to determine if an androidmk module
		// has a dependency on a non primary soong module variant.
		// Analyze all variants in soong+make builds.
		cmdlineArgs.KatiEnabled ||
		// TODO (b/477627661): Enable on demand variants with AllowMissingDependencies
		configuration.AllowMissingDependencies() {
		ctx.SetSplitAllVariants(true)
	}

	var configCache *ConfigCache
	configFile := shared.JoinPath(topDir, ctx.Config().OutDir(), configCacheFile)
	incremental := false
	ctx.SetIncrementalEnabled(cmdlineArgs.IncrementalBuildActions)
	if ctx.GetIncrementalEnabled() {
		configCache, incremental = incrementalValid(ctx.Config(), ctx.GetSplitAllVariants(), configFile)
		ctx.SetIncrementalProviderTest(incremental && cmdlineArgs.IncrementalProviderTest)
		// Every analysis writes to the cache, a full one after resetting it, and a stopped one can
		// leave it half written. The config cache of an earlier analysis must then not match, so
		// only an analysis that succeeds writes a new one.
		if err := os.Remove(configFile); !os.IsNotExist(err) {
			maybeQuit(err, "error removing config cache '%s'", configFile)
		}
	}
	ctx.SetIncrementalAnalysis(incremental)
	ctx.SetPartialAnalysisTargets(cmdlineArgs.PartialAnalysisTargets)
	ctx.SetIncrementalDebugFile(cmdlineArgs.IncrementalDebugFile)

	ctx.Register()
	finalOutputFile, ninjaDeps := runSoongOnlyBuild(ctx)

	ninjaDeps = append(ninjaDeps, configuration.ProductVariablesFileName)
	if shared.IsDebugging() {
		// Add a non-existent file to the dependencies so that soong_build will rerun when the debugger is
		// enabled even if it completed successfully.
		ninjaDeps = append(ninjaDeps, filepath.Join(configuration.SoongOutDir(), "always_rerun_for_delve"))
	}

	writeDepFile(finalOutputFile, ctx.EventHandler, ninjaDeps)

	if ctx.GetIncrementalEnabled() {
		writeConfigCache(configCache, configFile)
	}

	writeMetrics(ctx, configuration, ctx.EventHandler, metricsDir)

	writeUsedEnvironmentFile(configuration)

	ctx.EventHandler.Begin("writeGlobFile")
	err = ctx.WriteGlobFile(shared.JoinPath(topDir, finalOutputFile), soongStartTime)
	ctx.EventHandler.End("writeGlobFile")
	maybeQuit(err, "")
}

func writeUsedEnvironmentFile(configuration android.Config) {
	if usedEnvFile == "" {
		return
	}

	path := shared.JoinPath(topDir, usedEnvFile)
	data, err := shared.EnvFileContents(configuration.EnvDeps())
	maybeQuit(err, "error writing used environment file '%s'\n", usedEnvFile)

	err = pathtools.WriteFileIfChanged(path, data, 0666)
	maybeQuit(err, "error writing used environment file '%s'", usedEnvFile)
}

func maybeQuit(err error, format string, args ...interface{}) {
	if err == nil {
		return
	}
	if format != "" {
		fmt.Fprintln(os.Stderr, fmt.Sprintf(format, args...)+": "+err.Error())
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(1)
}
