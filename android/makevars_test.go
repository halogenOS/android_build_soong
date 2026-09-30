package android

import (
	"regexp"
	"strings"
	"testing"
)

func TestDistFilesInGenerateAndroidBuildActions(t *testing.T) {
	result := GroupFixturePreparers(
		FixtureRegisterWithContext(func(ctx RegistrationContext) {
			ctx.RegisterModuleType("my_module_type", newDistFileModule)
			ctx.RegisterParallelSingletonType("my_singleton", newDistFileSingleton)
		}),
		FixtureModifyConfig(SetKatiEnabledForTests),
		PrepareForTestWithMakevars,
	).RunTestWithBp(t, `
	my_module_type {
		name: "foo",
	}
	`)

	lateContents := string(result.SingletonForTests(t, "makevars").Singleton().(*makeVarsSingleton).lateForTesting)
	matched, err := regexp.MatchString(`call dist-for-goals,my_goal,.*/my_file.txt:my_file.txt\)`, lateContents)
	if err != nil || !matched {
		t.Fatalf("Expected a dist of my_file.txt, but got: %s", lateContents)
	}
	matched, err = regexp.MatchString(`call dist-for-goals,my_singleton_goal,.*/my_singleton_file.txt:my_singleton_file.txt\)`, lateContents)
	if err != nil || !matched {
		t.Fatalf("Expected a dist of my_singleton_file.txt, but got: %s", lateContents)
	}
}

type distFileModule struct {
	ModuleBase
}

func newDistFileModule() Module {
	m := &distFileModule{}
	InitAndroidModule(m)
	return m
}

func (m *distFileModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	out := PathForModuleOut(ctx, "my_file.txt")
	WriteFileRule(ctx, out, "Hello, world!")
	ctx.DistForGoal("my_goal", out)
}

type distFileSingleton struct {
}

func newDistFileSingleton() Singleton {
	return &distFileSingleton{}
}

func (d *distFileSingleton) GenerateBuildActions(ctx SingletonContext) {
	out := PathForOutput(ctx, "my_singleton_file.txt")
	WriteFileRule(ctx, out, "Hello, world!")
	ctx.DistForGoal("my_singleton_goal", out)
}

func TestInstallRulesWithExtraFilesZip(t *testing.T) {
	result := GroupFixturePreparers(
		FixtureRegisterWithContext(func(ctx RegistrationContext) {
			ctx.RegisterModuleType("extra_zip_install", newExtraZipInstallModule)
		}),
		FixtureModifyConfig(SetKatiEnabledForTests),
		PrepareForTestWithMakevars,
	).RunTestWithBp(t, `
	extra_zip_install {
		name: "keeping",
	}
	extra_zip_install {
		name: "replacing",
		replace_dir: true,
	}
	`)

	installs := string(result.SingletonForTests(t, "makevars").Singleton().(*makeVarsSingleton).installsForTesting)
	ruleOf := func(name string) string {
		t.Helper()
		target := regexp.MustCompile(`(?m)^\S*/` + name + `/primary\.txt: `)
		loc := target.FindStringIndex(installs)
		if loc == nil {
			t.Fatalf("no install rule for %s in:\n%s", name, installs)
		}
		rule := installs[loc[0]:]
		if end := strings.Index(rule, "\n\n"); end >= 0 {
			rule = rule[:end]
		}
		return rule
	}
	inOrder := func(rule string, parts ...string) {
		t.Helper()
		last := -1
		for _, part := range parts {
			i := strings.Index(rule, part)
			if i < 0 {
				t.Fatalf("%q not found in rule:\n%s", part, rule)
			}
			if i < last {
				t.Fatalf("%q comes before the text expected before it in rule:\n%s", part, rule)
			}
			last = i
		}
	}

	keeping := ruleOf("keeping")
	inOrder(keeping, "rm -f $@ && cp -f -d $< $@", "unzip -qDD -d ", "EXTRA_INSTALL_ZIPS += ")
	if strings.Contains(keeping, "rm -rf") || strings.Contains(keeping, "$@.tmp") {
		t.Errorf("install without the mark replaces its directory:\n%s", keeping)
	}

	replacing := ruleOf("replacing")
	inOrder(replacing, "rm -rf '", "' && mkdir -p '", "unzip -qDD -d ", "cp -f -d $< $@.tmp && mv -f $@.tmp $@",
		"EXTRA_INSTALL_ZIPS += ")
	if strings.Contains(replacing, "rm -f $@ && cp") {
		t.Errorf("install with the mark copies the primary file before the zip:\n%s", replacing)
	}
}

type extraZipInstallModule struct {
	ModuleBase
	properties struct {
		Replace_dir *bool
	}
}

func newExtraZipInstallModule() Module {
	m := &extraZipInstallModule{}
	m.AddProperties(&m.properties)
	InitAndroidArchModule(m, DeviceSupported, MultilibCommon)
	return m
}

func (m *extraZipInstallModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	primary := PathForModuleOut(ctx, "primary.txt")
	WriteFileRule(ctx, primary, "primary")
	extraZip := PathForModuleOut(ctx, "extra.zip")
	WriteFileRule(ctx, extraZip, "zip")
	installDir := PathForModuleInstall(ctx, ctx.ModuleName())
	if m.properties.Replace_dir != nil && *m.properties.Replace_dir {
		ctx.InstallFileWithExtraFilesZipReplacingDir(installDir, "primary.txt", primary, extraZip)
	} else {
		ctx.InstallFileWithExtraFilesZip(installDir, "primary.txt", primary, extraZip)
	}
}
