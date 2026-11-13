// Copyright 2019 Google Inc. All rights reserved.
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

package apex

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"android/soong/android"
	"android/soong/java"
)

func testDexpreoptBoot(t *testing.T, ruleFile string, expectedInputs, expectedOutputs []string, preferPrebuilt bool) {
	bp := `
		// Platform.

		java_sdk_library {
			name: "foo",
			srcs: ["a.java"],
			api_packages: ["foo"],
		}

		java_library {
			name: "bar",
			srcs: ["b.java"],
			installable: true,
			system_ext_specific: true,
		}

		dex_import {
			name: "baz",
			jars: ["a.jar"],
		}

		platform_bootclasspath {
			name: "platform-bootclasspath",
			fragments: [
				{
					apex: "com.android.art",
					module: "art-bootclasspath-fragment",
				},
				{
					apex: "com.android.os.statsd",
					module: "com.android.os.statsd-bootclasspath-fragment",
				},
				{
					apex: "com.android.connectivity",
					module: "com.android.connectivity-bootclasspath-fragment",
				},
			],
		}

		// Source ART APEX.

		java_library {
			name: "core-oj",
			srcs: ["core-oj.java"],
			installable: true,
			apex_available: [
				"com.android.art",
			],
		}

		bootclasspath_fragment {
			name: "art-bootclasspath-fragment",
			image_name: "art",
			contents: ["core-oj"],
			apex_available: [
				"com.android.art",
			],
			dex_preopt: {
				profile: "art/build/boot/boot-image-profile.txt",
			},
			hidden_api: {
				split_packages: ["*"],
			},
		}

		apex_key {
			name: "com.android.art.key",
			public_key: "com.android.art.avbpubkey",
			private_key: "com.android.art.pem",
		}

		apex {
			name: "com.android.art",
			key: "com.android.art.key",
			bootclasspath_fragments: ["art-bootclasspath-fragment"],
			updatable: false,
		}

		// Prebuilt ART APEX.

		java_import {
			name: "core-oj",
			prefer: %[1]t,
			jars: ["core-oj.jar"],
			apex_available: [
				"com.android.art",
			],
		}

		prebuilt_bootclasspath_fragment {
			name: "art-bootclasspath-fragment",
			prefer: %[1]t,
			image_name: "art",
			contents: ["core-oj"],
			hidden_api: {
				annotation_flags: "my-bootclasspath-fragment/annotation-flags.csv",
				metadata: "my-bootclasspath-fragment/metadata.csv",
				index: "my-bootclasspath-fragment/index.csv",
				stub_flags: "my-bootclasspath-fragment/stub-flags.csv",
				all_flags: "my-bootclasspath-fragment/all-flags.csv",
			},
			apex_available: [
				"com.android.art",
			],
		}

		prebuilt_apex {
			name: "com.android.art",
			prefer: %[1]t,
			apex_name: "com.android.art",
			src: "com.android.art-arm.apex",
			exported_bootclasspath_fragments: ["art-bootclasspath-fragment"],
		}

		apex_contributions {
			name: "prebuilt_art_contributions",
			contents: ["prebuilt_com.android.art"],
			api_domain: "com.android.art",
		}

		// Source BCP Mainline APEXs
		// Statsd.
		java_library {
			name: "framework-statsd",
			srcs: ["framework-statsd.java"],
			installable: true,
			apex_available: [
				"com.android.os.statsd",
			],
		}

		bootclasspath_fragment {
			name: "com.android.os.statsd-bootclasspath-fragment",
			contents: ["framework-statsd"],
			apex_available: [
				"com.android.os.statsd",
			],
			dex_preopt: {
				profile: "packages/modules/StatsD/framework/boot-image-profile.txt",
			},
			fragments: [
				{
					apex: "com.android.art",
					module: "art-bootclasspath-fragment",
				},
			],
			hidden_api: {
				split_packages: ["*"],
			},
		}

		apex_key {
			name: "com.android.os.statsd.key",
			public_key: "com.android.os.statsd.avbpubkey",
			private_key: "com.android.os.statsd.pem",
		}

		apex {
			name: "com.android.os.statsd",
			key: "com.android.os.statsd.key",
			bootclasspath_fragments: ["com.android.os.statsd-bootclasspath-fragment"],
			updatable: false,
		}

		// Prebuilt Statsd APEX.
		java_import {
			name: "framework-statsd",
			prefer: %[1]t,
			jars: ["framework-statsd.jar"],
			apex_available: [
				"com.android.os.statsd",
			],
		}

		prebuilt_bootclasspath_fragment {
			name: "com.android.os.statsd-bootclasspath-fragment",
			prefer: %[1]t,
			contents: ["framework-statsd"],
			dex_preopt: {
				profile_guided: true,
			},
			fragments: [
				{
					apex: "com.android.art",
					module: "art-bootclasspath-fragment",
				},
			],
			hidden_api: {
				annotation_flags: "my-bootclasspath-fragment/annotation-flags.csv",
				metadata: "my-bootclasspath-fragment/metadata.csv",
				index: "my-bootclasspath-fragment/index.csv",
				stub_flags: "my-bootclasspath-fragment/stub-flags.csv",
				all_flags: "my-bootclasspath-fragment/all-flags.csv",
			},
			apex_available: [
				"com.android.os.statsd",
			],
		}

		prebuilt_apex {
			name: "com.android.os.statsd",
			prefer: %[1]t,
			apex_name: "com.android.os.statsd",
			src: "com.android.os.statsd-arm.apex",
			exported_bootclasspath_fragments: ["com.android.os.statsd-bootclasspath-fragment"],
		}

		apex_contributions {
			name: "prebuilt_statsd_contributions",
			contents: ["prebuilt_com.android.os.statsd"],
			api_domain: "com.android.os.statsd",
		}

		// Connectivity.
		java_library {
			name: "framework-connectivity",
			srcs: ["framework-connectivity.java"],
			installable: true,
			apex_available: [
				"com.android.connectivity",
			],
		}

		bootclasspath_fragment {
			name: "com.android.connectivity-bootclasspath-fragment",
			contents: ["framework-connectivity"],
			apex_available: [
				"com.android.connectivity",
			],
			dex_preopt: {
				profile: "packages/modules/Connectivity/framework/boot-image-profile.txt",
			},
			fragments: [
				{
					apex: "com.android.art",
					module: "art-bootclasspath-fragment",
				},
			],
			hidden_api: {
				split_packages: ["*"],
			},
		}

		apex_key {
			name: "com.android.connectivity.key",
			public_key: "com.android.connectivity.avbpubkey",
			private_key: "com.android.connectivity.pem",
		}

		apex {
			name: "com.android.connectivity",
			key: "com.android.connectivity.key",
			bootclasspath_fragments: ["com.android.connectivity-bootclasspath-fragment"],
			updatable: false,
		}

		// Prebuilt Connectivity APEX.
		java_import {
			name: "framework-connectivity",
			prefer: %[1]t,
			jars: ["framework-connectivity.jar"],
			apex_available: [
				"com.android.connectivity",
			],
		}

		prebuilt_bootclasspath_fragment {
			name: "com.android.connectivity-bootclasspath-fragment",
			prefer: %[1]t,
			contents: ["framework-connectivity"],
			dex_preopt: {
				profile_guided: true,
			},
			fragments: [
				{
					apex: "com.android.art",
					module: "art-bootclasspath-fragment",
				},
			],
			hidden_api: {
				annotation_flags: "my-bootclasspath-fragment/annotation-flags.csv",
				metadata: "my-bootclasspath-fragment/metadata.csv",
				index: "my-bootclasspath-fragment/index.csv",
				stub_flags: "my-bootclasspath-fragment/stub-flags.csv",
				all_flags: "my-bootclasspath-fragment/all-flags.csv",
			},
			apex_available: [
				"com.android.connectivity",
			],
		}

		prebuilt_apex {
			name: "com.android.connectivity",
			prefer: %[1]t,
			apex_name: "com.android.connectivity",
			src: "com.android.connectivity-arm.apex",
			exported_bootclasspath_fragments: ["com.android.connectivity-bootclasspath-fragment"],
		}

		apex_contributions {
			name: "prebuilt_connectivity_contributions",
			contents: ["prebuilt_com.android.connectivity"],
			api_domain: "com.android.connectivity",
		}
	`

	fixture := android.GroupFixturePreparers(
		java.PrepareForTestWithDexpreopt,
		java.PrepareForTestWithJavaSdkLibraryFiles,
		java.FixtureWithLastReleaseApis("foo"),
		java.FixtureConfigureBootJars("com.android.art:core-oj", "platform:foo", "system_ext:bar", "platform:baz"),
		java.FixtureConfigureApexBootJars("com.android.connectivity:framework-connectivity", "com.android.os.statsd:framework-statsd"),
		android.PrepareForTestWithBuildFlag("RELEASE_ART_COMPILE_BCP_APEX_SPEED_PROFILE", "true"),
		PrepareForTestWithApexBuildComponents,
		prepareForTestWithArtApex,
		prepareForTestWithMainlineApex,
	)
	if preferPrebuilt {
		fixture = android.GroupFixturePreparers(
			fixture,
			android.PrepareForTestWithBuildFlag("RELEASE_APEX_CONTRIBUTIONS_ART", "prebuilt_art_contributions"),
			android.PrepareForTestWithBuildFlag("RELEASE_APEX_CONTRIBUTIONS_STATSD", "prebuilt_statsd_contributions"),
			android.PrepareForTestWithBuildFlag("RELEASE_APEX_CONTRIBUTIONS_CONNECTIVITY", "prebuilt_connectivity_contributions"),
		)
	}
	result := fixture.RunTestWithBp(t, fmt.Sprintf(bp, preferPrebuilt))

	dexBootJars := result.ModuleForTests(t, "dex_bootjars", "android_common")
	rule := dexBootJars.Output(ruleFile)

	inputs := rule.Implicits.Strings()
	sort.Strings(inputs)
	sort.Strings(expectedInputs)

	outputs := append(android.WritablePaths{rule.Output}, rule.ImplicitOutputs...).Strings()
	sort.Strings(outputs)
	sort.Strings(expectedOutputs)

	android.AssertStringPathsRelativeToTopEquals(t, "inputs", result.Config, expectedInputs, inputs)

	android.AssertStringPathsRelativeToTopEquals(t, "outputs", result.Config, expectedOutputs, outputs)
}

func TestDexpreoptBootJarsWithSourceArtApex(t *testing.T) {
	t.Parallel()
	ruleFile := "out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.art"

	expectedInputs := []string{
		"out/soong/dexpreopt_arm64/dex_bootjars_input/core-oj.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/foo.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/bar.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/baz.jar",
		"out/soong/.intermediates/art-bootclasspath-fragment/android_common_com.android.art/art-bootclasspath-fragment/boot.prof",
		// Connectivity and Statsd profiles are here since we include all profiles from the bootclasspath fragments, but dex2oat will select only the one corresponding to the apex that is being built.
		"out/soong/.intermediates/com.android.os.statsd-bootclasspath-fragment/android_common_com.android.os.statsd/com.android.os.statsd-bootclasspath-fragment/boot.prof",
		"out/soong/.intermediates/com.android.connectivity-bootclasspath-fragment/android_common_com.android.connectivity/com.android.connectivity-bootclasspath-fragment/boot.prof",
		"out/soong/.intermediates/default/java/dex_bootjars/android_common/boot/boot.prof",
		"out/soong/dexpreopt/uffd_gc_flag.txt",
		"out/soong/dexpreopt/assume_value_flags.txt",
		"out/soong/dexpreopt/allow_profile_code_flag.txt",
	}

	expectedOutputs := []string{
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.invocation",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot-foo.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot-bar.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot-baz.oat",
	}

	testDexpreoptBoot(t, ruleFile, expectedInputs, expectedOutputs, false)
}

func TestDexpreoptBootJarsWithSourceMainlineApex(t *testing.T) {
	t.Parallel()
	ruleFile := "out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.art"

	expectedInputs := []string{
		"out/soong/dexpreopt/allow_profile_code_flag.txt",
		"out/soong/dexpreopt/assume_value_flags.txt",
		"out/soong/dexpreopt/uffd_gc_flag.txt",
		"out/soong/dexpreopt_arm64/dex_mainlinejars_input/framework-statsd.jar",
		"out/soong/dexpreopt_arm64/dex_mainlinejars_input/framework-connectivity.jar",
		// ART profile is here since we include all profiles from the bootclasspath fragments, but dex2oat will select only the one corresponding to the apex that is being built.
		"out/soong/.intermediates/com.android.connectivity-bootclasspath-fragment/android_common_com.android.connectivity/com.android.connectivity-bootclasspath-fragment/boot.prof",
		"out/soong/.intermediates/com.android.os.statsd-bootclasspath-fragment/android_common_com.android.os.statsd/com.android.os.statsd-bootclasspath-fragment/boot.prof",
		"out/soong/.intermediates/art-bootclasspath-fragment/android_common_com.android.art/art-bootclasspath-fragment/boot.prof",
		// ART related inputs
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/bar.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/baz.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/core-oj.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/foo.jar",
	}

	expectedOutputs := []string{
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot.invocation",
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.art",
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.oat",
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.vdex",
		"out/soong/dexpreopt_arm64/dex_mainlinejars_unstripped/android/system/framework/arm64/boot-framework-connectivity.oat",
	}

	testDexpreoptBoot(t, ruleFile, expectedInputs, expectedOutputs, false)
}

// The only difference is that the ART profile should be deapexed from the prebuilt APEX. Other
// inputs and outputs should be the same as above.
func TestDexpreoptBootJarsWithPrebuiltArtApex(t *testing.T) {
	t.Parallel()
	ruleFile := "out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.art"

	expectedInputs := []string{
		"out/soong/dexpreopt_arm64/dex_bootjars_input/core-oj.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/foo.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/bar.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/baz.jar",
		// Connectivity and Statsd profiles are here since we include all profiles from the bootclasspath fragments, but dex2oat will select only the one corresponding to the apex that is being built.
		"out/soong/.intermediates/prebuilt_com.android.os.statsd/android_common_prebuilt_com.android.os.statsd/deapexer/etc/boot-image.prof",
		"out/soong/.intermediates/prebuilt_com.android.connectivity/android_common_prebuilt_com.android.connectivity/deapexer/etc/boot-image.prof",
		"out/soong/.intermediates/prebuilt_com.android.art/android_common_prebuilt_com.android.art/deapexer/etc/boot-image.prof",
		"out/soong/.intermediates/default/java/dex_bootjars/android_common/boot/boot.prof",
		"out/soong/dexpreopt/uffd_gc_flag.txt",
		"out/soong/dexpreopt/assume_value_flags.txt",
		"out/soong/dexpreopt/allow_profile_code_flag.txt",
	}

	expectedOutputs := []string{
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.invocation",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot-foo.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot-bar.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars_unstripped/android/system/framework/arm64/boot-baz.oat",
	}

	testDexpreoptBoot(t, ruleFile, expectedInputs, expectedOutputs, true)
}

// The only difference is that the Mainline profile should be deapexed from the
// prebuilt Mainline APEXs.
func TestDexpreoptBootJarsWithPrebuiltMainlineApex(t *testing.T) {
	t.Parallel()
	ruleFile := "out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.art"

	expectedInputs := []string{
		"out/soong/dexpreopt/allow_profile_code_flag.txt",
		"out/soong/dexpreopt/assume_value_flags.txt",
		"out/soong/dexpreopt/uffd_gc_flag.txt",
		"out/soong/dexpreopt_arm64/dex_mainlinejars_input/framework-statsd.jar",
		"out/soong/dexpreopt_arm64/dex_mainlinejars_input/framework-connectivity.jar",
		// ART profile is here since we include all profiles from the bootclasspath fragments, but dex2oat will select only the one corresponding to the apex that is being built.
		"out/soong/.intermediates/prebuilt_com.android.connectivity/android_common_prebuilt_com.android.connectivity/deapexer/etc/boot-image.prof",
		"out/soong/.intermediates/prebuilt_com.android.os.statsd/android_common_prebuilt_com.android.os.statsd/deapexer/etc/boot-image.prof",
		"out/soong/.intermediates/prebuilt_com.android.art/android_common_prebuilt_com.android.art/deapexer/etc/boot-image.prof",
		// ART related inputs
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-bar.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-baz.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot-foo.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.art",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.oat",
		"out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.vdex",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/bar.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/baz.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/core-oj.jar",
		"out/soong/dexpreopt_arm64/dex_bootjars_input/foo.jar",
	}

	expectedOutputs := []string{
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot.invocation",
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.art",
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.oat",
		"out/soong/dexpreopt_arm64/dex_mainlinejars/android/system/framework/arm64/boot-framework-connectivity.vdex",
		"out/soong/dexpreopt_arm64/dex_mainlinejars_unstripped/android/system/framework/arm64/boot-framework-connectivity.oat",
	}

	testDexpreoptBoot(t, ruleFile, expectedInputs, expectedOutputs, true)
}

// Changes to the boot.zip structure may break the ART APK scanner.
func TestDexpreoptBootZip(t *testing.T) {
	t.Parallel()
	ruleFile := "boot.zip"

	ctx := android.PathContextForTesting(android.TestArchConfig("", nil, "", nil))
	expectedInputs := []string{}
	for _, target := range ctx.Config().CompileTargets().Targets[android.Android] {
		for _, ext := range []string{".art", ".oat", ".vdex"} {
			for _, suffix := range []string{"", "-foo", "-bar", "-baz"} {
				expectedInputs = append(expectedInputs,
					filepath.Join(
						"out/soong/dexpreopt_arm64/dex_bootjars",
						target.Os.String(),
						"system/framework",
						target.Arch.ArchType.String(),
						"boot"+suffix+ext))
			}
		}
	}

	expectedOutputs := []string{
		"out/soong/dexpreopt_arm64/dex_bootjars/boot.zip",
	}

	testDexpreoptBoot(t, ruleFile, expectedInputs, expectedOutputs, false)
}

// Multiple ART apexes might exist in the tree.
// The profile should correspond to the apex selected using release build flags
func TestDexpreoptProfileWithMultiplePrebuiltArtApexes(t *testing.T) {
	t.Parallel()
	ruleFile := "out/soong/dexpreopt_arm64/dex_bootjars/android/system/framework/arm64/boot.art"
	bp := `
		// Platform.

		platform_bootclasspath {
			name: "platform-bootclasspath",
			fragments: [
				{
					apex: "com.android.art",
					module: "art-bootclasspath-fragment",
				},
			],
		}

		// Source ART APEX.

		java_library {
			name: "core-oj",
			srcs: ["core-oj.java"],
			installable: true,
			apex_available: [
				"com.android.art",
			],
		}

		bootclasspath_fragment {
			name: "art-bootclasspath-fragment",
			image_name: "art",
			contents: ["core-oj"],
			apex_available: [
				"com.android.art",
			],
			dex_preopt: {
				profile: "art/build/boot/boot-image-profile.txt",
			},
			hidden_api: {
				split_packages: ["*"],
			},
		}

		apex_key {
			name: "com.android.art.key",
			public_key: "com.android.art.avbpubkey",
			private_key: "com.android.art.pem",
		}

		apex {
			name: "com.android.art",
			key: "com.android.art.key",
			bootclasspath_fragments: ["art-bootclasspath-fragment"],
			updatable: false,
		}

		// Prebuilt ART APEX.

		java_import {
			name: "core-oj",
			jars: ["core-oj.jar"],
			apex_available: [
				"com.android.art",
			],
		}

		prebuilt_bootclasspath_fragment {
			name: "art-bootclasspath-fragment",
			image_name: "art",
			contents: ["core-oj"],
			dex_preopt: {
				profile_guided: true,
			},
			hidden_api: {
				annotation_flags: "my-bootclasspath-fragment/annotation-flags.csv",
				metadata: "my-bootclasspath-fragment/metadata.csv",
				index: "my-bootclasspath-fragment/index.csv",
				stub_flags: "my-bootclasspath-fragment/stub-flags.csv",
				all_flags: "my-bootclasspath-fragment/all-flags.csv",
			},
			apex_available: [
				"com.android.art",
			],
		}

		prebuilt_apex {
			name: "com.android.art",
			apex_name: "com.android.art",
			src: "com.android.art-arm.apex",
			exported_bootclasspath_fragments: ["art-bootclasspath-fragment"],
		}

		// Another Prebuilt ART APEX
		prebuilt_apex {
			name: "com.android.art.v2",
			apex_name: "com.android.art", // Used to determine the API domain
			src: "com.android.art-arm.apex",
			exported_bootclasspath_fragments: ["art-bootclasspath-fragment"],
		}

		// APEX contribution modules

		apex_contributions {
			name: "art.source.contributions",
			api_domain: "com.android.art",
			contents: ["com.android.art"],
		}

		apex_contributions {
			name: "art.prebuilt.contributions",
			api_domain: "com.android.art",
			contents: ["prebuilt_com.android.art"],
		}

		apex_contributions {
			name: "art.prebuilt.v2.contributions",
			api_domain: "com.android.art",
			contents: ["com.android.art.v2"], // prebuilt_ prefix is missing because of prebuilt_rename mutator
		}

	`

	testCases := []struct {
		desc                         string
		selectedArtApexContributions string
		expectedProfile              string
	}{
		{
			desc:                         "Source apex com.android.art is selected, profile should come from source java library",
			selectedArtApexContributions: "art.source.contributions",
			expectedProfile:              "out/soong/.intermediates/art-bootclasspath-fragment/android_common_com.android.art/art-bootclasspath-fragment/boot.prof",
		},
		{
			desc:                         "Prebuilt apex prebuilt_com.android.art is selected, profile should come from .prof deapexed from the prebuilt",
			selectedArtApexContributions: "art.prebuilt.contributions",
			expectedProfile:              "out/soong/.intermediates/prebuilt_com.android.art/android_common_prebuilt_com.android.art/deapexer/etc/boot-image.prof",
		},
		{
			desc:                         "Prebuilt apex prebuilt_com.android.art.v2 is selected, profile should come from .prof deapexed from the prebuilt",
			selectedArtApexContributions: "art.prebuilt.v2.contributions",
			expectedProfile:              "out/soong/.intermediates/com.android.art.v2/android_common_prebuilt_com.android.art/deapexer/etc/boot-image.prof",
		},
	}
	for _, tc := range testCases {
		result := android.GroupFixturePreparers(
			java.PrepareForTestWithDexpreopt,
			java.PrepareForTestWithJavaSdkLibraryFiles,
			java.FixtureConfigureBootJars("com.android.art:core-oj"),
			PrepareForTestWithApexBuildComponents,
			prepareForTestWithArtApex,
			android.PrepareForTestWithBuildFlag("RELEASE_APEX_CONTRIBUTIONS_ART", tc.selectedArtApexContributions),
		).RunTestWithBp(t, bp)

		dexBootJars := result.ModuleForTests(t, "dex_bootjars", "android_common")
		rule := dexBootJars.Output(ruleFile)

		inputs := rule.Implicits.Strings()
		android.AssertStringListContains(t, tc.desc, inputs, tc.expectedProfile)
	}
}

// Check that dexpreopt works with Google mainline prebuilts even in workspaces where source is missing
func TestDexpreoptWithMainlinePrebuiltNoSource(t *testing.T) {
	t.Parallel()
	bp := `
		// Platform.

		platform_bootclasspath {
			name: "platform-bootclasspath",
			fragments: [
				{
					apex: "com.android.art",
					module: "art-bootclasspath-fragment",
				},
			],
		}

		// Source AOSP ART apex
		java_library {
			name: "core-oj",
			srcs: ["core-oj.java"],
			installable: true,
			apex_available: [
				"com.android.art",
			],
		}

		bootclasspath_fragment {
			name: "art-bootclasspath-fragment",
			image_name: "art",
			contents: ["core-oj"],
			apex_available: [
				"com.android.art",
			],
			dex_preopt: {
				profile: "art/build/boot/boot-image-profile.txt",
			},
			hidden_api: {
				split_packages: ["*"],
			},
		}

		apex_key {
			name: "com.android.art.key",
			public_key: "com.android.art.avbpubkey",
			private_key: "com.android.art.pem",
		}

		apex {
			name: "com.android.art",
			key: "com.android.art.key",
			bootclasspath_fragments: ["art-bootclasspath-fragment"],
			updatable: false,
		}


		// Prebuilt Google ART APEX.

		java_import {
			name: "core-oj",
			jars: ["core-oj.jar"],
			apex_available: [
				"com.android.art",
			],
		}

		prebuilt_bootclasspath_fragment {
			name: "art-bootclasspath-fragment",
			image_name: "art",
			contents: ["core-oj"],
			dex_preopt: {
				profile_guided: true,
			},
			hidden_api: {
				annotation_flags: "my-bootclasspath-fragment/annotation-flags.csv",
				metadata: "my-bootclasspath-fragment/metadata.csv",
				index: "my-bootclasspath-fragment/index.csv",
				stub_flags: "my-bootclasspath-fragment/stub-flags.csv",
				all_flags: "my-bootclasspath-fragment/all-flags.csv",
			},
			apex_available: [
				"com.android.art",
			],
		}

		prebuilt_apex {
			name: "com.google.android.art",
			apex_name: "com.android.art",
			src: "com.android.art-arm.apex",
			exported_bootclasspath_fragments: ["art-bootclasspath-fragment"],
		}

		apex_contributions {
			name: "art.prebuilt.contributions",
			api_domain: "com.android.art",
			contents: ["prebuilt_com.google.android.art"],
		}
	`
	res := android.GroupFixturePreparers(
		java.PrepareForTestWithDexpreopt,
		java.PrepareForTestWithJavaSdkLibraryFiles,
		java.FixtureConfigureBootJars("com.android.art:core-oj"),
		PrepareForTestWithApexBuildComponents,
		prepareForTestWithArtApex,
		android.PrepareForTestWithBuildFlag("RELEASE_APEX_CONTRIBUTIONS_ART", "art.prebuilt.contributions"),
	).RunTestWithBp(t, bp)
	if !java.CheckModuleHasDependency(t, res.TestContext, "dex_bootjars", "android_common", "prebuilt_com.google.android.art") {
		t.Errorf("Expected dexpreopt to use prebuilt apex")
	}
}
