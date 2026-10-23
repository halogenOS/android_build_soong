// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
)

// readComplianceRows returns the lines of the records of table after the header, sorted.
func readComplianceRows(t *testing.T, result *TestResult, table string) []string {
	t.Helper()
	file, err := result.Config.fs.Open(complianceTablePath(result.Config, table).String())
	if err != nil {
		t.Fatalf("could not open the records of %s: %s", table, err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("could not read the records of %s: %s", table, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	rows := lines[1:]
	slices.Sort(rows)
	return rows
}

// complianceModuleTestRow is the part of a row of the modules table that the tests check.
type complianceModuleTestRow struct {
	Name           string              `json:"name"`
	ModuleType     string              `json:"module_type"`
	InstalledFiles map[string][]string `json:"installed_files"`
}

// complianceFilesTestModule runs generate while it generates its build actions.
type complianceFilesTestModule struct {
	ModuleBase
	generate func(ctx ModuleContext)
}

func (m *complianceFilesTestModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	m.generate(ctx)
}

// prepareForComplianceFilesTest registers the module type compliance_files_test, whose modules run
// generate.
func prepareForComplianceFilesTest(generate func(ctx ModuleContext)) FixturePreparer {
	return FixtureRegisterWithContext(func(ctx RegistrationContext) {
		ctx.RegisterModuleType("compliance_files_test", func() Module {
			m := &complianceFilesTestModule{generate: generate}
			InitAndroidArchModule(m, DeviceSupported, MultilibCommon)
			return m
		})
	})
}

func TestComplianceRecords(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForComplianceFilesTest(func(ctx ModuleContext) {
			out := PathForModuleOut(ctx, ctx.ModuleName()+".txt")
			ctx.Build(pctx, BuildParams{Rule: CpRule, Input: PathForModuleSrc(ctx, "src.txt"), Output: out})
			ctx.InstallFile(PathForModuleInstall(ctx, "etc"), out.Base(), out)
		}),
		FixtureRegisterWithContext(RegisterComplianceMetadataSingleton),
		PrepareForTestWithArchMutator,
		PrepareForTestWithPackageModule,
		FixtureAddTextFile("src.txt", ""),
	).RunTestWithBp(t, `
	package {}
	compliance_files_test {
		name: "plain",
	}
	`)

	rows := map[string]complianceModuleTestRow{}
	for _, line := range readComplianceRows(t, result, modulesComplianceTable) {
		var row complianceModuleTestRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("could not read the row %q: %s", line, err)
		}
		rows[row.Name] = row
	}
	AssertDeepEquals(t, "installed files",
		map[string][]string{"out": {"target/product/test_device/system/etc/plain.txt"}},
		rows["plain"].InstalledFiles)
	AssertDeepEquals(t, "row of the package",
		complianceModuleTestRow{Name: "//.", ModuleType: "package"},
		rows["//."])

	AssertDeepEquals(t, "installed files of Make without a main device", []string{},
		readComplianceRows(t, result, makeMetadataComplianceTable))

	db := result.SingletonForTests(t, "compliance_metadata_singleton").Rule("importComplianceMetadata")
	AssertPathsRelativeToTopEquals(t, "records", []string{
		"out/soong/compliance-metadata/test_product/modules.jsonl",
		"out/soong/compliance-metadata/test_product/make_metadata.jsonl",
		"out/soong/compliance-metadata/test_product/make_modules.jsonl",
	}, db.Inputs)
}

func TestProductComplianceRecords(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForComplianceFilesTest(func(ctx ModuleContext) {
			install := func(name string) InstallPath { return PathForModuleInstall(ctx, "etc", name) }
			files := ComplianceFilesInfo{}
			files.SetFilesContained([]ComplianceFile{
				ComplianceFileOfPath(install("module.txt")),
				ComplianceFileOfPath(install("generated.txt")),
				ComplianceFileOfPath(install("kernel_module.ko")),
				ComplianceFileOfPath(install("copied.txt")),
			})
			files.SetPlatformGeneratedFiles(Paths{install("generated.txt")})
			files.SetKernelModuleCopyFiles([]ComplianceKernelModuleCopy{
				{Src: "ANDROID-GEN", Dest: install("kernel_module.ko").String()},
			})
			files.SetProductCopyFiles([]ComplianceProductCopy{
				{Src: PathForSource(ctx, "device/copied.txt"), Dest: install("copied.txt")},
			})
			files.SetPrebuiltFilesCopied([]CompliancePrebuiltCopy{
				{Src: PathForSource(ctx, "device/kernel"), Dest: "kernel"},
			})
			WriteProductComplianceRecords(ctx, &files)
		}),
		PrepareForTestWithArchMutator,
	).RunTestWithBp(t, `
	compliance_files_test {
		name: "device",
	}
	`)

	installed := `{"installed_file":{"out":["target/product/test_device/system/etc/`
	AssertDeepEquals(t, "installed files of the product", []string{
		`{"installed_file":{"out":["target/product/test_device/kernel"]},"product_copy_files":"device/kernel:kernel"}`,
		installed + `copied.txt"]},"product_copy_files":"device/copied.txt:out/target/product/test_device/system/etc/copied.txt"}`,
		installed + `generated.txt"]},"is_platform_generated":"Y","license_text":{"src":["build/soong/licenses/LICENSE"]}}`,
		installed + `kernel_module.ko"]},"kernel_module_copy_files":"ANDROID-GEN::out/target/product/test_device/system/etc/kernel_module.ko"}`,
		installed + `module.txt"]},"is_soong_module":"Y"}`,
	}, StringsRelativeToTop(result.Config, readComplianceRows(t, result, makeMetadataComplianceTable)))
}

func TestUnbundledAppComplianceRecords(t *testing.T) {
	result := GroupFixturePreparers(
		prepareForComplianceFilesTest(func(ctx ModuleContext) {
			SetUnbundledAppFiles(ctx, ctx.ModuleName(), []UnbundledAppFile{
				{
					Installed:   ComplianceFileOfPath(PathForModuleInstall(ctx, "app", "app.apk")),
					BuildOutput: PathForModuleOut(ctx, "app.apk"),
				},
				{
					Installed:   ComplianceFileInImage("lib64/a.so"),
					BuildOutput: PathForModuleOut(ctx, "image", "lib64", "a.so"),
				},
			})
		}),
		PrepareForTestWithArchMutator,
		FixtureModifyProductVariables(func(variables FixtureProductVariables) {
			variables.Unbundled_build_apps = []string{"app"}
		}),
	).RunTestWithBp(t, `
	compliance_files_test {
		name: "app",
	}
	`)

	AssertDeepEquals(t, "files of the app", []string{
		`{"installed_file":"lib64/a.so","build_output_path":{"out":["soong/.intermediates/app/android_common/image/lib64/a.so"]}}`,
		`{"installed_file":{"out":["target/product/test_device/system/app/app.apk"]},"build_output_path":{"out":["soong/.intermediates/app/android_common/app.apk"]}}`,
	}, readComplianceRows(t, result, "app"))
}

func TestCompliancePathsOf(t *testing.T) {
	ctx := PathContextForTesting(pathTestConfig("/o"))
	output := PathForOutput(ctx, "a.txt")
	paths := compliancePathsOf(ctx.Config(), Paths{
		output,
		output.WithoutRel(),
		PathForSource(ctx, "c.txt"),
	})
	AssertDeepEquals(t, "paths below their roots", compliancePaths{
		pathRootOut: {"soong/a.txt"},
		pathRootSrc: {"c.txt"},
	}, paths)
}
