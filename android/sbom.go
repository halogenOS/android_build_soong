// Copyright 2024 Google Inc. All rights reserved.
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

package android

import (
	"fmt"
	"path/filepath"

	"github.com/google/blueprint"
)

var (
	// Command line tool to generate SBOM in Soong
	genSbom = pctx.HostBinToolVariable("genSbom", "gen_sbom")

	// Command to generate SBOM in Soong. productOut and soongOut are relative to outDir.
	// installedFiles and unbundledModule select what the SBOM describes, as sbomScope sets them.
	genSbomRule = pctx.AndroidStaticRule("genSbomRule", blueprint.RuleParams{
		Command:         "rm -rf $out && ${genSbom} --output_file ${out} --metadata ${in} --out_dir ${outDir} --product_out ${productOut} --soong_out ${soongOut} --build_version \"$$(cat ${buildFingerprintFile})\" --product_mfr \"${productManufacturer}\" --json ${installedFiles} ${unbundledModule}",
		CommandDeps:     []string{"${genSbom}"},
		SandboxDisabled: true,
	}, "outDir", "productOut", "soongOut", "buildFingerprintFile", "productManufacturer", "installedFiles", "unbundledModule")
)

// installedFilesList returns the list of the installed files of the product that Make writes for
// gen_sbom.
func installedFilesList(ctx PathContext) OutputPath {
	return PathForOutput(ctx, "compliance-metadata", ctx.Config().DeviceProduct(), "installed_files.txt")
}

// sbomScope selects what an SBOM describes, as the values of the installedFiles and
// unbundledModule variables of genSbomRule. Each value is empty, or an argument of gen_sbom with
// its value. The zero value describes the product, with each installed file at the path that the
// metadata DB records.
type sbomScope struct {
	installedFiles  string
	unbundledModule string
}

// productInstalledFiles returns the file that depends on all installed files of the product, and
// the scope of the SBOM of the product. When Kati runs, Make writes the list of the installed
// files, and gen_sbom hashes each file at its path in the list. Otherwise the main device writes
// installed_files.stamp.
func productInstalledFiles(ctx PathContext) (Path, sbomScope) {
	if !ctx.Config().KatiEnabled() {
		return PathForOutput(ctx, "compliance-metadata", ctx.Config().DeviceProduct(), "installed_files.stamp"), sbomScope{}
	}
	list := installedFilesList(ctx)
	return list, sbomScope{installedFiles: "--installed_files " + list.String()}
}

// sbomArgs returns the arguments of genSbomRule for an SBOM of scope.
func sbomArgs(ctx PathContext, scope sbomScope) map[string]string {
	config := ctx.Config()
	soongOut, err := filepath.Rel(config.OutDir(), config.soongOutDir)
	if err != nil {
		panic(fmt.Errorf("could not find the Soong output directory %s in %s: %w", config.soongOutDir, config.OutDir(), err))
	}
	return map[string]string{
		"outDir":               config.OutDir(),
		"productOut":           filepath.Join("target", "product", String(config.productVariables.DeviceName)),
		"soongOut":             soongOut,
		"buildFingerprintFile": config.BuildFingerprintFile(ctx).String(),
		"productManufacturer":  config.ProductVariables().ProductManufacturer,
		"installedFiles":       scope.installedFiles,
		"unbundledModule":      scope.unbundledModule,
	}
}

func init() {
	RegisterSbomSingleton(InitRegistrationContext)
}

func RegisterSbomSingleton(ctx RegistrationContext) {
	ctx.RegisterParallelSingletonType("sbom_singleton", sbomSingletonFactory)
}

// sbomSingleton is used to generate build actions of generating SBOM of products.
type sbomSingleton struct {
}

func sbomSingletonFactory() Singleton {
	return &sbomSingleton{}
}

// MakeVars exports the path of the list of installed files that Make writes.
func (this *sbomSingleton) MakeVars(ctx MakeVarsContext) {
	if ctx.Config().HasDeviceProduct() {
		ctx.Strict("SOONG_SBOM_INSTALLED_FILES_LIST", installedFilesList(ctx).String())
	}
}

// Generates SBOM of products
func (this *sbomSingleton) GenerateBuildActions(ctx SingletonContext) {
	if !ctx.Config().HasDeviceProduct() {
		return
	}
	if ctx.Config().HasUnbundledBuildApps() {
		// In unbundled builds, the unbundled_builder module will find what modules to build
		// and then call BuildUnbundledSbom() on each of them, so this singleton doesn't need
		// to do anything.

		// Still create an sbom phony just so the phony exists even if there are no sbom-generating
		// unbundled apps.
		ctx.Phony("sbom")
	} else {
		// When building SBOM of products, phony rule "sbom" is for generating product SBOM in Soong.
		installedFiles, scope := productInstalledFiles(ctx)
		sbomFile := PathForOutput(ctx, "sbom", ctx.Config().DeviceProduct(), "sbom.spdx.json")
		ctx.Build(pctx, BuildParams{
			Rule:      genSbomRule,
			Input:     complianceMetadataDb(ctx),
			Implicits: []Path{ctx.Config().BuildFingerprintFile(ctx), installedFiles},
			Output:    sbomFile,
			Args:      sbomArgs(ctx, scope),
		})

		ctx.Build(pctx, BuildParams{
			Rule:   blueprint.Phony,
			Inputs: []Path{sbomFile},
			Output: PathForPhony(ctx, "sbom"),
		})
		ctx.DistForGoalWithFilename("droid", sbomFile, "sbom/sbom.spdx.json")
	}
}

func BuildUnbundledSbom(ctx ModuleContext, module ModuleProxy) {
	if metadataInfo := GetComplianceFiles(ctx, module); metadataInfo != nil && len(metadataInfo.filesContained) > 0 {
		installedFile := metadataInfo.filesContained[0].path
		sbomFile := PathForOutput(ctx, "sbom", ctx.Config().DeviceProduct(), module.Name(), installedFile.Base()+".spdx.json")
		ctx.Build(pctx, BuildParams{
			Rule:      genSbomRule,
			Input:     complianceMetadataDb(ctx),
			Implicits: []Path{ctx.Config().BuildFingerprintFile(ctx), installedFile},
			Output:    sbomFile,
			Args:      sbomArgs(ctx, sbomScope{unbundledModule: "--unbundled_module " + module.Name()}),
		})
		ctx.Phony("sbom", sbomFile)
		ctx.DistForGoalsWithFilename([]string{"apps_only", "sbom"}, sbomFile, "sbom/"+sbomFile.Base())
	}
}
