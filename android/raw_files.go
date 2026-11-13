// Copyright 2023 Google Inc. All rights reserved.
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

package android

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/blueprint"
	"github.com/google/blueprint/syncmap"
)

// WriteFileRule creates a ninja rule to write contents to a file by immediately writing the
// contents, plus a trailing newline, to a file in out/soong/raw-${TARGET_PRODUCT}, and then creating
// a ninja rule to copy the file into place.
func WriteFileRule(ctx BuilderContext, outputFile WritablePath, content string, validations ...Path) {
	writeFileRule(ctx, outputFile, content, true, false, validations)
}

// WriteFileRuleVerbatim creates a ninja rule to write contents to a file by immediately writing the
// contents to a file in out/soong/raw-${TARGET_PRODUCT}, and then creating a ninja rule to copy the file into place.
func WriteFileRuleVerbatim(ctx BuilderContext, outputFile WritablePath, content string, validations ...Path) {
	writeFileRule(ctx, outputFile, content, false, false, validations)
}

// WriteExecutableFileRuleVerbatim is the same as WriteFileRuleVerbatim, but runs chmod +x on the result
func WriteExecutableFileRuleVerbatim(ctx BuilderContext, outputFile WritablePath, content string, validations ...Path) {
	writeFileRule(ctx, outputFile, content, false, true, validations)
}

func writeFileRule(ctx BuilderContext, outputFile WritablePath, content string, newline bool, executable bool, validations Paths) {
	h := sha1.New()
	_, err := io.WriteString(h, content)
	if err == nil && newline {
		_, err = io.WriteString(h, "\n")
	}
	if err != nil {
		panic(fmt.Errorf("failed to calculate the hash of the file rule content: %w", err))
	}
	hash := hex.EncodeToString(h.Sum(nil))

	// Shard the final location of the raw file into a subdirectory based on the first two characters of the
	// hash to avoid making the raw directory too large and slowing down accesses.
	relPath := filepath.Join(hash[0:2], hash)

	// These files are written during soong_build.  If something outside the build deleted them there would be no
	// trigger to rerun soong_build, and the build would break with dependencies on missing files.  Writing them
	// to their final locations would risk having them deleted when cleaning a module, and would also pollute the
	// output directory with files for modules that have never been built.
	// Instead, the files are written to a separate "raw" directory next to the build.ninja file, and a ninja
	// rule is created to copy the files into their final location as needed.
	// Obsolete files written by previous runs of soong_build must be cleaned up to avoid continually growing
	// disk usage as the hashes of the files change over time.  The cleanup must not remove files that were
	// created by previous runs of soong_build for other products, as the build.ninja files for those products
	// may still exist and still reference those files.  The raw files from different products are kept
	// separate by appending the Make_suffix to the directory name.
	rawPath := PathForOutput(ctx, "raw"+ctx.Config().MakeSuffix(), relPath)

	rawFileInfo := rawFileInfo{
		relPath: relPath,
	}

	if ctx.Config().captureBuild {
		// When running tests the content won't be written to disk, instead store the
		// contents for later retrieval by ContentFromFileRuleForTests.
		extraLine := ""
		if newline {
			extraLine = "\n"
		}

		rawFileInfo.contentForTests = content + extraLine
	}

	rawFileSet := getRawFileSet(ctx.Config())
	if _, exists := rawFileSet.LoadOrStore(hash, rawFileInfo); !exists && !ctx.Config().captureBuild {
		// If this is the first time this hash has been seen then move it from the temporary directory
		// to the raw directory.  If the file already exists in the raw directory assume it has the correct
		// contents.
		absRawPath := absolutePath(rawPath.String())
		_, err := os.Stat(absRawPath)
		if os.IsNotExist(err) {
			dir := filepath.Dir(absRawPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				panic(fmt.Errorf("failed to create directory: %w", err))
			}
			rawFile, err := os.Create(absRawPath)
			if err != nil {
				panic(fmt.Errorf("failed to open file to write the file rule content: %w", err))
			}
			defer rawFile.Close()
			_, err = io.WriteString(rawFile, content)
			if err == nil && newline {
				_, err = io.WriteString(rawFile, "\n")
			}
			if err != nil {
				panic(fmt.Errorf("failed to write the file rule content: %w", err))
			}
		} else if err != nil {
			panic(fmt.Errorf("failed to stat %q: %w", absRawPath, err))
		}
	}

	// Emit a rule to copy the file from raw directory to the final requested location in the output tree.
	// Restat is used to ensure that two different products that produce identical files copied from their
	// own raw directories they don't cause everything downstream to rebuild.
	rule := rawFileCopy
	if executable {
		rule = rawFileCopyExecutable
	}
	ctx.Build(pctx, BuildParams{
		Rule:        rule,
		Input:       rawPath,
		Output:      outputFile,
		Description: "raw " + outputFile.Base(),
		Validations: validations,
	})
}

var (
	rawFileCopy = pctx.AndroidStaticRule("rawFileCopy",
		blueprint.RuleParams{
			Command2: blueprint.NewCommand(
				"if ! ", Cmp, " -s $in $out; then ", Cp, " $in $out; fi"),
			Description: "copy raw file $out",
			Restat:      true,
		})
	rawFileCopyExecutable = pctx.AndroidStaticRule("rawFileCopyExecutable",
		blueprint.RuleParams{
			Command2: blueprint.NewCommand(
				"if ! ", Cmp, " -s $in $out; then ", Cp, " $in $out; fi && ", Chmod, " +x $out"),
			Description: "copy raw exectuable file $out",
			Restat:      true,
		})
)

type rawFileInfo struct {
	relPath         string
	contentForTests string
}

var rawFileSetKey OnceKey = NewOnceKey("raw file set")

func getRawFileSet(config Config) *syncmap.SyncMap[string, rawFileInfo] {
	return config.Once(rawFileSetKey, func() any {
		return &syncmap.SyncMap[string, rawFileInfo]{}
	}).(*syncmap.SyncMap[string, rawFileInfo])
}

// ContentFromFileRuleForTests returns the content that was passed to a WriteFileRule for use
// in tests.
func ContentFromFileRuleForTests(t *testing.T, ctx *TestContext, params TestingBuildParams) string {
	t.Helper()
	if params.Rule != rawFileCopy && params.Rule != rawFileCopyExecutable {
		t.Errorf("expected params.Rule to be rawFileCopy or rawFileCopyExecutable, was %q", params.Rule)
		return ""
	}

	key := filepath.Base(params.Input.String())
	rawFileSet := getRawFileSet(ctx.Config())
	rawFileInfo, _ := rawFileSet.Load(key)

	return rawFileInfo.contentForTests
}

func rawFilesSingletonFactory() Singleton {
	return &rawFilesSingleton{}
}

type rawFilesSingleton struct{}

func (rawFilesSingleton) GenerateBuildActions(ctx SingletonContext) {
	if ctx.Config().captureBuild {
		// Nothing to do when running in tests, no temporary files were created.
		return
	}
	if ctx.GetIncrementalAnalysis() {
		// Don't try to delete the raw files for incremental build, otherwise all the
		// raw files that were originally created by the skipped build actions will be lost.
		return
	}
	rawFileSet := getRawFileSet(ctx.Config())
	rawFilesDir := PathForOutput(ctx, "raw"+ctx.Config().MakeSuffix()).String()
	absRawFilesDir := absolutePath(rawFilesDir)
	err := filepath.WalkDir(absRawFilesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Ignore obsolete directories for now.
			return nil
		}

		// Assume the basename of the file is a hash
		key := filepath.Base(path)
		relPath, err := filepath.Rel(absRawFilesDir, path)
		if err != nil {
			return err
		}

		// Check if a file with the same hash was written by this run of soong_build.  If the file was not written,
		// or if a file with the same hash was written but to a different path in the raw directory, then delete it.
		// Checking that the path matches allows changing the structure of the raw directory, for example to increase
		// the sharding.
		rawFileInfo, written := rawFileSet.Load(key)
		if !written || rawFileInfo.relPath != relPath {
			os.Remove(path)
		}
		return nil
	})
	if err != nil {
		panic(fmt.Errorf("failed to clean %q: %w", rawFilesDir, err))
	}
}
