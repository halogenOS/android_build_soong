// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// soong_build writes two lists of the installs that extract an extra files zip into the directory
// that contains their primary file, each as a JSON array that is empty when no install of its kind
// exists. It writes both on every run.
//
//   - SoongReplacedInstallDirs lists the directories that such an install replaces as a whole. The
//     removal of directories that are no longer installed (cleanReplacedInstallDirs) and the signing
//     run (wholeDirOutputsArgs) read it.
//   - SoongExtractedZipInstalls lists the primary files of the other such installs, which keep the
//     other files in the directory. The signing run reads it.

// replacedInstallDir is one entry of the list of replaced install directories: the primary file of
// the install and the directory, both relative to the product out directory.
type replacedInstallDir struct {
	Primary string `json:"primary"`
	Dir     string `json:"dir"`
}

// readReplacedInstallDirs reads the list of replaced install directories in file and checks each
// entry with checkReplacedInstallDir.
func readReplacedInstallDirs(file string) ([]replacedInstallDir, error) {
	return readInstallZipList(file, checkReplacedInstallDir)
}

// readExtractedZipInstalls reads the list of primary files in file and checks that each is a clean
// relative path below the product out directory.
func readExtractedZipInstalls(file string) ([]string, error) {
	return readInstallZipList(file, checkProductOutPath)
}

// invalidInstallZipListError is the error of a list whose content is not a JSON array of valid
// entries.
type invalidInstallZipListError struct {
	err error
}

func (e invalidInstallZipListError) Error() string { return e.err.Error() }

func (e invalidInstallZipListError) Unwrap() error { return e.err }

// readInstallZipList reads the list in file, a JSON array, and checks each entry with check. The
// error of a file that cannot be read is the error of os.ReadFile, so errors.Is(err,
// fs.ErrNotExist) reports a missing file. Any other content than such an array is an
// invalidInstallZipListError, null included, which json.Unmarshal takes for a nil slice without an
// error.
func readInstallZipList[T any](file string, check func(T) error) ([]T, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var entries []T
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, invalidInstallZipListError{fmt.Errorf("failed to parse it: %v", err)}
	}
	if entries == nil {
		return nil, invalidInstallZipListError{errors.New("it is no JSON array")}
	}
	for _, entry := range entries {
		if err := check(entry); err != nil {
			return nil, invalidInstallZipListError{err}
		}
	}
	return entries, nil
}

// describeInstallZipListError returns the message for err, an error of readInstallZipList on file,
// that ends with next, the step that fixes it.
func describeInstallZipListError(file string, err error, next string) string {
	if errors.As(err, &invalidInstallZipListError{}) {
		return fmt.Sprintf("The list in %q is not valid: %v. %s", file, err, next)
	}
	return fmt.Sprintf("Failed to read %q: %v. %s", file, err, next)
}

// checkReplacedInstallDir reports an entry whose primary file or directory is not a clean
// relative path below the product out directory, or whose primary file does not lie directly in
// its directory, the only shape soong_build writes. The last check keeps an edited or corrupted
// entry from deleting a directory that held no replacing install.
func checkReplacedInstallDir(entry replacedInstallDir) error {
	for _, p := range []string{entry.Primary, entry.Dir} {
		if checkProductOutPath(p) != nil {
			return fmt.Errorf("entry %+v does not name a clean relative path below the product out directory", entry)
		}
	}
	if filepath.Dir(entry.Primary) != entry.Dir {
		return fmt.Errorf("entry %+v names a primary file that does not lie directly in its directory", entry)
	}
	return nil
}

// checkProductOutPath reports a path that is not a clean relative path below the product out
// directory.
func checkProductOutPath(p string) error {
	if p == "" || p == "." || p == ".." || filepath.IsAbs(p) || filepath.Clean(p) != p || strings.HasPrefix(p, "../") {
		return fmt.Errorf("%q is no clean relative path below the product out directory", p)
	}
	return nil
}
