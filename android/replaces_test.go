// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"os"
	"strings"
	"testing"
)

var prepareForTestWithReplaces = FixtureRegisterWithContext(func(ctx RegistrationContext) {
	ctx.PrePartialMutators(RegisterReplacesCheckMutator)
})

var prepareForReplacesWithPrebuilts = GroupFixturePreparers(
	prepareForTestWithNamespace,
	prepareForTestWithReplaces,
	PrepareForTestWithArchMutator,
	PrepareForTestWithPrebuilts,
	PrepareForTestWithOverrides,
	FixtureRegisterWithContext(registerTestPrebuiltModules),
)

// dirsOfModulesNamed returns the directories of the modules named name.
func dirsOfModulesNamed(result *TestResult, name string) []string {
	var dirs []string
	result.VisitAllModules(func(m Module) {
		if result.ModuleName(m) == name {
			dirs = append(dirs, result.ModuleDir(m))
		}
	})
	return SortedUniqueStrings(dirs)
}

// directDeps returns the direct dependencies of the module named name.
func directDeps(t *testing.T, result *TestResult, name string) []Module {
	var deps []Module
	result.VisitDirectDeps(result.ModuleForTests(t, name, "android_common").Module(), func(m Module) {
		deps = append(deps, m)
	})
	return deps
}

func TestReplacingModuleTakesTheName(t *testing.T) {
	// dir1 is read before dir1/dir2, so the replaced definition arrives first. The Blueprint
	// tests cover both arrival orders.
	result := GroupFixturePreparers(
		prepareForTestWithNamespace,
		prepareForTestWithReplaces,
		dirBpToPreparer(map[string]string{
			".": `
				test_module {
					name: "b",
					deps: ["a"],
				}
			`,
			"dir1": `
				test_module {
					name: "a",
					id: "replaced",
				}
			`,
			"dir1/dir2": `
				test_module {
					name: "a",
					id: "replacing",
					replaces: ["//dir1:a"],
				}
			`,
		}),
	).RunTest(t)

	if findModuleById(result, "replaced").module != nil {
		t.Errorf("the replaced definition of a is still in the build")
	}
	a := findModuleById(result, "replacing")
	if !dependsOn(result, getModule(result, "b"), a) {
		t.Errorf("module b does not depend on the replacing definition of a")
	}
	AssertDeepEquals(t, "Make names of the replacing definition", []string(nil),
		a.Module().base().commonProperties.ReplacedMakeNames)
}

func TestReplacesInDefaultsModule(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForDefaultsTest,
		prepareForTestWithReplaces,
		dirBpToPreparer(map[string]string{
			".": `
				test {
					name: "foo",
					defaults: ["defaults"],
				}
			`,
			"dir1": `
				defaults {
					name: "defaults",
					foo: ["replaced"],
				}
			`,
			"dir1/dir2": `
				defaults {
					name: "defaults",
					foo: ["replacing"],
					replaces: ["//dir1:defaults"],
				}
			`,
		}),
	).RunTest(t)

	foo := result.Module("foo", "").(*defaultsTestModule)
	AssertDeepEquals(t, "foo", []string{"replacing"}, foo.properties.Foo)
	if refs, err := foo.ReplacedRefs(); refs != nil || err != nil {
		t.Errorf("foo replaces %v with error %v, want no definitions", refs, err)
	}
}

func TestReplacesAcrossNamespaces(t *testing.T) {
	for _, tc := range []struct {
		name               string
		exportedNamespaces []string
		wantReplaced       bool
	}{
		{name: "replacing namespace exported", exportedNamespaces: []string{"dir1", "dir2"}, wantReplaced: true},
		{name: "replacing namespace not exported", exportedNamespaces: []string{"dir1"}, wantReplaced: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := GroupFixturePreparers(
				prepareForTestWithNamespace,
				prepareForTestWithReplaces,
				FixtureModifyProductVariables(func(variables FixtureProductVariables) {
					variables.NamespacesToExport = tc.exportedNamespaces
				}),
				dirBpToPreparer(map[string]string{
					".": `
						test_module {
							name: "c",
							deps: ["//dir1:a"],
						}
					`,
					"dir1": `
						soong_namespace {
						}
						test_module {
							name: "a",
							id: "replaced",
						}
					`,
					"dir2": `
						soong_namespace {
						}
						test_module {
							name: "b",
							id: "replacing",
							replaces: ["//dir1:a"],
						}
					`,
				}),
			).RunTest(t)

			replacing := findModuleById(result, "replacing")
			AssertBoolEquals(t, "the replaced definition is left out", tc.wantReplaced,
				findModuleById(result, "replaced").module == nil)
			AssertBoolEquals(t, "c depends on the replacing definition", tc.wantReplaced,
				dependsOn(result, getModule(result, "c"), replacing))
			if tc.wantReplaced {
				AssertDeepEquals(t, "Make names of b", []string{"a"},
					replacing.Module().base().commonProperties.ReplacedMakeNames)
			}
		})
	}
}

func TestPrebuiltReplacesSourceOfAnotherName(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForReplacesWithPrebuilts,
		FixtureModifyProductVariables(func(variables FixtureProductVariables) {
			variables.NamespacesToExport = []string{"vendor"}
		}),
		MockFS{"vendor/qcrild": nil}.AddToFixture(),
		dirBpToPreparer(map[string]string{
			".": `
				source {
					name: "user",
					deps: [":rild"],
				}
			`,
			"rild": `
				source {
					name: "rild",
				}
			`,
			"vendor": `
				soong_namespace {
				}
				prebuilt {
					name: "qcrild",
					srcs: ["qcrild"],
					replaces: ["//rild:rild"],
				}
			`,
		}),
	).RunTest(t)

	AssertDeepEquals(t, "directories of modules named rild", []string(nil), dirsOfModulesNamed(result, "rild"))
	qcrild := result.ModuleForTests(t, "qcrild", "android_common").Module()
	AssertDeepEquals(t, "dependencies of user", []Module{qcrild}, directDeps(t, result, "user"))
	AssertDeepEquals(t, "Make names of qcrild", []string{"rild"}, qcrild.base().commonProperties.ReplacedMakeNames)
}

func TestPrebuiltReplacesDefinitionOfItsName(t *testing.T) {
	for _, tc := range []struct {
		name string
		// The start of the Android.bp file of the replacing prebuilt.
		replacingNamespace string
		replaced           string
	}{
		{
			name:               "source module in another namespace",
			replacingNamespace: "soong_namespace {}",
			replaced:           `source { name: "lib" }`,
		},
		{
			// The source module is left out, so prefer does nothing.
			name:     "source module in the same namespace",
			replaced: `source { name: "lib" }`,
		},
		{
			name:     "prebuilt in the same namespace",
			replaced: `prebuilt { name: "lib", srcs: ["lib"] }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := GroupFixturePreparers(
				prepareForReplacesWithPrebuilts,
				FixtureModifyProductVariables(func(variables FixtureProductVariables) {
					variables.NamespacesToExport = []string{"replacing"}
				}),
				MockFS{"replaced/lib": nil, "replacing/lib": nil}.AddToFixture(),
				dirBpToPreparer(map[string]string{
					".":        `source { name: "user", deps: [":lib"] }`,
					"replaced": tc.replaced,
					"replacing": tc.replacingNamespace + `
						prebuilt {
							name: "lib",
							prefer: false,
							srcs: ["lib"],
							replaces: ["//replaced:lib"],
						}
					`,
				}),
			).RunTest(t)

			AssertDeepEquals(t, "directories of modules named lib", []string{"replacing"}, dirsOfModulesNamed(result, "lib"))
			lib := result.ModuleForTests(t, "lib", "android_common").Module()
			AssertDeepEquals(t, "dependencies of user", []Module{lib}, directDeps(t, result, "user"))
			AssertDeepEquals(t, "Make names of lib", []string(nil), lib.base().commonProperties.ReplacedMakeNames)
		})
	}
}

func TestReplacesErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replaced string
		replaces string
		err      string
	}{
		{
			name:     "other partition",
			replaced: `test_module { name: "a" }`,
			replaces: `soc_specific: true, replaces: ["//.:a"]`,
			err:      `installs to vendor and cannot replace "a", which installs to system`,
		},
		{
			name:     "module that is not an Android module",
			replaced: `blueprint_test_module { name: "a" }`,
			replaces: `replaces: ["//.:a"]`,
			err:      `module "b" cannot replace "a", which is not an Android module`,
		},
		{
			name:     "entry without slashes",
			replaced: `test_module { name: "a" }`,
			replaces: `replaces: [".:a"]`,
			err:      `replaces: ".:a" is not of the form //<directory>:<name>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			GroupFixturePreparers(
				prepareForTestWithNamespace,
				prepareForTestWithReplaces,
				dirBpToPreparer(map[string]string{
					".":    tc.replaced,
					"dir1": `test_module { name: "b", ` + tc.replaces + ` }`,
				}),
			).
				ExtendWithErrorHandler(FixtureExpectsOneErrorPattern(`\Q` + tc.err + `\E`)).
				RunTest(t)
		})
	}
}

func TestReplacedDefinitionIsLeftOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{
			// The namespace dir1 is not exported to Make, as for a product that does not use it.
			name: "same name in a namespace not exported to Make",
			files: map[string]string{
				"dir1":      `soong_namespace {} test_module { name: "a", id: "replaced" }`,
				"dir1/dir2": `test_module { name: "a", replaces: ["//dir1:a"] }`,
			},
		},
		{
			name: "directory with a trailing slash",
			files: map[string]string{
				"dir1": `test_module { name: "a", id: "replaced" }`,
				"dir2": `test_module { name: "b", replaces: ["//dir1/:a"] }`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := GroupFixturePreparers(
				prepareForTestWithNamespace,
				prepareForTestWithReplaces,
				dirBpToPreparer(tc.files),
			).RunTest(t)

			if findModuleById(result, "replaced").module != nil {
				t.Errorf("the replaced definition is still in the build")
			}
		})
	}
}

func TestReplacesComparesInstallTargets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replaced  string
		replacing string
		err       string
	}{
		{
			name:      "replaced definition on vendor through defaults, replacing module through soc_specific",
			replaced:  `test { name: "a", defaults: ["vendor_defaults"] }`,
			replacing: `test { name: "b", soc_specific: true, replaces: ["//.:a"] }`,
		},
		{
			name:      "replaced definition on vendor through defaults only",
			replaced:  `test { name: "a", defaults: ["vendor_defaults"] }`,
			replacing: `test { name: "b", replaces: ["//.:a"] }`,
			err:       `installs to system and cannot replace "a", which installs to vendor`,
		},
		{
			name:      "replacing module on vendor through defaults only",
			replaced:  `test { name: "a" }`,
			replacing: `test { name: "b", defaults: ["vendor_defaults"], replaces: ["//.:a"] }`,
			err:       `installs to vendor and cannot replace "a", which installs to system`,
		},
		{
			name:      "host support of the replaced definition only",
			replaced:  `source { name: "a" }`,
			replacing: `source { name: "b", host_supported: false, replaces: ["//.:a"] }`,
			err:       `installs to system and cannot replace "a", which installs to system and the host`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errorHandler := FixtureExpectsNoErrors
			if tc.err != "" {
				errorHandler = FixtureExpectsOneErrorPattern(`\Q` + tc.err + `\E`)
			}
			// The fixtures register the mutators in the order of the build: namespaces, defaults, then
			// the check.
			GroupFixturePreparers(
				prepareForTestWithNamespace,
				prepareForDefaultsTest,
				prepareForReplacesWithPrebuilts,
				dirBpToPreparer(map[string]string{
					".":    `defaults { name: "vendor_defaults", vendor: true } ` + tc.replaced,
					"dir1": tc.replacing,
				}),
			).
				ExtendWithErrorHandler(errorHandler).
				RunTest(t)
		})
	}
}

func TestReplacesAppliesDefaultsOfReplacedNamespace(t *testing.T) {
	// The namespace of the replacing module has a defaults module of the same name that sets no
	// partition. The replaced definition gets vendor from the defaults module next to it.
	result := GroupFixturePreparers(
		prepareForTestWithNamespace,
		prepareForDefaultsTest,
		prepareForReplacesWithPrebuilts,
		FixtureModifyProductVariables(func(variables FixtureProductVariables) {
			variables.NamespacesToExport = []string{"dir1"}
		}),
		dirBpToPreparer(map[string]string{
			".": `
				defaults { name: "partition_defaults", vendor: true }
				test { name: "a", defaults: ["partition_defaults"] }
			`,
			"dir1": `
				soong_namespace {
				}
				defaults { name: "partition_defaults" }
				test { name: "b", soc_specific: true, replaces: ["//.:a"] }
			`,
		}),
	).RunTest(t)

	AssertDeepEquals(t, "Make names of b", []string{"a"},
		result.Module("b", "").(Module).base().commonProperties.ReplacedMakeNames)
}

func TestReplacedNameInAndroidMk(t *testing.T) {
	for _, tc := range []struct {
		name               string
		exportedNamespaces []string
		replacing          string
		wantPhony          bool
	}{
		{name: "replacing namespace exported", exportedNamespaces: []string{"vendor"}, wantPhony: true},
		{name: "replacing module disabled", exportedNamespaces: []string{"vendor"}, replacing: "enabled: false,"},
		{name: "replacing namespace not exported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := GroupFixturePreparers(
				prepareForTestWithNamespace,
				prepareForTestWithReplaces,
				PrepareForTestWithAndroidMk,
				FixtureModifyProductVariables(func(variables FixtureProductVariables) {
					variables.NamespacesToExport = tc.exportedNamespaces
				}),
				dirBpToPreparer(map[string]string{
					"rild": `test_module { name: "rild" }`,
					"vendor": `
						soong_namespace {
						}
						test_module {
							name: "qcrild",
							replaces: ["//rild:rild"],
							` + tc.replacing + `
						}
					`,
				}),
			).RunTest(t)

			mk, err := os.ReadFile(PathForOutput(PathContextForTesting(result.Config), "Android.mk").String())
			if err != nil {
				t.Fatal(err)
			}
			phony := "LOCAL_MODULE := rild\nLOCAL_REQUIRED_MODULES := qcrild\ninclude $(BUILD_PHONY_PACKAGE)\n"
			AssertBoolEquals(t, "Android.mk has a phony module rild that requires qcrild", tc.wantPhony,
				strings.Contains(string(mk), phony))
		})
	}
}
