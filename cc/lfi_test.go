// Copyright 2026 Google Inc. All rights reserved.
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
	"testing"

	"android/soong/android"
)

var prepareForLfiTest = android.GroupFixturePreparers(
	prepareForCcTest,
	android.FixtureModifyConfig(func(config android.Config) {
		config.CompileTargets().AndroidLFITarget = config.CompileTargets().Targets[android.Android][0]
		config.CompileTargets().AndroidLFITarget.LFI = true
	}),
)

func TestBasicLfi(t *testing.T) {
	t.Parallel()
	bp := `
	cc_binary {
		name: "my_tool",
		srcs: ["src.c"],
		lfi_libs: ["my_lfi_lib"],
		stl: "none",
	}
	cc_binary {
		name: "my_lfi_lib",
		srcs: ["lib.c"],
		version_script: "symbols.map.txt",
		lfi_supported: true,
		lfi: {
			enabled: true,
		},
	    static_executable: true,
		stl: "none",
	}
	`

	result := prepareForLfiTest.RunTestWithBp(t, bp)

	tool := result.ModuleForTests(t, "my_tool", "android_arm64_armv8-a")
	lfiLib := result.ModuleForTests(t, "my_lfi_lib", "android_lfi_arm64_armv8-a_lfi_stores_and_loads")
	if !android.HasDirectDep(result, tool.Module(), lfiLib.Module()) {
		t.Errorf("base variant of '%s' missing dependency on the LFI variant of '%s'", tool.Module().Name(), lfiLib.Module().Name())
	}

	// Assert that my_tool builds its own source files,
	// and also the exported lfi source files from my_lfi_lib
	tool.Output("obj/src.o")
	tool.Output("obj/.intermediates/my_lfi_lib/android_lfi_arm64_armv8-a_lfi_stores_and_loads/gen/lfi_bind_gendir/init.o")
	tool.Output("obj/.intermediates/my_lfi_lib/android_lfi_arm64_armv8-a_lfi_stores_and_loads/gen/lfi_bind_gendir/trampolines.o")
}

func TestLfiMissingStaticExecutable(t *testing.T) {
	t.Parallel()
	bp := `
	cc_binary {
		name: "my_tool",
		srcs: ["src.c"],
		lfi_libs: ["my_lfi_lib"],
		stl: "none",
	}
	cc_binary {
		name: "my_lfi_lib",
		srcs: ["lib.c"],
		version_script: "symbols.map.txt",
		lfi_supported: true,
		lfi: {
			enabled: true,
		},
		stl: "none",
	}
	`

	prepareForLfiTest.
		ExtendWithErrorHandler(android.FixtureExpectsOneErrorPattern(
			"Dynamic lfi binaries not supported, add static_executable: true")).
		RunTestWithBp(t, bp)
}

func TestLfiMissingVersionScript(t *testing.T) {
	t.Parallel()
	bp := `
	cc_binary {
		name: "my_tool",
		srcs: ["src.c"],
		lfi_libs: ["my_lfi_lib"],
		stl: "none",
	}
	cc_binary {
		name: "my_lfi_lib",
		srcs: ["lib.c"],
		static_executable: true,
		lfi_supported: true,
		lfi: {
			enabled: true,
		},
		stl: "none",
	}
	`

	prepareForLfiTest.
		ExtendWithErrorHandler(android.FixtureExpectsOneErrorPattern(
			"version_script property required to use LFI")).
		RunTestWithBp(t, bp)
}

func TestNoLfiPropertiesOnStaticLib(t *testing.T) {
	t.Parallel()
	bp := `
	cc_binary {
		name: "my_tool",
		srcs: ["src.c"],
		static_libs: ["my_lfi_lib"],
		stl: "none",
	}
	cc_library {
		name: "my_lfi_lib",
		srcs: ["lib.c"],
		lfi_supported: true,
		lfi: {
			enabled: true,
		},
		stl: "none",
	}
	`

	prepareForLfiTest.
		ExtendWithErrorHandler(android.FixtureExpectsOneErrorPattern(
			"unrecognized property \"lfi.enabled\"")).
		RunTestWithBp(t, bp)
}

func TestNoStoresOnlyYet(t *testing.T) {
	t.Parallel()
	bp := `
	cc_binary {
		name: "my_tool",
		srcs: ["src.c"],
		static_libs: ["my_lfi_lib"],
		stl: "none",
	}
	cc_binary {
		name: "my_lfi_lib",
		srcs: ["lib.c"],
		version_script: "symbols.map.txt",
		lfi_supported: true,
		lfi: {
			enabled: true,
			stores_only: true,
		},
	    static_executable: true,
		stl: "none",
	}
	`

	prepareForLfiTest.
		ExtendWithErrorHandler(android.FixtureExpectsOneErrorPattern(
			"lfi.stores_only: stores_only mode not supported yet")).
		RunTestWithBp(t, bp)
}

func TestNoRlboxYet(t *testing.T) {
	t.Parallel()
	bp := `
	cc_binary {
		name: "my_tool",
		srcs: ["src.c"],
		static_libs: ["my_lfi_lib"],
		stl: "none",
	}
	cc_binary {
		name: "my_lfi_lib",
		srcs: ["lib.c"],
		version_script: "symbols.map.txt",
		lfi_supported: true,
		lfi: {
			enabled: true,
			use_rlbox: true,
		},
	    static_executable: true,
		stl: "none",
	}
	`

	prepareForLfiTest.
		ExtendWithErrorHandler(android.FixtureExpectsOneErrorPattern(
			"lfi.use_rlbox: use_rlbox not supported yet")).
		RunTestWithBp(t, bp)
}
