// Copyright 2015 Google Inc. All rights reserved.
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

package java

// This file generates the final rules for compiling all Java.  All properties related to
// compiling should have been translated into javaBuilderFlags or another argument to the Transform*
// functions.

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"

	"android/soong/android"
	"android/soong/remoteexec"
)

var (
	Signapk, SignapkRE = pctx.RemoteStaticRules("signapk",
		blueprint.RuleParams{
			Command: `rm -f $out && $reTemplate${config.JavaCmd} ${config.JavaVmFlags} -Djava.library.path=$$(dirname ${config.SignapkJniLibrary}) ` +
				`-jar ${config.SignapkCmd} $flags $certificates $in $out`,
			CommandDeps:     []string{"${config.SignapkCmd}", "${config.SignapkJniLibrary}"},
			SandboxDisabled: true,
		},
		&remoteexec.REParams{Labels: map[string]string{"type": "tool", "name": "signapk"},
			ExecStrategy:    "${config.RESignApkExecStrategy}",
			Inputs:          []string{"${config.SignapkCmd}", "$in", "$$(dirname ${config.SignapkJniLibrary})", "$implicits"},
			OutputFiles:     []string{"$outCommaList"},
			ToolchainInputs: []string{"${config.JavaCmd}"},
			Platform:        map[string]string{remoteexec.PoolKey: "${config.REJavaPool}"},
		}, []string{"flags", "certificates"}, []string{"implicits", "outCommaList"})
)

var combineApk = pctx.AndroidStaticRule("combineApk",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.MergeZips, ` $out $in`,
		),
	})

func CreateAndSignAppPackage(ctx android.ModuleContext, outputFile android.WritablePath,
	packageFile, jniJarFile, dexJarFile android.Path, certificates []Certificate, deps android.Paths,
	v4SignatureFile android.WritablePath, lineageFile android.Path, rotationMinSdkVersion string,
	minSdkVersion android.ApiLevel) {

	unsignedApkName := strings.TrimSuffix(outputFile.Base(), ".apk") + "-unsigned.apk"
	unsignedApk := android.PathForModuleOut(ctx, unsignedApkName)

	var inputs android.Paths
	if dexJarFile != nil {
		inputs = append(inputs, dexJarFile)
	}
	inputs = append(inputs, packageFile)
	if jniJarFile != nil {
		inputs = append(inputs, jniJarFile)
	}
	ctx.Build(pctx, android.BuildParams{
		Rule:      combineApk,
		Inputs:    inputs,
		Output:    unsignedApk,
		Implicits: deps,
	})
	SignAppPackage(ctx, outputFile, unsignedApk, certificates, v4SignatureFile, lineageFile, rotationMinSdkVersion,
		minSdkVersion)
}

func SignAppPackage(ctx android.ModuleContext, signedApk android.WritablePath, unsignedApk android.Path,
	certificates []Certificate, v4SignatureFile android.WritablePath, lineageFile android.Path,
	rotationMinSdkVersion string, minSdkVersion android.ApiLevel) {

	var certificateArgs []string
	var deps android.Paths
	for _, c := range certificates {
		pem := android.SigningKeyFile(ctx, c.Pem)
		key := android.SigningKeyFile(ctx, c.Key)
		certificateArgs = append(certificateArgs, pem.Ref(), key.Ref())
		deps = append(deps, pem.Inputs()...)
		deps = append(deps, key.Inputs()...)
	}
	outputFiles := android.WritablePaths{signedApk}
	var flags []string
	if v4SignatureFile != nil {
		outputFiles = append(outputFiles, v4SignatureFile)
		flags = append(flags, "--enable-v4")
	}

	if lineageFile != nil {
		flags = append(flags, "--lineage", lineageFile.String())
		deps = append(deps, lineageFile)
	}

	if rotationMinSdkVersion != "" {
		flags = append(flags, "--rotation-min-sdk-version", rotationMinSdkVersion)
	}

	if minSdkVersion.GreaterThanOrEqualTo(android.UncheckedFinalApiLevel(24)) {
		flags = append(flags, "--disable-v1")
	}

	rule := Signapk
	args := map[string]string{
		"certificates": strings.Join(certificateArgs, " "),
		"flags":        strings.Join(flags, " "),
	}
	if ctx.Config().UseREWrapper() && ctx.Config().IsEnvTrue("RBE_SIGNAPK") {
		rule = SignapkRE
		args["implicits"] = strings.Join(deps.Strings(), ",")
		args["outCommaList"] = strings.Join(outputFiles.Strings(), ",")
	}
	ctx.Build(pctx, android.BuildParams{
		Rule:        rule,
		Description: "signapk",
		Outputs:     outputFiles,
		Input:       unsignedApk,
		Implicits:   deps,
		Args:        args,
	})
}

var buildAAR = pctx.AndroidStaticRule("buildAAR",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Rm, ` -rf ${outDir} && `,
			android.Mkdir, ` -p ${outDir} && `,
			android.Cp, ` ${manifest} ${outDir}/AndroidManifest.xml && `,
			android.Cp, ` ${classesJar} ${outDir}/classes.jar && `,
			android.Cp, ` ${rTxt} ${outDir}/R.txt && `,
			android.SoongZip, ` -jar -o $out -C ${outDir} -D ${outDir}`,
		),
	},
	"manifest", "classesJar", "rTxt", "outDir")

func BuildAAR(ctx android.ModuleContext, outputFile android.WritablePath,
	classesJar, manifest, rTxt android.Path, res android.Paths) {

	// TODO(ccross): uniquify and copy resources with dependencies

	deps := android.Paths{manifest, rTxt}
	classesJarPath := ""
	if classesJar != nil {
		deps = append(deps, classesJar)
		classesJarPath = classesJar.String()
	}

	ctx.Build(pctx, android.BuildParams{
		Rule:        buildAAR,
		Description: "aar",
		Implicits:   deps,
		Output:      outputFile,
		Args: map[string]string{
			"manifest":   manifest.String(),
			"classesJar": classesJarPath,
			"rTxt":       rTxt.String(),
			"outDir":     android.PathForModuleOut(ctx, "aar").String(),
		},
	})
}

var buildBundleModule = pctx.AndroidStaticRule("buildBundleModule",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.MergeZips, ` ${out} ${in}`,
		),
	})

var bundleMungePackage = pctx.AndroidStaticRule("bundleMungePackage",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Zip2zip, ` -i ${in} -o ${out} AndroidManifest.xml:manifest/AndroidManifest.xml resources.pb "res/**/*" "assets/**/*"`,
		),
	})

var bundleMungeDexJar = pctx.AndroidStaticRule("bundleMungeDexJar",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			android.Zip2zip, ` -i ${in} -o ${out} "classes*.dex:dex/" && `,
			android.Zip2zip, ` -i ${in} -o ${resJar} -x "classes*.dex" "**/*:root/"`,
		),
	}, "resJar")

// Builds an app into a module suitable for input to bundletool
func BuildBundleModule(ctx android.ModuleContext, outputFile android.WritablePath,
	packageFile, jniJarFile, dexJarFile android.Path) {

	protoResJarFile := android.PathForModuleOut(ctx, "package-res.pb.apk")
	aapt2Convert(ctx, protoResJarFile, packageFile, "proto")

	var zips android.Paths

	mungedPackage := android.PathForModuleOut(ctx, "bundle", "apk.zip")
	ctx.Build(pctx, android.BuildParams{
		Rule:        bundleMungePackage,
		Input:       protoResJarFile,
		Output:      mungedPackage,
		Description: "bundle apk",
	})
	zips = append(zips, mungedPackage)

	if dexJarFile != nil {
		mungedDexJar := android.PathForModuleOut(ctx, "bundle", "dex.zip")
		mungedResJar := android.PathForModuleOut(ctx, "bundle", "res.zip")
		ctx.Build(pctx, android.BuildParams{
			Rule:           bundleMungeDexJar,
			Input:          dexJarFile,
			Output:         mungedDexJar,
			ImplicitOutput: mungedResJar,
			Description:    "bundle dex",
			Args: map[string]string{
				"resJar": mungedResJar.String(),
			},
		})
		zips = append(zips, mungedDexJar, mungedResJar)
	}
	if jniJarFile != nil {
		zips = append(zips, jniJarFile)
	}

	ctx.Build(pctx, android.BuildParams{
		Rule:        buildBundleModule,
		Inputs:      zips,
		Output:      outputFile,
		Description: "bundle",
	})
}

func TransformJniLibsToJar(
	ctx android.ModuleContext,
	outputFile android.WritablePath,
	jniLibs []jniLib,
	prebuiltJniPackages android.Paths,
	uncompressJNI bool) {

	var deps android.Paths
	jarArgs := []string{
		"-j", // junk paths, they will be added back with -P arguments
	}

	if uncompressJNI {
		jarArgs = append(jarArgs, "-L", "0")
	}

	for _, j := range jniLibs {
		deps = append(deps, j.path)
		jarArgs = append(jarArgs,
			"-P", targetToJniDir(j.target),
			"-f", j.path.String())
	}

	rule := zip
	args := map[string]string{
		"jarArgs": strings.Join(proptools.NinjaAndShellEscapeList(jarArgs), " "),
	}
	if ctx.Config().UseREWrapper() && ctx.Config().IsEnvTrue("RBE_ZIP") {
		rule = zipRE
		args["implicits"] = strings.Join(deps.Strings(), ",")
	}
	var jniJarPath android.WritablePath = android.PathForModuleOut(ctx, "jniJarOutput.zip")
	if len(prebuiltJniPackages) == 0 {
		jniJarPath = outputFile
	}
	ctx.Build(pctx, android.BuildParams{
		Rule:        rule,
		Description: "zip jni libs",
		Output:      jniJarPath,
		Implicits:   deps,
		Args:        args,
	})
	if len(prebuiltJniPackages) > 0 {
		var mergeJniJarPath android.WritablePath = android.PathForModuleOut(ctx, "mergeJniJarOutput.zip")
		if !uncompressJNI {
			mergeJniJarPath = outputFile
		}
		ctx.Build(pctx, android.BuildParams{
			Rule:        mergeAssetsRule,
			Description: "merge prebuilt JNI packages",
			Inputs:      append(prebuiltJniPackages, jniJarPath),
			Output:      mergeJniJarPath,
		})

		if uncompressJNI {
			ctx.Build(pctx, android.BuildParams{
				Rule:   uncompressEmbeddedJniLibsRule,
				Input:  mergeJniJarPath,
				Output: outputFile,
			})
		}
	}
}

func (a *AndroidApp) generateJavaUsedByApex(ctx android.ModuleContext) {
	// Skip generating the XML file for unbundled test apps that don't have the tools
	if ctx.Config().UnbundledBuild() && !slices.Contains(ctx.Config().UnbundledBuildApps(), a.Name()) {
		return
	}
	javaApiUsedByOutputFile := android.PathForModuleOut(ctx, a.installApkName+"_using.xml")
	javaUsedByRule := android.NewRuleBuilder(pctx, ctx).SandboxDisabled()
	javaUsedByRule.Command().
		BuiltTool("gen_apex_symbols").
		Text("java_usedby").
		BuiltTool("dexdeps").
		Output(javaApiUsedByOutputFile).
		Input(a.Library.Module.outputFile)
	javaUsedByRule.Build("java_usedby_list", "Generate Java APIs used by Apex")

	if slices.Contains(ctx.Config().UnbundledBuildApps(), a.Name()) && !android.ShouldSkipAndroidMkProcessing(ctx, a) {
		ctx.DistForGoalsWithFilename([]string{a.installApkName, "apps_only"}, javaApiUsedByOutputFile, "java_apis_used_by_apex/"+javaApiUsedByOutputFile.Base())
	}
}

func targetToJniDir(target android.Target) string {
	return filepath.Join("lib", target.Arch.Abi[0])
}
