// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"path/filepath"
	"strings"

	"github.com/google/blueprint"

	"android/soong/shared"
)

//go:generate go run ../../blueprint/gobtools/codegen

func init() {
	registerKeysDirComponents(InitRegistrationContext)
}

func registerKeysDirComponents(ctx RegistrationContext) {
	ctx.RegisterParallelSingletonType("keys_dir", keysDirSingletonFactory)
}

// PrepareForTestWithKeysDir registers the singleton that writes the links in out/soong/test-keys.
var PrepareForTestWithKeysDir = FixtureRegisterWithContext(registerKeysDirComponents)

// KeysDir is the directory with the keys read by every build step that needs a key, under the
// file names of a release key directory. soong_ui sets it in combined-<product>.ninja: to the links
// to the test keys written by the regular build, or to the release key directory of a signing
// build.
var KeysDir = blueprint.NewExternalVariable(shared.KeysDirVariable)

const (
	// The name in keys_dir of the default certificate, without the suffixes .x509.pem and .pk8.
	releaseKeyName = "releasekey"
	// The end of the name in keys_dir of each key of an APEX, after the name of the APEX and before
	// the suffix of the key file.
	apexKeyNameSuffix = ".certificate.override"
	// The name in keys_dir of the AVB key of each RSA key size, with % in the place of the size.
	// Make writes the AVB keys, and reads the name through a make variable.
	avbKeyNamePattern = "avbkey_%.pem"
)

// A KeysDirKey is a key file in keys_dir. A build step reads it through Ref, and lists Inputs as its
// inputs.
type KeysDirKey struct {
	name   string
	inputs Paths
}

// Ref returns the reference to the key file for a command or an argument of a rule defined with
// PackageContext. A RuleBuilder command adds the key file with FlagWithKeysDirKey, since RuleBuilder
// escapes the text of a command.
func (k KeysDirKey) Ref() string {
	return KeysDir.Ref() + "/" + k.name
}

// Inputs returns the link written under the name of the key file by the regular build, the file
// with the value of keys_dir, and the output of the step that checks that keys_dir contains the key
// file. The check runs only when a step that reads the key file runs.
func (k KeysDirKey) Inputs() Paths {
	return k.inputs
}

// A SigningKey is a key file read by a build step: a key file in keys_dir, or a key file outside
// the default certificate directory, which the step reads where it is.
type SigningKey interface {
	// Ref returns the text that refers to the key file in a command or an argument of a rule
	// defined with PackageContext.
	Ref() string
	// Inputs returns the files that the step lists as inputs.
	Inputs() Paths
}

var _ SigningKey = KeysDirKey{}
var _ SigningKey = sourceKey{}

// sourceKey is a key file that a build step reads where it is.
type sourceKey struct {
	path Path
}

func (k sourceKey) Ref() string {
	return k.path.String()
}

func (k sourceKey) Inputs() Paths {
	return Paths{k.path}
}

// SigningKeyFile returns the key file that a build step reads in place of src: the key file in
// keys_dir for a key of the default certificate directory, or src itself for a key file of any
// other directory, such as the key of an android_app_certificate module.
func SigningKeyFile(ctx ModuleContext, src Path) SigningKey {
	if name, ok := defaultCertificateKeyName(ctx, src); ok {
		return keysDirKey(ctx, name, src)
	}
	return sourceKey{src}
}

// ReleaseCertificate returns the certificate of the default certificate in keys_dir.
func ReleaseCertificate(ctx ModuleContext) KeysDirKey {
	pem, _ := releaseKeyLinks(ctx)
	return keysDirKey(ctx, pem.Name, pem.Src)
}

// ApexPayloadKey returns the key in keys_dir that signs the payload of the APEX called apexName,
// linked to src by the regular build.
func ApexPayloadKey(ctx ModuleContext, apexName string, src Path) KeysDirKey {
	return keysDirKey(ctx, apexName+apexKeyNameSuffix+".pem", src)
}

// ApexContainerKeys returns the certificate and the private key in keys_dir that sign the container
// of the APEX called apexName, linked to pem and key by the regular build.
func ApexContainerKeys(ctx ModuleContext, apexName string, pem, key Path) (certificate, privateKey KeysDirKey) {
	return keysDirKey(ctx, apexName+apexKeyNameSuffix+".x509.pem", pem),
		keysDirKey(ctx, apexName+apexKeyNameSuffix+".pk8", key)
}

// FlagWithKeysDirKey adds flag and key, joined directly, to the command line of cmd, and the inputs
// of key to its inputs.
func FlagWithKeysDirKey(cmd *RuleBuilderCommand, flag string, key KeysDirKey) *RuleBuilderCommand {
	return cmd.FlagWithExternalVariable(flag, KeysDir, "/"+key.name).Implicits(key.inputs)
}

// FlagWithKeyDirectory adds flag and dir, joined directly, to the command line of cmd, and keys, the
// key files in dir read by the step, to its inputs. For the default certificate directory, it adds
// keys_dir in the place of dir, and the step reads keys from keys_dir under their file names.
func FlagWithKeyDirectory(ctx ModuleContext, cmd *RuleBuilderCommand, flag string, dir Path, keys Paths) *RuleBuilderCommand {
	if !isDefaultCertificateDir(ctx, dir.String()) {
		return cmd.FlagWithArg(flag, dir.String()).Implicits(keys)
	}
	cmd.FlagWithExternalVariable(flag, KeysDir, "")
	for _, key := range keys {
		cmd.Implicits(keysDirKey(ctx, key.Base(), key).inputs)
	}
	return cmd
}

// keysDirLink is a key file of the tree linked under its name in keys_dir by the regular build.
// @auto-generate: gob
type keysDirLink struct {
	Name string
	Src  Path
}

// keysDirLinksInfo lists the key files in keys_dir read by the build steps of a module.
// @auto-generate: gob
type keysDirLinksInfo []keysDirLink

var keysDirLinksProvider = blueprint.NewProvider[keysDirLinksInfo]()

// keysDirLinks collects the key files in keys_dir read by the build steps of a module.
type keysDirLinks struct {
	links keysDirLinksInfo
}

func (l *keysDirLinks) finish(ctx ModuleContext) {
	SetProvider(ctx, keysDirLinksProvider, l.links)
}

var keysDirLinksKey = &buildActionsStateKey[*keysDirLinks]{
	newState: func() *keysDirLinks { return &keysDirLinks{} },
}

// keysDirKey returns the key file called name in keys_dir, and records that a build step of the
// module reads it, linked to src by the regular build.
func keysDirKey(ctx ModuleContext, name string, src Path) KeysDirKey {
	links := moduleState(ctx, keysDirLinksKey)
	links.links = append(links.links, keysDirLink{Name: name, Src: src})
	return KeysDirKey{name: name, inputs: Paths{testKeysDirPath(ctx).Join(ctx, name), keysDirValuePath(ctx),
		keysDirCheckPath(ctx, name)}}
}

// isDefaultCertificateDir reports whether dir is the default certificate directory, whose keys
// the build steps read from keys_dir.
func isDefaultCertificateDir(ctx PathContext, dir string) bool {
	return dir == ctx.Config().DefaultAppCertificateDir(ctx).String()
}

// releaseKeyLinks returns the certificate and the private key of the default certificate under
// their names in keys_dir.
func releaseKeyLinks(ctx PathContext) (pem, key keysDirLink) {
	pemSrc, keySrc := ctx.Config().DefaultAppCertificate(ctx)
	return keysDirLink{Name: releaseKeyName + ".x509.pem", Src: pemSrc},
		keysDirLink{Name: releaseKeyName + ".pk8", Src: keySrc}
}

// defaultCertificateKeyName returns the name in keys_dir of src, a key file of the default
// certificate directory: the release key name for the default certificate, and the file name for
// every other key file. It returns false for a key file of any other directory.
func defaultCertificateKeyName(ctx PathContext, src Path) (string, bool) {
	if !isDefaultCertificateDir(ctx, filepath.Dir(src.String())) {
		return "", false
	}
	pem, key := releaseKeyLinks(ctx)
	for _, releaseKey := range []keysDirLink{pem, key} {
		if releaseKey.Src.String() == src.String() {
			return releaseKey.Name, true
		}
	}
	return src.Base(), true
}

// defaultCertificateKeyLinks returns every certificate and private key of the default certificate
// directory under its name in keys_dir.
func defaultCertificateKeyLinks(ctx PathGlobContext) []keysDirLink {
	dir := ctx.Config().DefaultAppCertificateDir(ctx).String()
	var links []keysDirLink
	for _, pattern := range []string{"*.x509.pem", "*.pk8"} {
		files, err := ctx.GlobWithDeps(filepath.Join(dir, pattern), nil)
		if err != nil {
			ReportPathErrorf(ctx, "could not list the keys in %s: %s", dir, err)
			return nil
		}
		for _, file := range files {
			src := PathForSource(ctx, file)
			name, _ := defaultCertificateKeyName(ctx, src)
			links = append(links, keysDirLink{Name: name, Src: src})
		}
	}
	return links
}

// testKeysDirPath returns the directory of the links written by the regular build, the value of
// keys_dir without signing keys.
func testKeysDirPath(ctx PathContext) OutputPath {
	return PathForOutput(ctx, shared.TestKeysDir)
}

// keysDirValuePath returns the file that contains the value of keys_dir, for the steps written by
// Make, whose commands cannot reference a Ninja variable.
func keysDirValuePath(ctx PathContext) OutputPath {
	return PathForOutput(ctx, "keys_dir.txt")
}

// keysDirCheckPath returns the output of the step that checks that keys_dir contains the key file
// called name.
func keysDirCheckPath(ctx PathContext, name string) OutputPath {
	return PathForOutput(ctx, "keys_dir_checks", name)
}

// keysDirIsTestKeysPath returns the file that contains true when keys_dir is the directory of the
// links written by the regular build, and false otherwise.
func keysDirIsTestKeysPath(ctx PathContext) OutputPath {
	return PathForOutput(ctx, "keys_dir_is_test_keys.txt")
}

// checkKeysDirKey fails when keys_dir does not contain the key file $name. Its command names
// keys_dir, so it runs again when keys_dir changes, and it leaves an existing $out untouched.
var checkKeysDirKey = pctx.AndroidStaticRule("checkKeysDirKey",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			`if `, Test, ` -e `, KeysDir.Ref(), `/$name; then `, Test, ` -e $out || `, Touch, ` $out; else `,
			Echo, ` "`, KeysDir.Ref(), ` does not contain the key file $name" >&2 && exit 1; fi`),
		Restat:      true,
		Description: "keys_dir check $name",
	}, "name")

// writeKeysDirValue writes keys_dir into $out, and into $isTestKeys whether keys_dir is
// $testKeysDir. It does not rewrite a file whose content is unchanged, so a step that reads it runs
// again only when keys_dir changes.
var writeKeysDirValue = pctx.AndroidStaticRule("writeKeysDirValue",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			Echo, ` `, KeysDir.Ref(), ` > $out.tmp && `,
			`if `, Test, ` `, KeysDir.Ref(), ` -ef $testKeysDir; then `, Echo, ` true; else `, Echo, ` false; fi`,
			` > $isTestKeys.tmp && `,
			`for file in $out $isTestKeys; do `,
			Cmp, ` -s $$file.tmp $$file && `, Rm, ` $$file.tmp || `, Mv, ` $$file.tmp $$file; done`),
		Restat:      true,
		Description: "keys_dir $out",
	}, "testKeysDir", "isTestKeys")

type keysDirSingleton struct {
	// The key files of the default certificate directory, as <file>:<name in keys_dir>
	defaultCertificateKeys []string
}

func keysDirSingletonFactory() Singleton {
	return &keysDirSingleton{}
}

// GenerateBuildActions writes the links from the names of a release key directory to the key files
// of the tree, and the file with the value of keys_dir, which depends on every link. The links
// cover every key file read by a build step, and every key file of the default certificate
// directory for the steps written by Make.
func (s *keysDirSingleton) GenerateBuildActions(ctx SingletonContext) {
	releaseKeyPem, releaseKeyKey := releaseKeyLinks(ctx)
	read := []keysDirLink{releaseKeyPem, releaseKeyKey}
	ctx.VisitAllModuleProxies(func(m ModuleProxy) {
		moduleLinks, _ := OtherModuleProvider(ctx, m, keysDirLinksProvider)
		read = append(read, moduleLinks...)
	})
	defaultCertificateKeys := defaultCertificateKeyLinks(ctx)

	srcs := make(map[string]Path)
	for _, link := range append(defaultCertificateKeys, read...) {
		if src, found := srcs[link.Name]; !found {
			srcs[link.Name] = link.Src
		} else if src.String() != link.Src.String() {
			ctx.Errorf("keys_dir has the name %s for two files: %s and %s", link.Name, src, link.Src)
		}
	}

	var links Paths
	for _, name := range SortedKeys(srcs) {
		link := testKeysDirPath(ctx).Join(ctx, name)
		ctx.Build(pctx, BuildParams{
			Rule:   Symlink,
			Output: link,
			Input:  srcs[name],
			Args: map[string]string{
				"fromPath": absolutePath(srcs[name].String()),
			},
		})
		links = append(links, link)
	}

	readNames := make(map[string]bool)
	for _, link := range read {
		readNames[link.Name] = true
	}
	for _, name := range SortedKeys(readNames) {
		ctx.Build(pctx, BuildParams{
			Rule:   checkKeysDirKey,
			Output: keysDirCheckPath(ctx, name),
			Args:   map[string]string{"name": name},
		})
	}
	ctx.Build(pctx, BuildParams{
		Rule:           writeKeysDirValue,
		Output:         keysDirValuePath(ctx),
		ImplicitOutput: keysDirIsTestKeysPath(ctx),
		Implicits:      links,
		Args: map[string]string{
			"testKeysDir": testKeysDirPath(ctx).String(),
			"isTestKeys":  keysDirIsTestKeysPath(ctx).String(),
		},
	})

	s.defaultCertificateKeys = nil
	for _, link := range defaultCertificateKeys {
		s.defaultCertificateKeys = append(s.defaultCertificateKeys, link.Src.String()+":"+link.Name)
	}
}

func (s *keysDirSingleton) MakeVars(ctx MakeVarsContext) {
	ctx.Strict("SOONG_KEYS_DIR_VALUE_FILE", keysDirValuePath(ctx).String())
	ctx.Strict("SOONG_KEYS_DIR_IS_TEST_KEYS_FILE", keysDirIsTestKeysPath(ctx).String())
	ctx.Strict("SOONG_TEST_KEYS_DIR", testKeysDirPath(ctx).String())
	ctx.Strict("SOONG_KEYS_DIR_RELEASE_KEY", releaseKeyName)
	ctx.Strict("SOONG_KEYS_DIR_AVB_KEY", avbKeyNamePattern)
	ctx.Strict("SOONG_KEYS_DIR_KEY_FILES", strings.Join(s.defaultCertificateKeys, " "))
}
