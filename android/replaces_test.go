// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"testing"
)

func TestReplacingModuleTakesTheName(t *testing.T) {
	// dir1 is read before dir1/dir2, so the replaced definition arrives first. The Blueprint
	// tests cover both arrival orders.
	result := GroupFixturePreparers(
		prepareForTestWithNamespace,
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
					replaces: ["dir1"],
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
}

func TestReplacesInDefaultsModule(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForDefaultsTest,
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
					replaces: ["dir1"],
				}
			`,
		}),
	).RunTest(t)

	foo := result.Module("foo", "").(*defaultsTestModule)
	AssertDeepEquals(t, "foo", []string{"replacing"}, foo.properties.Foo)
	AssertDeepEquals(t, "directories replaced by foo", []string(nil), foo.ReplacedDirs())
}

func TestReplacesAcrossNamespaces(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForTestWithNamespace,
		dirBpToPreparer(map[string]string{
			"dir1": `
				soong_namespace {
				}
				test_module {
					name: "a",
					id: "first",
				}
			`,
			"dir2": `
				soong_namespace {
				}
				test_module {
					name: "a",
					id: "second",
					replaces: ["dir1"],
				}
			`,
		}),
	).RunTest(t)

	for _, id := range []string{"first", "second"} {
		if findModuleById(result, id).module == nil {
			t.Errorf("the definition of a with id %q is missing from the build", id)
		}
	}
}

func TestReplacesLeavesPreferUnchanged(t *testing.T) {
	for _, tc := range []struct {
		prefer       string
		wantPrebuilt bool
	}{{prefer: "true", wantPrebuilt: true}, {prefer: "false", wantPrebuilt: false}} {
		t.Run("prefer "+tc.prefer, func(t *testing.T) {
			result := GroupFixturePreparers(
				PrepareForTestWithArchMutator,
				PrepareForTestWithPrebuilts,
				PrepareForTestWithOverrides,
				FixtureRegisterWithContext(registerTestPrebuiltModules),
				MockFS{"dir1/source_file": nil, "dir1/dir2/prebuilt_file": nil}.AddToFixture(),
				dirBpToPreparer(map[string]string{
					".": `
						source {
							name: "foo",
							deps: [":bar"],
						}
					`,
					"dir1": `
						source {
							name: "bar",
						}
					`,
					"dir1/dir2": `
						prebuilt {
							name: "bar",
							prefer: ` + tc.prefer + `,
							srcs: ["prebuilt_file"],
							replaces: ["dir1"],
						}
					`,
				}),
			).RunTest(t)

			var dependsOnPrebuilt bool
			result.VisitDirectDeps(result.ModuleForTests(t, "foo", "android_common").Module(), func(m Module) {
				if _, ok := m.(*prebuiltModule); ok {
					dependsOnPrebuilt = true
				}
			})
			AssertBoolEquals(t, "foo depends on the prebuilt", tc.wantPrebuilt, dependsOnPrebuilt)
		})
	}
}
