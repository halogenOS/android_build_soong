// Copyright 2022 Google Inc. All rights reserved.
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

package cc

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"android/soong/android"
)

func TestBinaryLinkerScripts(t *testing.T) {
	t.Parallel()
	result := PrepareForIntegrationTestWithCc.RunTestWithBp(t, `
		cc_binary {
			name: "foo",
			srcs: ["foo.cc"],
			linker_scripts: ["foo.ld", "bar.ld"],
		}`)

	binFoo := result.ModuleForTests(t, "foo", "android_arm64_armv8-a").Rule("ld")

	android.AssertStringListContains(t, "missing dependency on linker_scripts",
		binFoo.Implicits.Strings(), "foo.ld")
	android.AssertStringListContains(t, "missing dependency on linker_scripts",
		binFoo.Implicits.Strings(), "bar.ld")
	android.AssertStringDoesContain(t, "missing flag for linker_scripts",
		binFoo.Args["ldFlags"], "-Wl,--script,foo.ld")
	android.AssertStringDoesContain(t, "missing flag for linker_scripts",
		binFoo.Args["ldFlags"], "-Wl,--script,bar.ld")
}

func TestBinaryLibs(t *testing.T) {
	t.Parallel()
	bp := `
		cc_binary_host {
			name: "foo",
			srcs: ["foo.cc"],
			static_libs: ["libstatic"],
			shared_libs: ["libshared"],
			system_shared_libs: ["libsystem"],
			static: {
				static_libs: ["libstatic_static"],
			},
			shared: {
				static_libs: ["libstatic_shared"],
				shared_libs: ["libshared_shared"],
			},
			stl: "none",
		}
	`

	libs := []string{"libstatic", "libshared", "libstatic_static", "libshared_static", "libstatic_shared", "libshared_shared", "libsystem"}
	for _, lib := range libs {
		bp += fmt.Sprintf(`cc_library { name: "%s", host_supported: true }`+"\n", lib)
	}

	testCases := []struct {
		name      string
		preparer  android.FixturePreparer
		linuxOnly bool
		static    []string
		shared    []string
	}{
		{
			name:     "normal",
			preparer: nil,
			static:   []string{"libstatic", "libstatic_shared"},
			shared:   []string{"libshared", "libshared_shared", "libsystem"},
		},
		{
			name: "BUILD_HOST_static=1",
			preparer: android.FixtureModifyConfig(func(config android.Config) {
				config.TestProductVariables.HostStaticBinaries = BoolPtr(true)
			}),
			linuxOnly: true,
			static:    []string{"libstatic", "libstatic_static", "libsystem"},
			shared:    nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.linuxOnly && runtime.GOOS != "linux" {
				t.Skip("test only supported on linux")
			}

			result := android.GroupFixturePreparers(
				PrepareForIntegrationTestWithCc,
				android.OptionalFixturePreparer(testCase.preparer),
			).RunTestWithBp(t, bp)

			binFoo := result.ModuleForTests(t, "foo", result.Config.CompileTargets().BuildOSTarget.String()).Rule("ld")
			var binFooSharedLibs []string
			var binFooStaticLibs []string
			for _, dep := range binFoo.Implicits {
				if strings.HasSuffix(dep.Base(), ".so.toc") {
					binFooSharedLibs = append(binFooSharedLibs, strings.TrimSuffix(dep.Base(), ".so.toc"))
				}
				if strings.HasSuffix(dep.Base(), ".dylib.toc") {
					binFooSharedLibs = append(binFooSharedLibs, strings.TrimSuffix(dep.Base(), ".dylib.toc"))
				}
				if strings.HasSuffix(dep.Base(), ".a") {
					binFooStaticLibs = append(binFooStaticLibs, strings.TrimSuffix(dep.Base(), ".a"))
				}
			}

			if g, w := binFooSharedLibs, testCase.shared; !slices.Equal(g, w) {
				t.Errorf("incorrect shared libs, expected %q got %q", w, g)
			}
			if g, w := binFooStaticLibs, testCase.static; !slices.Equal(g, w) {
				t.Errorf("incorrect static libs, expected %q got %q", w, g)
			}
		})
	}

}
