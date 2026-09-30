// Copyright 2020 Google Inc. All rights reserved.
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

package build

import (
	"android/soong/ui/logger"
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestCleanOldFiles(t *testing.T) {
	dir, err := ioutil.TempDir("", "testcleanoldfiles")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	ctx := testContext()
	logBuf := &bytes.Buffer{}
	ctx.Logger = logger.New(logBuf)

	touch := func(names ...string) {
		for _, name := range names {
			if f, err := os.Create(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			} else {
				f.Close()
			}
		}
	}
	runCleanOldFiles := func(names ...string) {
		data := []byte(strings.Join(names, " "))
		if err := ioutil.WriteFile(filepath.Join(dir, ".installed"), data, 0666); err != nil {
			t.Fatal(err)
		}

		cleanOldFiles(ctx, dir, ".installed")
	}

	assertFileList := func(names ...string) {
		t.Helper()

		sort.Strings(names)

		var foundNames []string
		if foundFiles, err := ioutil.ReadDir(dir); err == nil {
			for _, fi := range foundFiles {
				foundNames = append(foundNames, fi.Name())
			}
		} else {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(names, foundNames) {
			t.Errorf("Expected a different list of files:\nwant: %v\n got: %v", names, foundNames)
			t.Error("Log: ", logBuf.String())
			logBuf.Reset()
		}
	}

	// Initial list of potential files
	runCleanOldFiles("foo", "bar")
	touch("foo", "bar", "baz")
	assertFileList("foo", "bar", "baz", ".installed.previous")

	// This should be a no-op, as the list hasn't changed
	runCleanOldFiles("foo", "bar")
	assertFileList("foo", "bar", "baz", ".installed", ".installed.previous")

	// This should be a no-op, as only a file was added
	runCleanOldFiles("foo", "bar", "foo2")
	assertFileList("foo", "bar", "baz", ".installed.previous")

	// "bar" should be removed, foo2 should be ignored as it was never there
	runCleanOldFiles("foo")
	assertFileList("foo", "baz", ".installed.previous")

	// Recreate bar, and create foo2. Ensure that they aren't removed
	touch("bar", "foo2")
	runCleanOldFiles("foo", "baz")
	assertFileList("foo", "bar", "baz", "foo2", ".installed.previous")
}

func TestCleanReplacedInstallDirs(t *testing.T) {
	dir := t.TempDir()
	ctx := testContext()
	logBuf := &bytes.Buffer{}
	ctx.Logger = logger.New(logBuf)

	soongFile := filepath.Join(dir, "soong", "replaced_install_dirs-test.json")
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0666); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	const current = `[{"primary":"new/modules.dep","dir":"new"}]`
	reset := func() {
		t.Helper()
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		// Three replaced directories, each holding its primary file and a file of its zip.
		for _, d := range []string{"dropped", "kept", "never"} {
			write(d+"/modules/modules.dep", "")
			write(d+"/modules/a.ko", "")
		}
		write("soong/replaced_install_dirs-test.json", current)
	}
	previousDirs := `[{"primary":"dropped/modules/modules.dep","dir":"dropped/modules"},` +
		`{"primary":"kept/modules/modules.dep","dir":"kept/modules"},` +
		`{"primary":"never/modules/modules.dep","dir":"never/modules"}]`
	run := func() {
		cleanReplacedInstallDirs(ctx, dir, ".installable_files", ".replaced_install_dirs.json", soongFile)
	}
	// runFatal runs the step and returns the error of its Fatalf, or nil.
	runFatal := func() (fatal error) {
		defer logger.Recover(func(err error) { fatal = err })
		run()
		return nil
	}

	// A directory whose primary file the new list drops is deleted with its files, one whose
	// primary file stays is kept, and one whose primary file the previous list did not hold is kept.
	reset()
	write(".replaced_install_dirs.json", previousDirs)
	write(".installable_files.previous", "dropped/modules/modules.dep kept/modules/modules.dep other")
	write(".installable_files", "kept/modules/modules.dep other new/modules.dep")
	run()
	if exists("dropped/modules") {
		t.Errorf("directory whose primary file is no longer installed was not deleted; log: %s", logBuf.String())
	}
	if !exists("kept/modules/a.ko") || !exists("never/modules/a.ko") {
		t.Errorf("a directory that is still installed or was never installed was deleted")
	}
	if !strings.Contains(logBuf.String(), "Removed directory that is no longer installed: "+filepath.Join(dir, "dropped/modules")) {
		t.Errorf("no message for the removed directory; log: %s", logBuf.String())
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".replaced_install_dirs.json")); err != nil || string(data) != current {
		t.Errorf("current list not copied: %q, %v", data, err)
	}
	if exists(".replaced_install_dirs.json.tmp") {
		t.Errorf("temporary copy left behind")
	}

	// Nothing is deleted without the previous copy or without either list, and the current file
	// is copied every time.
	for _, missing := range []string{".replaced_install_dirs.json", ".installable_files", ".installable_files.previous"} {
		reset()
		write(".replaced_install_dirs.json", previousDirs)
		write(".installable_files.previous", "dropped/modules/modules.dep kept/modules/modules.dep")
		write(".installable_files", "kept/modules/modules.dep")
		if err := os.Remove(filepath.Join(dir, missing)); err != nil {
			t.Fatal(err)
		}
		run()
		if !exists("dropped/modules/a.ko") {
			t.Errorf("without %s a directory was deleted", missing)
		}
		if data, err := os.ReadFile(filepath.Join(dir, ".replaced_install_dirs.json")); err != nil || string(data) != current {
			t.Errorf("without %s the current list was not copied: %q, %v", missing, data, err)
		}
	}

	// Without the file of soong_build nothing is deleted and nothing is written.
	reset()
	write(".replaced_install_dirs.json", previousDirs)
	write(".installable_files.previous", "dropped/modules/modules.dep")
	write(".installable_files", "")
	if err := os.Remove(soongFile); err != nil {
		t.Fatal(err)
	}
	run()
	if !exists("dropped/modules/a.ko") {
		t.Errorf("without the file of soong_build a directory was deleted")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".replaced_install_dirs.json")); string(data) != previousDirs {
		t.Errorf("without the file of soong_build the copy was changed: %q", data)
	}

	// A copy that exists but cannot be read, here a directory, stops the build before anything is
	// deleted or the copy is replaced.
	reset()
	if err := os.MkdirAll(filepath.Join(dir, ".replaced_install_dirs.json"), 0777); err != nil {
		t.Fatal(err)
	}
	write(".installable_files.previous", "dropped/modules/modules.dep kept/modules/modules.dep")
	write(".installable_files", "kept/modules/modules.dep")
	fatal := runFatal()
	if fatal == nil || !strings.Contains(fatal.Error(), "Failed to read \""+filepath.Join(dir, ".replaced_install_dirs.json")+"\"") {
		t.Errorf("an unreadable copy did not stop the build naming it: %v", fatal)
	}
	if !exists("dropped/modules/a.ko") {
		t.Errorf("with an unreadable copy a directory was deleted")
	}
	if info, err := os.Stat(filepath.Join(dir, ".replaced_install_dirs.json")); err != nil || !info.IsDir() || exists(".replaced_install_dirs.json.tmp") {
		t.Errorf("with an unreadable copy the copy was replaced")
	}

	// A copy that is no JSON array, entries that do not name clean relative paths below the product
	// out directory, and entries whose primary file does not lie directly in their directory each
	// stop the build with nothing deleted and the copy in place.
	bad := map[string]string{
		"no array":          `{"primary":"dropped/modules/modules.dep","dir":"dropped/modules"}`,
		"null":              `null`,
		"dir ..":            `[{"primary":"dropped/modules/modules.dep","dir":".."}]`,
		"dir ../x":          `[{"primary":"dropped/modules/modules.dep","dir":"../x"}]`,
		"dir absolute":      `[{"primary":"dropped/modules/modules.dep","dir":"` + filepath.Join(dir, "dropped/modules") + `"}]`,
		"primary absolute":  `[{"primary":"` + filepath.Join(dir, "dropped/modules/modules.dep") + `","dir":"dropped/modules"}]`,
		"primary elsewhere": `[{"primary":"dropped/modules/modules.dep","dir":"kept/modules"}]`,
		"primary below dir": `[{"primary":"dropped/modules/modules.dep","dir":"dropped"}]`,
		"dir empty":         `[{"primary":"dropped/modules/modules.dep","dir":""}]`,
		"dir .":             `[{"primary":"dropped/modules/modules.dep","dir":"."}]`,
		"dir unclean":       `[{"primary":"dropped/modules/modules.dep","dir":"kept/../dropped/modules"}]`,
		"no primary key":    `[{"dir":"dropped/modules"}]`,
	}
	for name, content := range bad {
		reset()
		write(".replaced_install_dirs.json", content)
		write(".installable_files.previous", "dropped/modules/modules.dep "+filepath.Join(dir, "dropped/modules/modules.dep"))
		write(".installable_files", "")
		fatal := runFatal()
		if fatal == nil || !strings.Contains(fatal.Error(), filepath.Join(dir, ".replaced_install_dirs.json")) {
			t.Errorf("%s: the step did not stop the build naming the copy: %v", name, fatal)
		}
		if !exists("dropped/modules/a.ko") || !exists("kept/modules/a.ko") {
			t.Errorf("%s: a directory was deleted", name)
		}
		if data, _ := os.ReadFile(filepath.Join(dir, ".replaced_install_dirs.json")); string(data) != content {
			t.Errorf("%s: the copy was replaced: %q", name, data)
		}
	}

	// A file of soong_build that is no such list stops the build naming that file, before anything
	// is deleted and before it can become the copy.
	for name, content := range map[string]string{
		"no array":   `{"primary":"new/modules.dep","dir":"new"}`,
		"null":       `null`,
		"truncated":  `[{"primary":"new/modules.dep","dir":"ne`,
		"dir ../x":   `[{"primary":"new/modules.dep","dir":"../x"}]`,
		"primary ..": `[{"primary":"..","dir":"new"}]`,
	} {
		reset()
		write(".replaced_install_dirs.json", previousDirs)
		write(".installable_files.previous", "dropped/modules/modules.dep")
		write(".installable_files", "")
		write("soong/replaced_install_dirs-test.json", content)
		fatal := runFatal()
		if fatal == nil || !strings.Contains(fatal.Error(), soongFile) {
			t.Errorf("%s: a bad file of soong_build did not stop the build naming it: %v", name, fatal)
		}
		if !exists("dropped/modules/a.ko") {
			t.Errorf("%s: with a bad file of soong_build a directory was deleted", name)
		}
		if data, _ := os.ReadFile(filepath.Join(dir, ".replaced_install_dirs.json")); string(data) != previousDirs {
			t.Errorf("%s: a bad file of soong_build replaced the copy: %q", name, data)
		}
	}
}
