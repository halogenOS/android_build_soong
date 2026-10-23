// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"testing"
)

type keysDirTestModule struct {
	ModuleBase
	properties struct {
		// A key file read through SigningKeyFile, or under Keys_dir_name when it is set.
		Key           string
		Keys_dir_name *string
		// A directory added with FlagWithKeyDirectory, and the key files in it read by the step.
		Key_dir  string
		Dir_keys []string
	}

	signingKey SigningKey
}

func keysDirTestModuleFactory() Module {
	module := &keysDirTestModule{}
	module.AddProperties(&module.properties)
	InitAndroidModule(module)
	return module
}

func (m *keysDirTestModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	key := PathForSource(ctx, m.properties.Key)
	if m.properties.Keys_dir_name != nil {
		m.signingKey = keysDirKey(ctx, *m.properties.Keys_dir_name, key)
	} else {
		m.signingKey = SigningKeyFile(ctx, key)
	}

	rule := NewRuleBuilder(pctx, ctx)
	cmd := rule.Command().Text("sign")
	FlagWithKeysDirKey(cmd, "--cert=", ReleaseCertificate(ctx))
	FlagWithKeyDirectory(ctx, cmd, "--dir=", PathForSource(ctx, m.properties.Key_dir),
		PathsForSource(ctx, m.properties.Dir_keys))
	cmd.Output(PathForModuleOut(ctx, "signed"))
	rule.Build("sign", "sign")
}

var prepareForKeysDirTest = GroupFixturePreparers(
	FixtureRegisterWithContext(func(ctx RegistrationContext) {
		ctx.RegisterModuleType("keys_dir_test", keysDirTestModuleFactory)
	}),
	PrepareForTestWithKeysDir,
	FixtureMergeMockFs(MockFS{
		"build/make/target/product/security/testkey.x509.pem":  nil,
		"build/make/target/product/security/testkey.pk8":       nil,
		"build/make/target/product/security/platform.x509.pem": nil,
		"build/make/target/product/security/platform.pk8":      nil,
		"build/make/target/product/security/media.x509.pem":    nil,
		"cert/other.pk8":         nil,
		"vendor/keys/a.x509.pem": nil,
	}),
)

func TestKeysDir(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForKeysDirTest,
		PrepareForTestAccessingMakeVars,
		FixtureWithRootAndroidBp(`
			keys_dir_test {
				name: "default_key",
				key: "build/make/target/product/security/testkey.pk8",
				key_dir: "build/make/target/product/security",
				dir_keys: ["build/make/target/product/security/testkey.x509.pem"],
			}
			keys_dir_test {
				name: "platform_key",
				key: "build/make/target/product/security/platform.x509.pem",
				key_dir: "vendor/keys",
				dir_keys: ["vendor/keys/a.x509.pem"],
			}
			keys_dir_test {
				name: "other_key",
				key: "cert/other.pk8",
				key_dir: "vendor/keys",
			}
		`),
	).RunTest(t)

	for name, want := range map[string]struct {
		ref    string
		inputs []string
	}{
		"default_key": {"${keys_dir}/releasekey.pk8",
			[]string{"out/soong/test-keys/releasekey.pk8", "out/soong/keys_dir.txt",
				"out/soong/keys_dir_checks/releasekey.pk8"}},
		"platform_key": {"${keys_dir}/platform.x509.pem",
			[]string{"out/soong/test-keys/platform.x509.pem", "out/soong/keys_dir.txt",
				"out/soong/keys_dir_checks/platform.x509.pem"}},
		"other_key": {"cert/other.pk8", []string{"cert/other.pk8"}},
	} {
		key := result.ModuleForTests(t, name, "").Module().(*keysDirTestModule).signingKey
		AssertStringEquals(t, name+" reference", want.ref, key.Ref())
		AssertDeepEquals(t, name+" inputs", want.inputs, PathsRelativeToTop(key.Inputs()))
	}

	defaultKey := result.ModuleForTests(t, "default_key", "").Rule("sign")
	AssertStringDoesContain(t, "command", defaultKey.RuleParams.Command,
		"sign --cert=${keys_dir}/releasekey.x509.pem --dir=${keys_dir} ")
	AssertStringListContains(t, "implicits", PathsRelativeToTop(defaultKey.Implicits),
		"out/soong/test-keys/testkey.x509.pem")
	AssertStringListContains(t, "implicits", PathsRelativeToTop(defaultKey.Implicits), "out/soong/keys_dir.txt")
	platformKey := result.ModuleForTests(t, "platform_key", "").Rule("sign")
	AssertStringDoesContain(t, "command", platformKey.RuleParams.Command, " --dir=vendor/keys ")
	AssertStringListContains(t, "implicits", PathsRelativeToTop(platformKey.Implicits), "vendor/keys/a.x509.pem")

	keysDir := result.SingletonForTests(t, "keys_dir")
	value := keysDir.Output("out/soong/keys_dir.txt")
	for link, target := range map[string]string{
		"out/soong/test-keys/releasekey.x509.pem": "build/make/target/product/security/testkey.x509.pem",
		"out/soong/test-keys/releasekey.pk8":      "build/make/target/product/security/testkey.pk8",
		"out/soong/test-keys/platform.x509.pem":   "build/make/target/product/security/platform.x509.pem",
		"out/soong/test-keys/platform.pk8":        "build/make/target/product/security/platform.pk8",
		"out/soong/test-keys/media.x509.pem":      "build/make/target/product/security/media.x509.pem",
		"out/soong/test-keys/testkey.x509.pem":    "build/make/target/product/security/testkey.x509.pem",
	} {
		AssertStringEquals(t, link, target, keysDir.Output(link).Args["fromPath"])
		AssertStringListContains(t, "keys_dir.txt implicits", PathsRelativeToTop(value.Implicits), link)
	}
	for _, name := range []string{"platform.x509.pem", "releasekey.pk8", "releasekey.x509.pem", "testkey.x509.pem"} {
		AssertStringEquals(t, "key file checked in keys_dir", name,
			keysDir.Output("out/soong/keys_dir_checks/" + name).Args["name"])
	}
	AssertBoolEquals(t, "a key file no build step reads is not checked", true,
		keysDir.MaybeOutput("out/soong/keys_dir_checks/media.x509.pem").Rule == nil)

	keyFiles := result.MakeVarsForTesting(t, func(v MakeVarVariable) bool {
		return v.Name() == "SOONG_KEYS_DIR_KEY_FILES"
	})
	AssertStringEquals(t, "SOONG_KEYS_DIR_KEY_FILES",
		"build/make/target/product/security/media.x509.pem:media.x509.pem "+
			"build/make/target/product/security/platform.x509.pem:platform.x509.pem "+
			"build/make/target/product/security/testkey.x509.pem:releasekey.x509.pem "+
			"build/make/target/product/security/platform.pk8:platform.pk8 "+
			"build/make/target/product/security/testkey.pk8:releasekey.pk8",
		keyFiles[0].Value())
}

func TestKeysDirNameOfTwoFiles(t *testing.T) {
	GroupFixturePreparers(
		prepareForKeysDirTest,
		FixtureWithRootAndroidBp(`
			keys_dir_test {
				name: "first",
				key: "cert/other.pk8",
				keys_dir_name: "platform.pk8",
				key_dir: "vendor/keys",
			}
		`),
	).ExtendWithErrorHandler(FixtureExpectsAtLeastOneErrorMatchingPattern(
		`keys_dir has the name platform\.pk8 for two files`)).
		RunTest(t)
}
