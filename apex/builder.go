// Copyright (C) 2019 The Android Open Source Project
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
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"android/soong/aconfig"
	"android/soong/android"
	"android/soong/dexpreopt"
	"android/soong/java"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

var (
	pctx = android.NewPackageContext("android/apex")
)

func init() {
	pctx.Import("android/soong/aconfig")
	pctx.Import("android/soong/android")
	pctx.Import("android/soong/cc/config")
	pctx.Import("android/soong/java")
	pctx.HostBinToolVariable("apexer", "apexer")
	pctx.HostBinToolVariable("apexer_with_DCLA_preprocessing", "apexer_with_DCLA_preprocessing")

	// ART minimal builds (using the master-art manifest) do not have the "frameworks/base"
	// projects, and hence cannot build 'aapt2'. Use the SDK prebuilt instead.
	hostBinToolVariableWithPrebuilt := func(name, prebuiltDir, tool string) {
		pctx.VariableFunc(name, func(ctx android.PackageVarContext) string {
			if !ctx.Config().FrameworksBaseDirExists(ctx) {
				return filepath.Join(prebuiltDir, runtime.GOOS, "bin", tool)
			} else {
				return ctx.Config().HostToolPath(ctx, tool).String()
			}
		})
	}
	hostBinToolVariableWithPrebuilt("aapt2", "prebuilts/sdk/tools", "aapt2")
	pctx.SourcePathVariable("openssl", "prebuilts/build-tools/${android.HostPrebuiltTag}/bin/openssl")
}

type createStorageStruct struct {
	Output_file string
	Desc        string
	File_type   string
}

var createStorageInfo = []createStorageStruct{
	{"package.map", "create_aconfig_package_map_file", "package_map"},
	{"flag.map", "create_aconfig_flag_map_file", "flag_map"},
	{"flag.val", "create_aconfig_flag_val_file", "flag_val"},
	{"flag.info", "create_aconfig_flag_info_file", "flag_info"},
}

var (
	apexer                         = pctx.HostTool("apexer")
	apexer_with_DCLA_preprocessing = pctx.HostTool("apexer_with_DCLA_preprocessing")
	avbtool                        = pctx.HostTool("avbtool")
	e2fsdroid                      = pctx.HostTool("e2fsdroid")
	mke2fs                         = pctx.HostTool("mke2fs")
	resize2fs                      = pctx.HostTool("resize2fs")
	sefcontext_compile             = pctx.HostTool("sefcontext_compile")
	make_f2fs                      = pctx.HostTool("make_f2fs")
	sload_f2fs                     = pctx.HostTool("sload_f2fs")
	make_erofs                     = pctx.HostTool("mkfs.erofs")
	zipalign                       = pctx.HostTool("zipalign")
	apex_elf_checker               = pctx.HostTool("apex_elf_checker")
	host_apex_verifier             = pctx.HostTool("host_apex_verifier")
	deapexer                       = pctx.HostTool("deapexer")
	debugfs                        = pctx.HostTool("debugfs")
	fsck_erofs                     = pctx.HostTool("fsck.erofs")
	aconfigTool                    = aconfig.Aconfig
	apex_ls                        = pctx.HostTool("apex-ls")
	apex_sepolicy_tests            = pctx.HostTool("apex_sepolicy_tests")
	zip2zip                        = pctx.HostTool("zip2zip")
	jsonmodify                     = pctx.HostTool("jsonmodify")
	conv_apex_manifest             = pctx.HostTool("conv_apex_manifest")
	conv_linker_config             = pctx.HostTool("conv_linker_config")
	extract_apks                   = pctx.HostTool("extract_apks")
	gen_apex_symbols               = pctx.HostTool("gen_apex_symbols")

	apexManifestRule = pctx.StaticRule("apexManifestRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Rm, ` -f $out && `, jsonmodify, ` $in `,
			`-a provideNativeLibs ${provideNativeLibs} `,
			`-a requireNativeLibs ${requireNativeLibs} `,
			`-se version 0 ${default_version} `,
			`${opt} -o $out`,
		),
		Description: "prepare ${out}",
	}, "provideNativeLibs", "requireNativeLibs", "default_version", "opt")

	stripApexManifestRule = pctx.StaticRule("stripApexManifestRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Rm, ` -f $out && `, conv_apex_manifest, ` strip $in -o $out`,
		),
		Description: "strip ${in}=>${out}",
	})

	pbApexManifestRule = pctx.StaticRule("pbApexManifestRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Rm, ` -f $out && `, conv_apex_manifest, ` proto $in -o $out`,
		),
		Description: "convert ${in}=>${out}",
	})

	// TODO(b/113233103): make sure that file_contexts is as expected, i.e., validate
	// against the binary policy using sefcontext_compiler -p <policy>.

	// TODO(b/114327326): automate the generation of file_contexts
	apexRule             = pctx.StaticRule("apexRule", apexRuleParams(), apexRuleArgs...)
	apexWithSignToolRule = pctx.StaticRule("apexWithSignToolRule", apexRuleParams(signToolCommand...),
		slices.Concat(apexRuleArgs, []string{"sign_tool"})...)

	DCLAApexRule             = pctx.StaticRule("DCLAApexRule", dclaApexRuleParams(), dclaApexRuleArgs...)
	DCLAApexWithSignToolRule = pctx.StaticRule("DCLAApexWithSignToolRule", dclaApexRuleParams(signToolCommand...),
		slices.Concat(dclaApexRuleArgs, []string{"sign_tool"})...)

	// apexer fails when the public key given with --pubkey does not belong to the payload key.
	extractPayloadPublicKeyRule = pctx.StaticRule("extractPayloadPublicKeyRule", blueprint.RuleParams{
		Command2:        blueprint.NewCommand(avbtool, ` extract_public_key --key ${key} --output ${out}`),
		Description:     "extract public key ${key}",
		SandboxDisabled: true,
	}, "key")

	apexProtoConvertRule = pctx.AndroidStaticRule("apexProtoConvertRule",
		blueprint.RuleParams{
			Command:     `${aapt2} convert --output-format proto $in -o $out`,
			CommandDeps: []string{"${aapt2}"},
		})

	apexBundleRule = pctx.StaticRule("apexBundleRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			zip2zip, ` -i $in -o $out.base `,
			`apex_payload.img:apex/${abi}.img `,
			`apex_build_info.pb:apex/${abi}.build_info.pb `,
			`apex_manifest.json:root/apex_manifest.json `,
			`apex_manifest.pb:root/apex_manifest.pb `,
			`AndroidManifest.xml:manifest/AndroidManifest.xml `,
			`assets/NOTICE.html.gz:assets/NOTICE.html.gz &&`,
			android.SoongZip, ` -o $out.config -C $$(dirname ${config}) -f ${config} && `,
			android.MergeZips, ` $out $out.base $out.config`,
		),
		Description: "app bundle",
	}, "abi", "config")

	diffApexContentRule = pctx.StaticRule("diffApexContentRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			`diff --unchanged-group-format='' \`,
			`--changed-group-format='%<' \`,
			`${image_content_file} ${allowed_files_file} || (`,
			android.Echo, ` "New unexpected files were added to ${apex_module_name}." `,
			` "To fix the build run following command:" && `,
			android.Echo, ` "system/apex/tools/update_allowed_list.sh ${allowed_files_file} ${image_content_file}" && `,
			`exit 1); `, android.Touch, ` ${out}`,
		),
		Description: "Diff ${image_content_file} and ${allowed_files_file}",
	}, "image_content_file", "allowed_files_file", "apex_module_name")

	generateAPIsUsedbyApexRule = pctx.StaticRule("generateAPIsUsedbyApexRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Rm, ` -rf ${out}.image && `, android.Mkdir, ` -p ${out}.image && `,
			android.ZipSync, ` -d ${out}.image ${image_zip} && `,
			gen_apex_symbols, ` ndk_usedby ${out}.image ${config.ClangBin}/llvm-readelf `, android.ZipSync, ` ${out} && `,
			android.Rm, ` -rf ${out}.image`,
		),
		CommandDeps: []string{
			"${config.ClangBin}/llvm-readelf",
			"${config.ClangBin}/llvm-readobj",
		},
		Description: "Generate symbol list used by Apex",
	}, "image_zip")

	apexSepolicyTestsRule = pctx.StaticRule("apexSepolicyTestsRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			apex_ls, ` -Z ${in} > ${out}.fc`,
			` &&`, apex_sepolicy_tests, ` -f ${out}.fc --partition ${partition_tag}`,
			` &&`, android.Touch, ` ${out}`,
		),
		Description: "run apex_sepolicy_tests",
	}, "partition_tag")

	apexLinkerconfigValidationRule = pctx.StaticRule("apexLinkerconfigValidationRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Rm, ` -rf ${out}.image && `, android.Mkdir, ` -p ${out}.image && `,
			android.ZipSync, ` -d ${out}.image ${image_zip} && `,
			conv_linker_config, ` validate --type apex ${out}.image && `,
			android.Touch, ` ${out} && `,
			android.Rm, ` -rf ${out}.image`,
		),
		Description: "run apex_linkerconfig_validation",
	}, "image_zip")

	apexHostVerifierRule = pctx.StaticRule("apexHostVerifierRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			host_apex_verifier, ` --deapexer=`, deapexer, ` --debugfs=`, debugfs, ` `,
			`--fsckerofs=`, fsck_erofs, ` --apex=${in} --partition_tag=${partition_tag} && `,
			android.Touch, ` ${out}`),
		Description: "run host_apex_verifier",
	}, "partition_tag")

	apexElfCheckerUnwantedRule = pctx.StaticRule("apexElfCheckerUnwantedRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			apex_elf_checker, ` --tool_path ${tool_path} --unwanted ${unwanted} ${in} && `,
			android.Touch, ` ${out}`),
		CommandDeps: []string{
			"${config.ClangBin}/llvm-readelf",
			"${config.ClangBin}/llvm-readobj",
		},
		CommandDepsTools: []blueprint.HostTool{deapexer, debugfs, fsck_erofs},
		Description:      "run apex_elf_checker --unwanted",
	}, "tool_path", "unwanted")

	apexAconfigFlagsPbRule = pctx.StaticRule("apexAconfigFlagsPbRule", blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			aconfigTool, ` dump-cache --dedup --format protobuf --out ${out} `,
			`--filter container:${container}+namespace:!${beta_namespace} ${cache_files}`),
		Description: "create aconfig_flags.pb file",
	}, "container", "beta_namespace", "cache_files")
)

var (
	apexRuleArgs = []string{"tool_path", "image_dir", "image_zip", "copy_commands", "file_contexts",
		"canned_fs_config", "key", "opt_flags", "manifest"}
	dclaApexRuleArgs = slices.Concat(apexRuleArgs, []string{"is_DCLA"})

	// signToolCommand runs ${sign_tool}, the custom_sign_tool used by an APEX, on the files in
	// ${image_dir} with the payload key before apexer packs them, as the stock signer runs the tool
	// on the payload of an APEX that it signs again. The tool finds the helper tools it needs in
	// ${tool_path}.
	signToolCommand = []any{`PATH=${tool_path}:$$PATH ${sign_tool} ${key} ${image_dir} && `}
)

// apexRuleParams returns the parameters of a rule that copies the files of an APEX into
// ${image_dir}, runs payloadCommand on them and packs them with apexer.
func apexRuleParams(payloadCommand ...any) blueprint.RuleParams {
	return blueprint.RuleParams{
		Command2: blueprint.NewCommand(slices.Concat(
			[]any{android.Rm, ` -rf ${image_dir} && `, android.Mkdir, ` -p ${image_dir} && `,
				`(. ${out}.copy_commands) && `},
			payloadCommand,
			[]any{`APEXER_TOOL_PATH=${tool_path} `,
				apexer, ` --force --manifest ${manifest} `,
				`--file_contexts ${file_contexts} `,
				`--canned_fs_config ${canned_fs_config} `,
				`--include_build_info `,
				`--payload_type image `,
				`--key ${key} ${opt_flags} ${image_dir} ${out} && `,
				android.SoongZip, ` -d -C ${image_dir} -D ${image_dir} -o ${image_zip}`},
		)...),
		CommandDeps: []string{"${aapt2}", "prebuilts/sdk/current/public/android.jar"},
		CommandDepsTools: []blueprint.HostTool{avbtool, e2fsdroid, android.MergeZips,
			mke2fs, resize2fs, sefcontext_compile, make_f2fs, sload_f2fs, make_erofs,
			android.SoongZip, zipalign, android.Cp, android.Ln, android.ZipSync},
		Rspfile:        "${out}.copy_commands",
		RspfileContent: "${copy_commands}",
		Description:    "APEX ${image_dir} => ${out}",
	}
}

// dclaApexRuleParams returns the parameters of a rule that copies the files of a dynamic common lib
// APEX into ${image_dir}, runs payloadCommand on them and packs them with apexer.
func dclaApexRuleParams(payloadCommand ...any) blueprint.RuleParams {
	return blueprint.RuleParams{
		Command2: blueprint.NewCommand(slices.Concat(
			[]any{android.Rm, ` -rf ${image_dir} && `, android.Mkdir, ` -p ${image_dir} && `,
				`(. ${out}.copy_commands) && `},
			payloadCommand,
			[]any{`APEXER_TOOL_PATH=${tool_path} `,
				apexer_with_DCLA_preprocessing, ` --apexer `, apexer,
				` --canned_fs_config ${canned_fs_config} `,
				`${image_dir} `,
				`${out} `,
				`-- `,
				`--include_build_info `,
				`--force `,
				`--payload_type image `,
				`--key ${key} `,
				`--file_contexts ${file_contexts} `,
				`--manifest ${manifest} `,
				`${opt_flags} && `,
				android.SoongZip, ` -d -C ${image_dir} -D ${image_dir} -o ${image_zip}`},
		)...),
		CommandDeps: []string{"${aapt2}", "prebuilts/sdk/current/public/android.jar"},
		CommandDepsTools: []blueprint.HostTool{avbtool, e2fsdroid, android.MergeZips,
			mke2fs, resize2fs, sefcontext_compile, make_f2fs, sload_f2fs, make_erofs,
			android.SoongZip, zipalign, android.Cp, android.Ln, android.ZipSync},
		Rspfile:        "${out}.copy_commands",
		RspfileContent: "${copy_commands}",
		Description:    "APEX ${image_dir} => ${out}",
	}
}

func (a *apexBundle) buildAconfigFiles(ctx android.ModuleContext) []apexFile {
	var aconfigFiles android.Paths
	for _, file := range a.filesInfo {
		if file.module.IsNil() {
			continue
		}
		if dep, ok := android.OtherModuleProvider(ctx, file.module, android.CommonModuleInfoProvider); ok && dep.AconfigPropagatingDeclarations != nil {
			if len(dep.AconfigPropagatingDeclarations.AconfigFiles) > 0 && dep.AconfigPropagatingDeclarations.AconfigFiles[ctx.ModuleName()] != nil {
				aconfigFiles = append(aconfigFiles, dep.AconfigPropagatingDeclarations.AconfigFiles[ctx.ModuleName()]...)
			}
		}

		validationFlag := ctx.DeviceConfig().AconfigContainerValidation()
		if validationFlag == "error" || validationFlag == "warning" {
			android.VerifyAconfigBuildMode(ctx, ctx.ModuleName(), file.module, validationFlag == "error")
		}
	}
	aconfigFiles = android.FirstUniquePaths(aconfigFiles)

	var files []apexFile
	if len(aconfigFiles) > 0 {
		apexAconfigFile := android.PathForModuleOut(ctx, "aconfig_flags.pb")
		if ctx.Config().ReleaseRemoveBetaFlagsFromAconfigFlagsPb() {
			beta_namespace := ""
			if a.overridableProperties.Beta_namespace != nil {
				beta_namespace = *(a.overridableProperties.Beta_namespace)
			}
			ctx.Build(pctx, android.BuildParams{
				Rule:        apexAconfigFlagsPbRule,
				Inputs:      aconfigFiles,
				Output:      apexAconfigFile,
				Description: "aggregate all dependent aconfig caches",
				Args: map[string]string{
					"container":      ctx.ModuleName(),
					"beta_namespace": beta_namespace,
					"cache_files":    android.JoinPathsWithPrefix(aconfigFiles, "--cache "),
				},
			})
		} else {
			ctx.Build(pctx, android.BuildParams{
				Rule:        aconfig.AllDeclarationsRule,
				Inputs:      aconfigFiles,
				Output:      apexAconfigFile,
				Description: "combine_aconfig_declarations",
				Args: map[string]string{
					"cache_files": android.JoinPathsWithPrefix(aconfigFiles, "--cache "),
				},
			})
		}
		files = append(files, newApexFile(ctx, apexAconfigFile, "aconfig_flags", "etc", etc, android.ModuleProxy{}))

		storageFilesVersion := ctx.Config().ReleaseAconfigStorageVersion()
		for _, info := range createStorageInfo {
			outputFile := android.PathForModuleOut(ctx, info.Output_file)
			ctx.Build(pctx, android.BuildParams{
				Rule:        aconfig.CreateStorageRule,
				Inputs:      aconfigFiles,
				Output:      outputFile,
				Description: info.Desc,
				Args: map[string]string{
					"container":   ctx.ModuleName(),
					"file_type":   info.File_type,
					"cache_files": android.JoinPathsWithPrefix(aconfigFiles, "--cache "),
					"version":     storageFilesVersion,
				},
			})
			files = append(files, newApexFile(ctx, outputFile, info.File_type, "etc", etc, android.ModuleProxy{}))
		}
	}
	return files
}

// buildManifest creates buile rules to modify the input apex_manifest.json to add information
// gathered by the build system such as provided/required native libraries. Two output files having
// different formats are generated. a.manifestJsonOut is JSON format for Q devices, and
// a.manifest.PbOut is protobuf format for R+ devices.
// TODO(jiyong): make this to return paths instead of directly storing the paths to apexBundle
func (a *apexBundle) buildManifest(ctx android.ModuleContext, provideNativeLibs, requireNativeLibs []string) {
	src := android.PathForModuleSrc(ctx, proptools.StringDefault(a.properties.Manifest, "apex_manifest.json"))

	// Put dependency({provide|require}NativeLibs) in apex_manifest.json
	provideNativeLibs = android.SortedUniqueStrings(provideNativeLibs)
	requireNativeLibs = android.SortedUniqueStrings(android.RemoveListFromList(requireNativeLibs, provideNativeLibs))

	// VNDK APEX name is determined at runtime, so update "name" in apex_manifest
	optCommands := []string{}
	if a.vndkApex {
		apexName := vndkApexNamePrefix + a.vndkVersion()
		optCommands = append(optCommands, "-v name "+apexName)
	}

	// Collect jniLibs. Notice that a.filesInfo is already sorted
	var jniLibs []string
	for _, fi := range a.filesInfo {
		if fi.isJniLib && !android.InList(fi.stem(), jniLibs) {
			jniLibs = append(jniLibs, fi.stem())
		}
	}
	if len(jniLibs) > 0 {
		optCommands = append(optCommands, "-a jniLibs "+strings.Join(jniLibs, " "))
	}

	if android.InList(":vndk", requireNativeLibs) {
		if _, vndkVersion := a.getImageVariationPair(); vndkVersion != "" {
			optCommands = append(optCommands, "-v vndkVersion "+vndkVersion)
		}
	}

	manifestJsonFullOut := android.PathForModuleOut(ctx, "apex_manifest_full.json")
	defaultVersion := ctx.Config().ReleaseDefaultUpdatableModuleVersion()
	if a.properties.Variant_version != nil {
		defaultVersionInt, err := strconv.Atoi(defaultVersion)
		if err != nil {
			ctx.ModuleErrorf("expected RELEASE_DEFAULT_UPDATABLE_MODULE_VERSION to be an int, but got %s", defaultVersion)
		}
		if defaultVersionInt%10 != 0 && defaultVersionInt%10 != 9 {
			ctx.ModuleErrorf("expected RELEASE_DEFAULT_UPDATABLE_MODULE_VERSION to end in a zero or a nine, but got %s", defaultVersion)
		}
		variantVersion := []rune(*a.properties.Variant_version)
		if len(variantVersion) != 1 || variantVersion[0] < '0' || variantVersion[0] > '9' {
			ctx.PropertyErrorf("variant_version", "expected an integer between 0-9; got %s", *a.properties.Variant_version)
		}
		defaultVersionRunes := []rune(defaultVersion)
		defaultVersionRunes[len(defaultVersion)-1] = []rune(variantVersion)[0]
		defaultVersion = string(defaultVersionRunes)
	}
	if override := ctx.Config().Getenv("OVERRIDE_APEX_MANIFEST_DEFAULT_VERSION"); override != "" {
		defaultVersion = override
	}
	ctx.Build(pctx, android.BuildParams{
		Rule:   apexManifestRule,
		Input:  src,
		Output: manifestJsonFullOut,
		Args: map[string]string{
			"provideNativeLibs": strings.Join(provideNativeLibs, " "),
			"requireNativeLibs": strings.Join(requireNativeLibs, " "),
			"default_version":   defaultVersion,
			"opt":               strings.Join(optCommands, " "),
		},
	})

	// b/143654022 Q apexd can't understand newly added keys in apex_manifest.json prepare
	// stripped-down version so that APEX modules built from R+ can be installed to Q
	minSdkVersion := a.minSdkVersion(ctx)
	if minSdkVersion.EqualTo(android.SdkVersion_Android10) {
		a.manifestJsonOut = android.PathForModuleOut(ctx, "apex_manifest.json")
		ctx.Build(pctx, android.BuildParams{
			Rule:   stripApexManifestRule,
			Input:  manifestJsonFullOut,
			Output: a.manifestJsonOut,
		})
	}

	// From R+, protobuf binary format (.pb) is the standard format for apex_manifest
	a.manifestPbOut = android.PathForModuleOut(ctx, "apex_manifest.pb")
	ctx.Build(pctx, android.BuildParams{
		Rule:   pbApexManifestRule,
		Input:  manifestJsonFullOut,
		Output: a.manifestPbOut,
	})
}

// buildFileContexts create build rules to append an entry for apex_manifest.pb to the file_contexts
// file for this APEX which is either from /systme/sepolicy/apex/<apexname>-file_contexts or from
// the file_contexts property of this APEX. This is to make sure that the manifest file is correctly
// labeled as system_file or vendor_apex_metadata_file.
func (a *apexBundle) buildFileContexts(ctx android.ModuleContext) android.Path {
	var fileContexts android.Path
	var fileContextsDir string
	isFileContextsModule := false
	if a.properties.File_contexts == nil {
		fileContexts = android.PathForSource(ctx, "system/sepolicy/apex", ctx.ModuleName()+"-file_contexts")
	} else {
		if m, t := android.SrcIsModuleWithTag(*a.properties.File_contexts); m != "" {
			isFileContextsModule = true
			otherModule := android.GetModuleProxyFromPathDep(ctx, m, t)
			if !otherModule.IsNil() {
				fileContextsDir = ctx.OtherModuleDir(otherModule)
			}
		}
		fileContexts = android.PathForModuleSrc(ctx, *a.properties.File_contexts)
	}
	if fileContextsDir == "" {
		fileContextsDir = filepath.Dir(fileContexts.String())
	}
	fileContextsDir += string(filepath.Separator)

	if a.Platform() {
		if !strings.HasPrefix(fileContextsDir, "system/sepolicy/") {
			ctx.PropertyErrorf("file_contexts", "should be under system/sepolicy, but found in  %q", fileContextsDir)
		}
	}
	if !isFileContextsModule && !android.ExistentPathForSource(ctx, fileContexts.String()).Valid() {
		ctx.PropertyErrorf("file_contexts", "cannot find file_contexts file: %q", fileContexts.String())
	}

	output := android.PathForModuleOut(ctx, "file_contexts")
	rule := android.NewRuleBuilder(pctx, ctx)

	labelForRoot := "u:object_r:system_file:s0"
	labelForManifest := "u:object_r:system_file:s0"
	if a.SocSpecific() && !a.vndkApex {
		// APEX on /vendor should label ./ and ./apex_manifest.pb as vendor file.
		labelForRoot = "u:object_r:vendor_file:s0"
		labelForManifest = "u:object_r:vendor_apex_metadata_file:s0"
	}
	// remove old file
	rule.Command().BuiltTool("rm").FlagWithOutput("-f ", output)
	// copy file_contexts
	rule.Command().BuiltTool("cat").Input(fileContexts).Text(">>").Output(output)
	// new line
	rule.Command().BuiltTool("echo").Text(">>").Output(output)
	// force-label /apex_manifest.pb and /
	rule.Command().BuiltTool("echo").Text("/apex_manifest\\\\.pb").Text(labelForManifest).Text(">>").Output(output)
	rule.Command().BuiltTool("echo").Text("/").Text(labelForRoot).Text(">>").Output(output)

	rule.Build("file_contexts."+a.Name(), "Generate file_contexts")
	return output
}

// buildInstalledFilesFile creates a build rule for the installed-files.txt file where the list of
// files included in this APEX is shown. The text file is dist'ed so that people can see what's
// included in the APEX without actually downloading and extracting it.
func (a *apexBundle) buildInstalledFilesFile(ctx android.ModuleContext, builtApex android.Path, imageZip android.Path) android.Path {
	output := android.PathForModuleOut(ctx, "installed-files.txt")
	unzippedDir := android.PathForModuleOut(ctx, "installed-files-unzipped")
	rule := android.NewRuleBuilder(pctx, ctx)
	rule.Command().
		Implicit(builtApex).
		BuiltTool("zipsync").
		FlagWithArg("-d ", unzippedDir.String()).
		Input(imageZip)
	rule.Command().
		BuiltTool("find").
		Text(unzippedDir.String()).
		Text("\\( -type f -o -type l \\) -printf \"%s ./%P\\n\" |").
		BuiltTool("sort").
		Text("-nr > ").
		Output(output)
	rule.Build("installed-files."+a.Name(), "Installed files")
	return output
}

// buildBundleConfig creates a build rule for the bundle config file that will control the bundle
// creation process.
func (a *apexBundle) buildBundleConfig(ctx android.ModuleContext) android.Path {
	output := android.PathForModuleOut(ctx, "bundle_config.json")

	type ApkConfig struct {
		Package_name string `json:"package_name"`
		Apk_path     string `json:"path"`
	}
	config := struct {
		Compression struct {
			Uncompressed_glob []string `json:"uncompressed_glob"`
		} `json:"compression"`
		Apex_config struct {
			Apex_embedded_apk_config []ApkConfig `json:"apex_embedded_apk_config,omitempty"`
		} `json:"apex_config,omitempty"`
	}{}

	config.Compression.Uncompressed_glob = []string{
		"apex_payload.img",
		"apex_manifest.*",
	}

	// Collect the manifest names and paths of android apps if their manifest names are
	// overridden.
	for _, fi := range a.filesInfo {
		if fi.class != app && fi.class != appSet {
			continue
		}
		packageName := fi.overriddenPackageName
		if packageName != "" {
			config.Apex_config.Apex_embedded_apk_config = append(
				config.Apex_config.Apex_embedded_apk_config,
				ApkConfig{
					Package_name: packageName,
					Apk_path:     fi.path(),
				})
		}
	}

	j, err := json.Marshal(config)
	if err != nil {
		panic(fmt.Errorf("error while marshalling to %q: %#v", output, err))
	}

	android.WriteFileRule(ctx, output, string(j))

	return output
}

func markManifestTestOnly(ctx android.ModuleContext, androidManifestFile android.Path) android.Path {
	return java.ManifestFixer(ctx, androidManifestFile, java.ManifestFixerParams{
		TestOnly: true,
	})
}

func shouldApplyAssembleVintf(fi apexFile) bool {
	isVintfFragment, _ := path.Match("etc/vintf/*", fi.path())
	fromVintfFragmentModule := fi.providers != nil && fi.providers.vintfFragmentInfo != nil
	return isVintfFragment && !fromVintfFragmentModule
}

func runAssembleVintf(ctx android.ModuleContext, vintfFragment android.Path) android.Path {
	processed := android.PathForModuleOut(ctx, "vintf", vintfFragment.Base())
	ctx.Build(pctx, android.BuildParams{
		Rule:        android.AssembleVintfRule,
		Input:       vintfFragment,
		Output:      processed,
		Description: "run assemble_vintf for VINTF in APEX",
	})
	return processed
}

// installApexSystemServerFiles installs dexpreopt and dexjar files for system server classpath entries
// provided by the apex.  They are installed here instead of in library module because there may be multiple
// variants of the library, generally one for the "main" apex and another with a different min_sdk_version
// for the Android Go version of the apex.  Both variants would attempt to install to the same locations,
// and the library variants cannot determine which one should.  The apex module is better equipped to determine
// if it is "selected".
// This assumes that the jars produced by different min_sdk_version values are identical, which is currently
// true but may not be true if the min_sdk_version difference between the variants spans version that changed
// the dex format.
func (a *apexBundle) installApexSystemServerFiles(ctx android.ModuleContext) {
	// If performInstalls is set this module is responsible for creating the install rules.
	performInstalls := a.GetOverriddenBy() == "" && !a.testApex && a.installable()
	// TODO(b/234351700): Remove once ART does not have separated debug APEX, or make the selection
	// explicit in the ART Android.bp files.
	if ctx.Config().UseDebugArt() {
		if ctx.ModuleName() == "com.android.art" {
			performInstalls = false
		}
	} else {
		if ctx.ModuleName() == "com.android.art.debug" {
			performInstalls = false
		}
	}

	psi := android.PrebuiltSelectionInfoMap{}
	ctx.VisitDirectDepsProxy(func(am android.ModuleProxy) {
		if info, exists := android.OtherModuleProvider(ctx, am, android.PrebuiltSelectionInfoProvider); exists {
			psi = info
		}
	})

	if len(psi.GetSelectedModulesForApiDomain(ctx.ModuleName())) > 0 {
		performInstalls = false
	}

	for _, fi := range a.filesInfo {
		for _, install := range fi.systemServerDexpreoptInstalls {
			var installedFile android.InstallPath
			if performInstalls {
				// android_device will create the install rule in soong-only builds.
				// Skip creating the installation rule from the base variant
				// in soong-only builds to prevent duplicate installation rules.
				installedFile = ctx.InstallFile(install.InstallDirOnDevice, install.InstallFileOnDevice, install.OutputPathOnHost)
			} else {
				// Another module created the install rules, but this module should still depend on
				// the installed locations.
				installedFile = install.InstallDirOnDevice.Join(ctx, install.InstallFileOnDevice)
			}
			a.extraInstalledFiles = append(a.extraInstalledFiles, installedFile)
			a.extraInstalledPairs = append(a.extraInstalledPairs, installPair{install.OutputPathOnHost, installedFile})
			ctx.PackageFileWithFakeFullInstall(install.InstallDirOnDevice, install.InstallFileOnDevice, install.OutputPathOnHost)
		}
		if performInstalls {
			for _, dexJar := range fi.systemServerDexJars {
				// Copy the system server dex jar to a predefined location where dex2oat will find it.
				android.CopyFileRule(ctx, dexJar,
					android.PathForOutput(ctx, dexpreopt.SystemServerDexjarsDir, dexJar.Base()))
			}
		}
	}
}

// buildApex creates build rules to build an APEX using apexer.
func (a *apexBundle) buildApex(ctx android.ModuleContext) {
	suffix := imageApexSuffix
	apexName := a.BaseModuleName()

	////////////////////////////////////////////////////////////////////////////////////////////
	// Step 1: copy built files to appropriate directories under the image directory

	imageDir := android.PathForModuleOut(ctx, "image"+suffix)

	installSymbolFiles := a.ExportedToMake() && a.installable()

	// set of dependency module:location mappings
	installMapSet := make(map[string]bool)

	// TODO(jiyong): use the RuleBuilder
	var copyCommands []string
	var implicitInputs []android.Path
	rmCmd, _, _ := android.Rm.GetValueAndDeps(ctx.Config())
	mkdirCmd, _, _ := android.Mkdir.GetValueAndDeps(ctx.Config())
	lnCmd, _, _ := android.Ln.GetValueAndDeps(ctx.Config())
	cpCmd, _, _ := android.Cp.GetValueAndDeps(ctx.Config())
	zipSyncCmd, _, _ := android.ZipSync.GetValueAndDeps(ctx.Config())
	apexDir := android.PathForModuleInPartitionInstall(ctx, "apex", apexName)
	for _, fi := range a.filesInfo {
		destPath := imageDir.Join(ctx, fi.path()).String()
		// Prepare the destination path
		destPathDir := filepath.Dir(destPath)
		if fi.class == appSet {
			copyCommands = append(copyCommands, rmCmd+" -rf "+destPathDir)
		}
		copyCommands = append(copyCommands, mkdirCmd+" -p "+destPathDir)

		installMapPath := fi.builtFile

		// Copy the built file to the directory. But if the symlink optimization is turned
		// on, place a symlink to the corresponding file in /system partition instead.
		if a.linkToSystemLib && fi.transitiveDep && fi.availableToPlatform() {
			pathOnDevice := filepath.Join("/", fi.partition, fi.path())
			copyCommands = append(copyCommands, lnCmd+" -sfn "+pathOnDevice+" "+destPath)
		} else {
			// Copy the file into APEX
			if !a.testApex && shouldApplyAssembleVintf(fi) {
				// copy the output of assemble_vintf instead of the original
				vintfFragment := runAssembleVintf(ctx, fi.builtFile)
				copyCommands = append(copyCommands, cpCmd+" -f "+vintfFragment.String()+" "+destPath)
				implicitInputs = append(implicitInputs, vintfFragment)
			} else {
				copyCommands = append(copyCommands, cpCmd+" -f "+fi.builtFile.String()+" "+destPath)
				implicitInputs = append(implicitInputs, fi.builtFile)
			}
			if fi.extraZip.Valid() {
				copyCommands = append(copyCommands,
					fmt.Sprintf("%s -d %s %s", zipSyncCmd, destPathDir, fi.extraZip.String()))
				implicitInputs = append(implicitInputs, fi.extraZip.Path())
			}

			var installedPath android.InstallPath
			if installSymbolFiles {
				if fi.extraZip.Valid() {
					installedPath = ctx.InstallFileWithExtraFilesZip(apexDir.Join(ctx, fi.installDir),
						fi.stem(), fi.builtFile, fi.extraZip.Path())
				} else {
					// store installedPath. symlinks might be created if required.
					installedPath = ctx.InstallFile(apexDir.Join(ctx, fi.installDir), fi.stem(), fi.builtFile)
				}
			}

			// Create additional symlinks pointing the file inside the APEX (if any). Note that
			// this is independent from the symlink optimization.
			for _, symlinkPath := range fi.symlinkPaths() {
				symlinkDest := imageDir.Join(ctx, symlinkPath).String()
				copyCommands = append(copyCommands, lnCmd+" -sfn "+filepath.Base(destPath)+" "+symlinkDest)
				if installSymbolFiles {
					ctx.InstallSymlink(apexDir.Join(ctx, filepath.Dir(symlinkPath)), filepath.Base(symlinkPath), installedPath)
				}
			}

			installMapPath = installedPath
		}

		// Copy the test files (if any)
		for _, d := range fi.dataPaths {
			// TODO(eakammer): This is now the third repetition of ~this logic for test paths, refactoring should be possible
			relPath := d.ToRelativeInstallPath()
			dataDest := imageDir.Join(ctx, fi.apexRelativePath(relPath)).String()

			copyCommands = append(copyCommands, cpCmd+" -f "+d.SrcPath.String()+" "+dataDest)
			implicitInputs = append(implicitInputs, d.SrcPath)
		}

		installMapSet[installMapPath.String()+":"+fi.installDir+"/"+fi.builtFile.Base()] = true
	}

	implicitInputs = append(implicitInputs, a.manifestPbOut)

	if len(installMapSet) > 0 {
		var installs []string
		installs = append(installs, android.SortedKeys(installMapSet)...)
		ctx.SetLicenseInstallMap(installs)
	}

	////////////////////////////////////////////////////////////////////////////////////////////
	// Step 1.a: Write the list of files in this APEX to a txt file and compare it against
	// the allowed list given via the allowed_files property. Build fails when the two lists
	// differ.
	//
	// TODO(jiyong): consider removing this. Nobody other than com.android.apex.cts.shim.* seems
	// to be using this at this moment. Furthermore, this looks very similar to what
	// buildInstalledFilesFile does. At least, move this to somewhere else so that this doesn't
	// hurt readability.
	if a.overridableProperties.Allowed_files != nil {
		// Build content.txt
		var contentLines []string
		imageContentFile := android.PathForModuleOut(ctx, "content.txt")
		contentLines = append(contentLines, "./apex_manifest.pb")
		minSdkVersion := a.minSdkVersion(ctx)
		if minSdkVersion.EqualTo(android.SdkVersion_Android10) {
			contentLines = append(contentLines, "./apex_manifest.json")
		}
		for _, fi := range a.filesInfo {
			contentLines = append(contentLines, "./"+fi.path())
		}
		sort.Strings(contentLines)
		android.WriteFileRule(ctx, imageContentFile, strings.Join(contentLines, "\n"))
		implicitInputs = append(implicitInputs, imageContentFile)

		// Compare content.txt against allowed_files.
		allowedFilesFile := android.PathForModuleSrc(ctx, proptools.String(a.overridableProperties.Allowed_files))
		phonyOutput := android.PathForModuleOut(ctx, a.Name()+"-diff-phony-output")
		ctx.Build(pctx, android.BuildParams{
			Rule:        diffApexContentRule,
			Implicits:   implicitInputs,
			Output:      phonyOutput,
			Description: "diff apex image content",
			Args: map[string]string{
				"allowed_files_file": allowedFilesFile.String(),
				"image_content_file": imageContentFile.String(),
				"apex_module_name":   a.Name(),
			},
		})
		implicitInputs = append(implicitInputs, phonyOutput)
	}

	unsignedOutputFile := android.PathForModuleOut(ctx, a.Name()+suffix+".unsigned")
	outHostBinDir := ctx.Config().HostToolPath(ctx, "").String()
	prebuiltSdkToolsBinDir := filepath.Join("prebuilts", "sdk", "tools", runtime.GOOS, "bin")

	////////////////////////////////////////////////////////////////////////////////////
	// Step 2: create canned_fs_config which encodes filemode,uid,gid of each files
	// in this APEX. The file will be used by apexer in later steps.
	cannedFsConfig := a.buildCannedFsConfig(ctx)
	implicitInputs = append(implicitInputs, cannedFsConfig)

	////////////////////////////////////////////////////////////////////////////////////
	// Step 3: Prepare option flags for apexer and invoke it to create an unsigned APEX.
	// TODO(jiyong): use the RuleBuilder
	optFlags := []string{}

	fileContexts := a.buildFileContexts(ctx)
	implicitInputs = append(implicitInputs, fileContexts)

	payloadKey := android.ApexPayloadKey(ctx, a.Name(), a.privateKeyFile)
	publicKey := a.extractPayloadPublicKey(ctx, payloadKey)
	implicitInputs = append(implicitInputs, payloadKey.Inputs()...)
	implicitInputs = append(implicitInputs, publicKey)
	// apexer sets the key name of the payload to the APEX name, as the stock signer does when it
	// builds a payload again, instead of the name of the key file in keys_dir.
	optFlags = append(optFlags, "--pubkey "+publicKey.String(), "--do_not_check_keyname")

	manifestPackageName := a.getOverrideManifestPackageName(ctx)
	if manifestPackageName != "" {
		optFlags = append(optFlags, "--override_apk_package_name "+manifestPackageName)
	}

	androidManifest := a.properties.AndroidManifest.GetOrDefault(ctx, "")
	if androidManifest != "" {
		androidManifestFile := android.PathForModuleSrc(ctx, androidManifest)

		if a.testApex {
			androidManifestFile = markManifestTestOnly(ctx, androidManifestFile)
		}

		implicitInputs = append(implicitInputs, androidManifestFile)
		optFlags = append(optFlags, "--android_manifest "+androidManifestFile.String())
	} else if a.testApex {
		optFlags = append(optFlags, "--test_only")
	}

	// Determine target/min sdk version from the context
	// TODO(jiyong): make this as a function
	moduleMinSdkVersion := a.minSdkVersion(ctx)
	minSdkVersion := moduleMinSdkVersion.String()

	// bundletool doesn't understand what "current" is. We need to transform it to
	// codename
	if moduleMinSdkVersion.IsCurrent() || moduleMinSdkVersion.IsNone() {
		minSdkVersion = ctx.Config().DefaultAppTargetSdk(ctx).String()

		if useApiFingerprint, fingerprintMinSdkVersion, fingerprintDeps :=
			java.UseApiFingerprint(ctx); useApiFingerprint {
			minSdkVersion = fingerprintMinSdkVersion
			implicitInputs = append(implicitInputs, fingerprintDeps)
		}
	}
	// apex module doesn't have a concept of target_sdk_version, hence for the time
	// being targetSdkVersion == default targetSdkVersion of the branch.
	targetSdkVersion := strconv.Itoa(ctx.Config().DefaultAppTargetSdk(ctx).FinalOrFutureInt())

	if useApiFingerprint, fingerprintTargetSdkVersion, fingerprintDeps :=
		java.UseApiFingerprint(ctx); useApiFingerprint {
		targetSdkVersion = fingerprintTargetSdkVersion
		implicitInputs = append(implicitInputs, fingerprintDeps)
	}
	optFlags = append(optFlags, "--target_sdk_version "+targetSdkVersion)
	optFlags = append(optFlags, "--min_sdk_version "+minSdkVersion)

	if a.overridableProperties.Logging_parent != "" {
		optFlags = append(optFlags, "--logging_parent ", a.overridableProperties.Logging_parent)
	}

	// Create a NOTICE file, and embed it as an asset file in the APEX.
	htmlGzNotice := android.PathForModuleOut(ctx, "NOTICE.html.gz")
	android.BuildNoticeHtmlOutputFromLicenseMetadata(
		ctx, htmlGzNotice, "", "",
		android.BuildNoticeFromLicenseDataArgs{
			StripPrefix: []string{
				android.PathForModuleInstall(ctx).String() + "/",
				android.PathForModuleInPartitionInstall(ctx, "apex").String() + "/",
			},
		},
		ctx.ModuleProxy(),
	)
	noticeAssetPath := android.PathForModuleOut(ctx, "NOTICE", "NOTICE.html.gz")
	builder := android.NewRuleBuilder(pctx, ctx)
	builder.Command().BuiltTool("cp").
		Input(htmlGzNotice).
		Output(noticeAssetPath)
	builder.Build("notice_dir", "Building notice dir")
	implicitInputs = append(implicitInputs, noticeAssetPath)
	optFlags = append(optFlags, "--assets_dir "+filepath.Dir(noticeAssetPath.String()))

	if a.testOnlyShouldSkipPayloadSign() {
		optFlags = append(optFlags, "--unsigned_payload")
	}

	if moduleMinSdkVersion == android.SdkVersion_Android10 {
		implicitInputs = append(implicitInputs, a.manifestJsonOut)
		optFlags = append(optFlags, "--manifest_json "+a.manifestJsonOut.String())
	}

	optFlags = append(optFlags, "--payload_fs_type "+a.payloadFsType.string())

	if a.payloadFsType == erofs {
		args, inputs := a.buildApexerArgsForErofs(ctx)
		optFlags = append(optFlags, args...)
		implicitInputs = append(implicitInputs, inputs...)
	}

	if a.nonProduction() {
		optFlags = append(optFlags, "--non_production")
	}

	imageZipOut := android.PathForModuleOut(ctx, "image.zip")
	imageRule, signToolRule := apexRule, apexWithSignToolRule
	if a.dynamic_common_lib_apex() {
		imageRule, signToolRule = DCLAApexRule, DCLAApexWithSignToolRule
	}
	imageArgs := map[string]string{
		"tool_path":        outHostBinDir + ":" + prebuiltSdkToolsBinDir,
		"image_dir":        imageDir.String(),
		"image_zip":        imageZipOut.String(),
		"copy_commands":    strings.Join(copyCommands, " && "),
		"manifest":         a.manifestPbOut.String(),
		"file_contexts":    fileContexts.String(),
		"canned_fs_config": cannedFsConfig.String(),
		"key":              payloadKey.Ref(),
		"opt_flags":        strings.Join(optFlags, " "),
	}
	if signTool := proptools.String(a.properties.Custom_sign_tool); signTool != "" {
		imageRule = signToolRule
		imageArgs["sign_tool"] = ctx.Config().HostToolPath(ctx, signTool).String()
		implicitInputs = append(implicitInputs, android.HostToolDeps(ctx, signTool))
	}
	ctx.Build(pctx, android.BuildParams{
		Rule:           imageRule,
		Implicits:      implicitInputs,
		Output:         unsignedOutputFile,
		ImplicitOutput: imageZipOut,
		Description:    "apex",
		Args:           imageArgs,
	})

	// TODO(jiyong): make the two rules below as separate functions
	apexProtoFile := android.PathForModuleOut(ctx, a.Name()+".pb"+suffix)
	bundleModuleFile := android.PathForModuleOut(ctx, a.Name()+suffix+"-base.zip")
	a.bundleModuleFile = bundleModuleFile

	ctx.Build(pctx, android.BuildParams{
		Rule:        apexProtoConvertRule,
		Input:       unsignedOutputFile,
		Output:      apexProtoFile,
		Description: "apex proto convert",
	})

	implicitInputs = append(implicitInputs, unsignedOutputFile)

	// Run coverage analysis
	apisUsedbyOutputFile := android.PathForModuleOut(ctx, a.Name()+"_using.txt")
	ctx.Build(pctx, android.BuildParams{
		Rule:        generateAPIsUsedbyApexRule,
		Input:       imageZipOut,
		Implicits:   implicitInputs,
		Description: "coverage",
		Output:      apisUsedbyOutputFile,
		Args: map[string]string{
			"image_zip": imageZipOut.String(),
		},
	})

	var nativeLibNames []string
	for _, f := range a.filesInfo {
		if f.class == nativeSharedLib {
			nativeLibNames = append(nativeLibNames, f.stem())
		}
	}
	apisBackedbyOutputFile := android.PathForModuleOut(ctx, a.Name()+"_backing.txt")
	rb := android.NewRuleBuilder(pctx, ctx)
	rb.Command().
		BuiltTool("gen_apex_symbols").
		Text("ndk_backedby").
		Output(apisBackedbyOutputFile).
		Flags(nativeLibNames)
	rb.Build("ndk_backedby_list", "Generate API libraries backed by Apex")

	var javaLibOrApkPath []android.Path
	for _, f := range a.filesInfo {
		if f.class == javaSharedLib || f.class == app {
			javaLibOrApkPath = append(javaLibOrApkPath, f.builtFile)
		}
	}
	javaApiUsedbyOutputFile := android.PathForModuleOut(ctx, a.Name()+"_using.xml")
	javaUsedByRule := android.NewRuleBuilder(pctx, ctx)
	javaUsedByRule.Command().
		BuiltTool("gen_apex_symbols").
		Text("java_usedby").
		BuiltTool("dexdeps").
		Output(javaApiUsedbyOutputFile).
		Inputs(javaLibOrApkPath)
	javaUsedByRule.Build("java_usedby_list", "Generate Java APIs used by Apex")

	if slices.Contains(ctx.Config().UnbundledBuildApps(), a.Name()) && !android.ShouldSkipAndroidMkProcessing(ctx, a) {
		ctx.DistForGoalWithFilename("apps_only", apisUsedbyOutputFile, "ndk_apis_usedby_apex/"+apisUsedbyOutputFile.Base())
		ctx.DistForGoalWithFilename("apps_only", apisBackedbyOutputFile, "ndk_apis_backedby_apex/"+apisBackedbyOutputFile.Base())
		ctx.DistForGoalWithFilename("apps_only", javaApiUsedbyOutputFile, "java_apis_used_by_apex/"+javaApiUsedbyOutputFile.Base())
	}

	bundleConfig := a.buildBundleConfig(ctx)

	var abis []string
	for _, target := range ctx.MultiTargets() {
		if len(target.Arch.Abi) > 0 {
			abis = append(abis, target.Arch.Abi[0])
		}
	}

	abis = android.FirstUniqueStrings(abis)

	ctx.Build(pctx, android.BuildParams{
		Rule:        apexBundleRule,
		Input:       apexProtoFile,
		Implicit:    bundleConfig,
		Output:      a.bundleModuleFile,
		Description: "apex bundle module",
		Args: map[string]string{
			"abi":    strings.Join(abis, "."),
			"config": bundleConfig.String(),
		},
	})

	////////////////////////////////////////////////////////////////////////////////////
	// Step 4: Sign the APEX using signapk
	signedOutputFile := android.PathForModuleOut(ctx, a.Name()+suffix)

	pemSrc, keySrc := a.getCertificateAndPrivateKey(ctx)
	pem, key := android.ApexContainerKeys(ctx, a.Name(), pemSrc, keySrc)
	rule := java.Signapk
	args := map[string]string{
		"certificates": pem.Ref() + " " + key.Ref(),
		"flags":        "--disable-v1 -a 4096 --align-file-size", //alignment
	}
	implicits := slices.Concat(pem.Inputs(), key.Inputs())
	if ctx.Config().UseREWrapper() && ctx.Config().IsEnvTrue("RBE_SIGNAPK") {
		rule = java.SignapkRE
		args["implicits"] = strings.Join(implicits.Strings(), ",")
		args["outCommaList"] = signedOutputFile.String()
	}
	var validations android.Paths
	validations = append(validations, runApexLinkerconfigValidation(ctx, signedOutputFile, imageZipOut))
	if !a.skipValidation(apexSepolicyTests) && android.InList(a.payloadFsType, []fsType{ext4, erofs}) {
		validations = append(validations, runApexSepolicyTests(ctx, a, signedOutputFile))
	}
	if !a.testApex && len(a.properties.Unwanted_transitive_deps) > 0 {
		validations = append(validations,
			runApexElfCheckerUnwanted(ctx, signedOutputFile, a.properties.Unwanted_transitive_deps))
	}
	if !a.skipValidation(hostApexVerifier) && android.InList(a.payloadFsType, []fsType{ext4, erofs}) {
		validations = append(validations, runApexHostVerifier(ctx, a, signedOutputFile))
	}
	ctx.Build(pctx, android.BuildParams{
		Rule:        rule,
		Description: "signapk",
		Output:      signedOutputFile,
		Input:       unsignedOutputFile,
		Implicits:   implicits,
		Args:        args,
		Validations: validations,
	})
	if suffix == imageApexSuffix {
		a.outputApexFile = signedOutputFile
	}
	a.outputFile = signedOutputFile

	if ctx.ModuleDir() != "system/apex/apexd/apexd_testdata" && a.testOnlyShouldForceCompression() {
		ctx.PropertyErrorf("test_only_force_compression", "not available")
		return
	}

	installSuffix := suffix
	a.setCompression(ctx)
	if a.isCompressed {
		unsignedCompressedOutputFile := android.PathForModuleOut(ctx, a.Name()+imageCapexSuffix+".unsigned")

		compressRule := android.NewRuleBuilder(pctx, ctx)
		compressRule.Command().
			BuiltTool("rm").
			FlagWithOutput("-f ", unsignedCompressedOutputFile)
		compressRule.Command().
			BuiltTool("apex_compression_tool").
			Flag("compress").
			FlagWithArg("--apex_compression_tool ", outHostBinDir+":"+prebuiltSdkToolsBinDir).
			FlagWithInput("--input ", signedOutputFile).
			FlagWithOutput("--output ", unsignedCompressedOutputFile)
		compressRule.Build("compressRule", "Generate unsigned compressed APEX file")

		signedCompressedOutputFile := android.PathForModuleOut(ctx, a.Name()+imageCapexSuffix)
		if ctx.Config().UseREWrapper() && ctx.Config().IsEnvTrue("RBE_SIGNAPK") {
			args["outCommaList"] = signedCompressedOutputFile.String()
		}
		ctx.Build(pctx, android.BuildParams{
			Rule:        rule,
			Description: "sign compressedApex",
			Output:      signedCompressedOutputFile,
			Input:       unsignedCompressedOutputFile,
			Implicits:   implicits,
			Args:        args,
		})
		a.outputFile = signedCompressedOutputFile
		installSuffix = imageCapexSuffix
	}

	if !a.installable() {
		a.SkipInstall()
	}

	installDeps := slices.Concat(a.compatSymlinks, a.extraInstalledFiles)
	// Install to $OUT/soong/{target,host}/.../apex.
	a.installedFile = ctx.InstallFile(a.installDir, a.Name()+installSuffix, a.outputFile, installDeps...)

	// installed-files.txt is dist'ed
	a.installedFilesFile = a.buildInstalledFilesFile(ctx, a.outputFile, imageZipOut)

	a.apexKeysPath = writeApexKeys(ctx, a)
}

// extractPayloadPublicKey writes the public key of payloadKey.
func (a *apexBundle) extractPayloadPublicKey(ctx android.ModuleContext, payloadKey android.KeysDirKey) android.Path {
	publicKey := android.PathForModuleOut(ctx, a.Name()+".avbpubkey")
	ctx.Build(pctx, android.BuildParams{
		Rule:      extractPayloadPublicKeyRule,
		Implicits: payloadKey.Inputs(),
		Output:    publicKey,
		Args: map[string]string{
			"key": payloadKey.Ref(),
		},
	})
	return publicKey
}

// getCertificateAndPrivateKey retrieves the cert and the private key that will be used to sign
// the zip container of this APEX. See the description of the 'certificate' property for how
// the cert and the private key are found.
func (a *apexBundle) getCertificateAndPrivateKey(ctx android.PathContext) (pem, key android.Path) {
	if a.containerCertificateFile != nil {
		return a.containerCertificateFile, a.containerPrivateKeyFile
	}

	cert := String(a.overridableProperties.Certificate)
	if cert == "" {
		return ctx.Config().DefaultAppCertificate(ctx)
	}

	defaultDir := ctx.Config().DefaultAppCertificateDir(ctx)
	pem = defaultDir.Join(ctx, cert+".x509.pem")
	key = defaultDir.Join(ctx, cert+".pk8")
	return pem, key
}

func (a *apexBundle) getOverrideManifestPackageName(ctx android.ModuleContext) string {
	// For VNDK APEXes, check "com.android.vndk" in PRODUCT_MANIFEST_PACKAGE_NAME_OVERRIDES
	// to see if it should be overridden because their <apex name> is dynamically generated
	// according to its VNDK version.
	if a.vndkApex {
		overrideName, overridden := ctx.DeviceConfig().OverrideManifestPackageNameFor(vndkApexName)
		if overridden {
			return overrideName + ".v" + a.vndkVersion()
		}
		return ""
	}
	packageNameFromProp := a.overridableProperties.Package_name.GetOrDefault(ctx, "")
	if packageNameFromProp != "" {
		return packageNameFromProp
	}
	manifestPackageName, overridden := ctx.DeviceConfig().OverrideManifestPackageNameFor(ctx.ModuleName())
	if overridden {
		return manifestPackageName
	}
	return ""
}

func (a *apexBundle) buildApexDependencyInfo(ctx android.ModuleContext) {
	depInfos := android.DepNameToDepInfoMap{}
	a.WalkPayloadDeps(ctx, func(ctx android.BaseModuleContext, from, to android.ModuleProxy, externalDep bool) bool {
		if from.Name() == to.Name() {
			// This can happen for cc.reuseObjTag. We are not interested in tracking this.
			// As soon as the dependency graph crosses the APEX boundary, don't go further.
			return !externalDep
		}

		// Skip dependencies that are only available to APEXes; they are developed with updatability
		// in mind and don't need manual approval.
		if android.OtherModuleProviderOrDefault(ctx, to, android.PlatformAvailabilityInfoProvider).NotAvailableToPlatform {
			return !externalDep
		}

		depTag := ctx.OtherModuleDependencyTag(to)
		// Check to see if dependency been marked to skip the dependency check
		if skipDepCheck, ok := depTag.(android.SkipApexAllowedDependenciesCheck); ok && skipDepCheck.SkipApexAllowedDependenciesCheck() {
			return !externalDep
		}

		if info, exists := depInfos[to.Name()]; exists {
			if !android.InList(from.Name(), info.From) {
				info.From = append(info.From, from.Name())
			}
			info.IsExternal = info.IsExternal && externalDep
			depInfos[to.Name()] = info
		} else {
			toMinSdkVersion := "(no version)"
			if info, ok := android.OtherModuleProvider(ctx, to, android.CommonModuleInfoProvider); ok &&
				!info.MinSdkVersion.IsPlatform && info.MinSdkVersion.ApiLevel != nil {
				toMinSdkVersion = info.MinSdkVersion.ApiLevel.String()
			}
			depInfos[to.Name()] = android.ApexModuleDepInfo{
				To:            to.Name(),
				From:          []string{from.Name()},
				IsExternal:    externalDep,
				MinSdkVersion: toMinSdkVersion,
			}
		}

		// As soon as the dependency graph crosses the APEX boundary, don't go further.
		return !externalDep
	})

	a.ApexBundleDepsInfo.BuildDepsInfoLists(ctx, a.MinSdkVersion(ctx).String(), depInfos)

	ctx.Phony(a.Name()+"-deps-info", a.ApexBundleDepsInfo.FullListPath(), a.ApexBundleDepsInfo.FlatListPath())
}

func (a *apexBundle) buildLintReports(ctx android.ModuleContext) {
	depSetsBuilder := java.NewLintDepSetBuilder()
	for _, fi := range a.filesInfo {
		if fi.lintInfo != nil {
			depSetsBuilder.Transitive(fi.lintInfo)
		}
	}

	depSets := depSetsBuilder.Build()
	var validations android.Paths

	if a.checkStrictUpdatabilityLinting(ctx) {
		baselines := depSets.Baseline.ToList()
		if len(baselines) > 0 {
			outputFile := java.VerifyStrictUpdatabilityChecks(ctx, baselines)
			validations = append(validations, outputFile)
		}
	}

	a.lintReports = java.BuildModuleLintReportZips(ctx, depSets, validations)
}

func (a *apexBundle) reexportJacocoInfo(ctx android.ModuleContext) {
	var jacocoInfos []java.JacocoInfo
	for _, fi := range a.filesInfo {
		if fi.jacocoInfo.ReportClassesFile != nil {
			jacocoInfos = append(jacocoInfos, fi.jacocoInfo)
		}
	}

	android.SetProvider(ctx, java.ApexJacocoInfoProvider, jacocoInfos)
	android.SetProvider(ctx, java.BundleProvider, java.BundleInfo{Bundle: a.bundleModuleFile})
}

func (a *apexBundle) buildCannedFsConfig(ctx android.ModuleContext) android.Path {
	var readOnlyPaths = []string{"apex_manifest.json", "apex_manifest.pb"}
	var executablePaths []string // this also includes dirs
	var zipDirs []string
	zipFiles := make(map[string]android.Path)
	for _, f := range a.filesInfo {
		pathInApex := f.path()
		if f.installDir == "bin" || strings.HasPrefix(f.installDir, "bin/") {
			executablePaths = append(executablePaths, pathInApex)
			for _, d := range f.dataPaths {
				rel := d.ToRelativeInstallPath()
				readOnlyPaths = append(readOnlyPaths, filepath.Join(f.installDir, rel))
			}
			for _, s := range f.symlinks {
				executablePaths = append(executablePaths, filepath.Join(f.installDir, s))
			}
		} else {
			readOnlyPaths = append(readOnlyPaths, pathInApex)
		}
		if f.extraZip.Valid() {
			zipDirs = append(zipDirs, f.installDir)
			zipFiles[f.installDir] = f.extraZip.Path()
		}
		dir := f.installDir
		for !android.InList(dir, executablePaths) && dir != "" {
			executablePaths = append(executablePaths, dir)
			dir, _ = filepath.Split(dir) // move up to the parent
			if len(dir) > 0 {
				// remove trailing slash
				dir = dir[:len(dir)-1]
			}
		}
	}
	sort.Strings(readOnlyPaths)
	sort.Strings(executablePaths)
	sort.Strings(zipDirs)

	cannedFsConfig := android.PathForModuleOut(ctx, "canned_fs_config")
	builder := android.NewRuleBuilder(pctx, ctx)
	cmd := builder.Command()
	cmd.Text("(")
	cmd.BuiltTool("echo").Text("'/ 1000 1000 0755';")
	for _, p := range readOnlyPaths {
		cmd.BuiltTool("echo").Textf("'/%s 1000 1000 0644';", p)
	}
	for _, p := range executablePaths {
		cmd.BuiltTool("echo").Textf("'/%s 0 2000 0755';", p)
	}
	for _, dir := range zipDirs {
		cmd.BuiltTool("echo").Textf("'/%s 0 2000 0755';", dir)
		file := zipFiles[dir]
		cmd.PrebuiltBuildTool(ctx, "ziptool").Text("zipinfo -1").Input(file).Text(`|`).BuiltTool("sed").Textf(`"s:\(.*\):/%s/\1 1000 1000 0644:";`, dir)
	}
	// Custom fs_config is "appended" to the last so that entries from the file are preferred
	// over default ones set above.
	customFsConfig := a.properties.Canned_fs_config.GetOrDefault(ctx, "")
	if customFsConfig != "" {
		cmd.BuiltTool("cat").Input(android.PathForModuleSrc(ctx, customFsConfig))
	}
	cmd.Text(")").FlagWithOutput("> ", cannedFsConfig)
	builder.Build("generateFsConfig", fmt.Sprintf("Generating canned fs config for %s", a.BaseModuleName()))

	return cannedFsConfig
}

func runApexLinkerconfigValidation(ctx android.ModuleContext, apexFile android.Path, imageZip android.Path) android.Path {
	timestamp := android.PathForModuleOut(ctx, "apex_linkerconfig_validation.timestamp")
	ctx.Build(pctx, android.BuildParams{
		Rule:     apexLinkerconfigValidationRule,
		Input:    apexFile,
		Output:   timestamp,
		Implicit: imageZip,
		Args: map[string]string{
			"image_zip": imageZip.String(),
		},
	})
	return timestamp
}

// buildApexerArgsForErofs returns the erofs specific arguments for apexer
func (a *apexBundle) buildApexerArgsForErofs(ctx android.ModuleContext) ([]string, android.Paths) {
	var args []string
	var inputs android.Paths

	compressor := ctx.Config().DefaultApexPayloadErofsCompressor()
	if a.properties.Erofs.Compressor != nil {
		compressor = *a.properties.Erofs.Compressor
	}
	if compressor != "" {
		args = append(args, "--erofs_compressor", compressor)
	}

	var hints android.Path
	hints = ctx.Config().DefaultApexPayloadErofsCompressHints(ctx)
	if a.properties.Erofs.Compress_hints != nil {
		hints = android.PathForModuleSrc(ctx, *a.properties.Erofs.Compress_hints)
	}
	if hints != nil {
		args = append(args, "--erofs_compress_hints", hints.String())
		inputs = append(inputs, hints)
	}

	pcluster_size := ctx.Config().DefaultApexPayloadErofsPclusterSize()
	if a.properties.Erofs.Pcluster_size != nil {
		pcluster_size = *a.properties.Erofs.Pcluster_size
	}
	if pcluster_size != 0 {
		args = append(args, "--erofs_pcluster_size", strconv.FormatInt(pcluster_size, 10))
	}
	return args, inputs
}

// Can't use PartitionTag() because PartitionTag() returns the partition this module is actually
// installed (e.g. PartitionTag() may return "system" for vendor apex when vendor is linked to /system/vendor)
func (a *apexBundle) partition() string {
	if a.SocSpecific() {
		return "vendor"
	} else if a.DeviceSpecific() {
		return "odm"
	} else if a.ProductSpecific() {
		return "product"
	} else if a.SystemExtSpecific() {
		return "system_ext"
	} else {
		return "system"
	}
}

// Runs apex_sepolicy_tests
//
// $ apex-ls -Z {apex_file} > {file_contexts}
// $ apex_sepolicy_tests -f {file_contexts}
func runApexSepolicyTests(ctx android.ModuleContext, a *apexBundle, apexFile android.Path) android.Path {
	timestamp := android.PathForModuleOut(ctx, "apex_sepolicy_tests.timestamp")
	ctx.Build(pctx, android.BuildParams{
		Rule:   apexSepolicyTestsRule,
		Input:  apexFile,
		Output: timestamp,
		Args: map[string]string{
			"partition_tag": a.partition(),
		},
	})
	return timestamp
}

func runApexElfCheckerUnwanted(ctx android.ModuleContext, apexFile android.Path, unwanted []string) android.Path {
	timestamp := android.PathForModuleOut(ctx, "apex_elf_unwanted.timestamp")
	ctx.Build(pctx, android.BuildParams{
		Rule:   apexElfCheckerUnwantedRule,
		Input:  apexFile,
		Output: timestamp,
		Args: map[string]string{
			"unwanted":  android.JoinWithSuffixAndSeparator(unwanted, ".so", ":"),
			"tool_path": ctx.Config().HostToolPath(ctx, "").String() + ":${config.ClangBin}",
		},
	})
	return timestamp
}

func runApexHostVerifier(ctx android.ModuleContext, a *apexBundle, apexFile android.Path) android.Path {
	timestamp := android.PathForModuleOut(ctx, "host_apex_verifier.timestamp")
	ctx.Build(pctx, android.BuildParams{
		Rule:   apexHostVerifierRule,
		Input:  apexFile,
		Output: timestamp,
		Args: map[string]string{
			"partition_tag": a.partition(),
		},
	})
	return timestamp
}
