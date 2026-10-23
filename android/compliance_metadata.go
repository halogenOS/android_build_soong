// Copyright 2024 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package android

import (
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

//go:generate go run ../../blueprint/gobtools/codegen

// A complianceColumn is a column of the modules table.
type complianceColumn interface {
	columnName() string
}

// A ComplianceTextColumn is a column of the compliance metadata of a module that contains one text.
type ComplianceTextColumn struct{ name string }

// A ComplianceListColumn is a column of the compliance metadata of a module that contains a list of
// texts, such as the names of modules.
type ComplianceListColumn struct{ name string }

// A CompliancePathColumn is a column of the compliance metadata of a module that contains a list of
// paths.
type CompliancePathColumn struct{ name string }

func (c ComplianceTextColumn) columnName() string { return c.name }
func (c ComplianceListColumn) columnName() string { return c.name }
func (c CompliancePathColumn) columnName() string { return c.name }

var (
	// The columns of the compliance metadata of modules, in the order of the columns of the modules
	// table after id.
	ComplianceMetadataProp = struct {
		NAME             ComplianceTextColumn
		PACKAGE          ComplianceTextColumn
		MODULE_TYPE      ComplianceTextColumn
		BASE_MODULE_TYPE ComplianceTextColumn
		OS               ComplianceTextColumn
		ARCH             ComplianceTextColumn
		VARIANT          ComplianceTextColumn
		IS_STATIC_LIB    ComplianceTextColumn
		IS_PRIMARY_ARCH  ComplianceTextColumn
		INSTALLED_FILES  CompliancePathColumn
		BUILT_FILES      CompliancePathColumn
		// Module names of static dependencies
		STATIC_DEPS      ComplianceListColumn
		STATIC_DEP_FILES CompliancePathColumn
		// Module names of whole static dependencies
		WHOLE_STATIC_DEPS      ComplianceListColumn
		WHOLE_STATIC_DEP_FILES CompliancePathColumn
		// Module names of header libs
		HEADER_LIBS ComplianceListColumn
		LICENSES    ComplianceListColumn

		// module_type=package
		PKG_DEFAULT_APPLICABLE_LICENSES ComplianceListColumn

		// module_type=license
		LIC_LICENSE_KINDS ComplianceListColumn
		LIC_LICENSE_TEXT  CompliancePathColumn
		LIC_PACKAGE_NAME  ComplianceTextColumn

		// module_type=license_kind
		LK_CONDITIONS ComplianceListColumn
		LK_URL        ComplianceTextColumn

		// module_type=cipd_package
		CIPD_VERSION ComplianceTextColumn

		// module_type=prebuilt modules
		CIPD_SRC          CompliancePathColumn
		PREBUILT_SRC_FILE CompliancePathColumn
	}{
		ComplianceTextColumn{"name"},
		ComplianceTextColumn{"package"},
		ComplianceTextColumn{"module_type"},
		ComplianceTextColumn{"base_module_type"},
		ComplianceTextColumn{"os"},
		ComplianceTextColumn{"arch"},
		ComplianceTextColumn{"variant"},
		ComplianceTextColumn{"is_static_lib"},
		ComplianceTextColumn{"is_primary_arch"},
		CompliancePathColumn{"installed_files"},
		CompliancePathColumn{"built_files"},
		ComplianceListColumn{"static_deps"},
		CompliancePathColumn{"static_dep_files"},
		ComplianceListColumn{"whole_static_deps"},
		CompliancePathColumn{"whole_static_dep_files"},
		ComplianceListColumn{"header_libs"},
		ComplianceListColumn{"licenses"},

		ComplianceListColumn{"pkg_default_applicable_licenses"},

		ComplianceListColumn{"lic_license_kinds"},
		CompliancePathColumn{"lic_license_text"},
		ComplianceTextColumn{"lic_package_name"},

		ComplianceListColumn{"lk_conditions"},
		ComplianceTextColumn{"lk_url"},

		ComplianceTextColumn{"cipd_version"},

		CompliancePathColumn{"cipd_src"},
		CompliancePathColumn{"prebuilt_src_file"},
	}

	// The names of the columns of the modules table after id, in table order.
	complianceModuleColumns = complianceColumnNames(ComplianceMetadataProp)
)

// complianceColumnNames returns the names of the columns that are the fields of columns, in the
// order of the fields.
func complianceColumnNames(columns any) []string {
	fields := reflect.ValueOf(columns)
	names := make([]string, fields.NumField())
	for i := range names {
		names[i] = fields.Field(i).Interface().(complianceColumn).columnName()
	}
	return names
}

// ComplianceMetadataInfo collects the compliance metadata of a module while it generates its build
// actions: the columns of its row in the modules table, and the files that the modules containing
// it read. The row is written into the records of the modules table, and only the files go into
// the CommonModuleInfo of the module.
type ComplianceMetadataInfo struct {
	ComplianceFilesInfo

	// texts contains the values of the text and list columns, and paths the paths of the path
	// columns, which become relative to their root when the row is written.
	texts map[complianceColumn]complianceValue
	paths map[CompliancePathColumn]Paths
}

// ComplianceFilesInfo contains the files of a module that the modules containing it read for their
// compliance metadata, such as the files of a partition that its device reads.
// @auto-generate: gob
type ComplianceFilesInfo struct {
	filesContained         []ComplianceFile
	prebuiltFilesCopied    []CompliancePrebuiltCopy
	platformGeneratedFiles Paths
	productCopyFiles       []ComplianceProductCopy
	kernelModuleCopyFiles  []ComplianceKernelModuleCopy
}

// A ComplianceFile is a file that a module contains: a path of the build, or the path of a file
// inside an image, such as a file inside an APEX, which has no root. ComplianceFileOfPath and
// ComplianceFileInImage make one.
// @auto-generate: gob
type ComplianceFile struct {
	path    Path
	inImage string
}

func ComplianceFileOfPath(path Path) ComplianceFile {
	return ComplianceFile{path: path}
}

func ComplianceFileInImage(path string) ComplianceFile {
	return ComplianceFile{inImage: path}
}

// String returns the String of the path, or the path inside the image.
func (f ComplianceFile) String() string {
	if f.path == nil {
		return f.inImage
	}
	return f.path.String()
}

// value returns the file as the value of a column: the path below its root, or the path inside
// the image as a text.
func (f ComplianceFile) value(config Config) complianceValue {
	if f.path == nil {
		return complianceText(f.inImage)
	}
	return compliancePathsOf(config, Paths{f.path})
}

// A CompliancePrebuiltCopy is a prebuilt file that a module copies into the product directory, to
// Dest below the product directory.
// @auto-generate: gob
type CompliancePrebuiltCopy struct {
	Src  Path
	Dest string
}

// A ComplianceProductCopy is a file of PRODUCT_COPY_FILES, installed from Src to Dest.
// @auto-generate: gob
type ComplianceProductCopy struct {
	Src  Path
	Dest InstallPath
}

// A ComplianceKernelModuleCopy is a kernel module, installed from Src to Dest, which are the texts
// of PrebuiltKernelModulesComplianceMetadata.
// @auto-generate: gob
type ComplianceKernelModuleCopy struct {
	Src  string
	Dest string
}

func NewComplianceMetadataInfo() *ComplianceMetadataInfo {
	return &ComplianceMetadataInfo{
		texts: map[complianceColumn]complianceValue{},
		paths: map[CompliancePathColumn]Paths{},
	}
}

func (c *ComplianceMetadataInfo) SetStringValue(column ComplianceTextColumn, value string) {
	c.texts[column] = complianceText(value)
}

func (c *ComplianceMetadataInfo) SetListValue(column ComplianceListColumn, values []string) {
	c.texts[column] = nonEmptyTexts(values)
}

// SetPaths sets column to paths. A path that is in the list twice is written once.
func (c *ComplianceMetadataInfo) SetPaths(column CompliancePathColumn, paths Paths) {
	c.paths[column] = slices.Clone(paths)
}

func (c *ComplianceFilesInfo) SetFilesContained(files []ComplianceFile) {
	c.filesContained = files
}

func (c *ComplianceFilesInfo) GetFilesContained() []ComplianceFile {
	return c.filesContained
}

func (c *ComplianceFilesInfo) SetPrebuiltFilesCopied(files []CompliancePrebuiltCopy) {
	c.prebuiltFilesCopied = files
}

func (c *ComplianceFilesInfo) GetPrebuiltFilesCopied() []CompliancePrebuiltCopy {
	return c.prebuiltFilesCopied
}

func (c *ComplianceFilesInfo) SetPlatformGeneratedFiles(files Paths) {
	c.platformGeneratedFiles = files
}

func (c *ComplianceFilesInfo) GetPlatformGeneratedFiles() Paths {
	return c.platformGeneratedFiles
}

func (c *ComplianceFilesInfo) SetProductCopyFiles(files []ComplianceProductCopy) {
	c.productCopyFiles = files
}

func (c *ComplianceFilesInfo) GetProductCopyFiles() []ComplianceProductCopy {
	return c.productCopyFiles
}

func (c *ComplianceFilesInfo) SetKernelModuleCopyFiles(files []ComplianceKernelModuleCopy) {
	c.kernelModuleCopyFiles = files
}

func (c *ComplianceFilesInfo) GetKernelModuleCopyFiles() []ComplianceKernelModuleCopy {
	return c.kernelModuleCopyFiles
}

func (c *ComplianceMetadataInfo) AddBuiltFiles(files ...Path) {
	builtFiles := ComplianceMetadataProp.BUILT_FILES
	c.SetPaths(builtFiles, SortedUniquePaths(slices.Concat(c.paths[builtFiles], files)))
}

func (c *ComplianceMetadataInfo) SetPrebuiltSrc(ctx ModuleContext, src string) {
	if module, tag := SrcIsModuleWithTag(src); module != "" {
		m := GetModuleProxyFromPathDep(ctx, module, tag)
		baseModuleType := OtherModuleProviderOrDefault(ctx, m, CommonModuleInfoProvider).BaseModuleType
		if ctx.OtherModuleType(m) == "cipd_package" || baseModuleType == "cipd_package" ||
			ctx.OtherModuleType(m) == "filegroup" || baseModuleType == "filegroup" {
			paths, err := expandOneSrcPath(sourcePathInput{context: ctx, path: src, includeDirs: true})
			if err != nil {
				panic(err)
			}
			for _, p := range paths {
				if root, _ := rootAndRel(ctx.Config(), p); root != pathRootSrc {
					c.SetPaths(ComplianceMetadataProp.CIPD_SRC, Paths{p})
				} else {
					c.SetPaths(ComplianceMetadataProp.PREBUILT_SRC_FILE, Paths{p})
				}
			}
		}
	} else {
		paths, err := expandOneSrcPath(sourcePathInput{context: ctx, path: src, includeDirs: true})
		if err != nil {
			panic(err)
		}
		for _, p := range paths {
			c.SetPaths(ComplianceMetadataProp.PREBUILT_SRC_FILE, Paths{p})
		}
	}
}

// moduleRow returns the row of the module in the modules table.
func (c *ComplianceMetadataInfo) moduleRow(config Config) complianceRow {
	row := complianceRow{}
	for column, value := range c.texts {
		row.set(column, value)
	}
	for column, paths := range c.paths {
		row.set(column, compliancePathsOf(config, paths))
	}
	return row
}

// buildComplianceMetadataProvider starts with the ModuleContext.ComplianceMetadataInfo() and fills in more common metadata
// for different module types without accessing their private fields but through android.Module interface
// and public/private fields of package android. The final files are stored to a module's CommonModuleInfo.
func buildComplianceMetadataProvider(ctx *moduleContext, m *ModuleBase) *ComplianceMetadataInfo {
	complianceMetadataInfo := ctx.ComplianceMetadataInfo()
	complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.NAME, m.Name())
	complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.PACKAGE, ctx.ModuleDir())
	complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.MODULE_TYPE, ctx.ModuleType())

	if baseTypePtr := m.baseProperties.Soong_config_base_module_type; baseTypePtr != nil {
		if baseType := *baseTypePtr; baseType != "" {
			complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.BASE_MODULE_TYPE, baseType)
		}
	}

	switch ctx.ModuleType() {
	case "license":
		licenseModule := m.module.(*licenseModule)
		complianceMetadataInfo.SetListValue(ComplianceMetadataProp.LIC_LICENSE_KINDS, licenseModule.properties.License_kinds)
		complianceMetadataInfo.SetPaths(ComplianceMetadataProp.LIC_LICENSE_TEXT, PathsForModuleSrc(ctx, licenseModule.properties.License_text.GetOrDefault(ctx, nil)))
		complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.LIC_PACKAGE_NAME, String(licenseModule.properties.Package_name))
	case "license_kind":
		licenseKindModule := m.module.(*licenseKindModule)
		complianceMetadataInfo.SetListValue(ComplianceMetadataProp.LK_CONDITIONS, licenseKindModule.properties.Conditions)
		complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.LK_URL, licenseKindModule.properties.Url)
	default:
		complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.OS, ctx.Os().String())
		complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.ARCH, ctx.Arch().String())
		complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.IS_PRIMARY_ARCH, strconv.FormatBool(ctx.PrimaryArch()))
		complianceMetadataInfo.SetStringValue(ComplianceMetadataProp.VARIANT, ctx.ModuleSubDir())
		if m.primaryLicensesProperty != nil && m.primaryLicensesProperty.getName() == "licenses" {
			complianceMetadataInfo.SetListValue(ComplianceMetadataProp.LICENSES, m.primaryLicensesProperty.getStrings())
		}

		var installed InstallPaths
		installed = append(installed, ctx.installFiles...)
		installed = append(installed, ctx.katiInstalls.InstallPaths()...)
		installed = append(installed, ctx.katiSymlinks.InstallPaths()...)
		installed = append(installed, ctx.katiInitRcInstalls.InstallPaths()...)
		installed = append(installed, ctx.katiVintfInstalls.InstallPaths()...)
		// The following module types use PackageFiles instead of InstallFiles so here we need to
		// collect the fullInstallPaths from the packagingSpecs.
		// TODO: b/409854522
		if strings.HasPrefix(ctx.ModuleType(), "sdk_library_internal") ||
			ctx.ModuleType() == "bpf" ||
			ctx.ModuleType() == "libbpf_prog" ||
			ctx.ModuleType() == "avbpubkey__loadHookModule" ||
			(ctx.ModuleType() == "prebuilt_etc" &&
				slices.Contains([]string{"preloaded-classes", "public.libraries.android.txt"}, ctx.ModuleName())) ||
			(ctx.ModuleType() == "prebuilt_root" && ctx.ModuleName() == "init.environ.rc-soong") {
			for _, s := range ctx.packagingSpecs {
				installed = append(installed, s.fullInstallPath)
			}
		}
		complianceMetadataInfo.SetPaths(ComplianceMetadataProp.INSTALLED_FILES, installed.Paths())

		// BUILT_FILES
		builtFiles := slices.Clone(ctx.GetOutputFiles().DefaultOutputFiles)
		for _, paths := range ctx.GetOutputFiles().TaggedOutputFiles {
			builtFiles = append(builtFiles, paths...)
		}
		ctx.ComplianceMetadataInfo().AddBuiltFiles(builtFiles...)
	}
	return complianceMetadataInfo
}

func init() {
	RegisterComplianceMetadataSingleton(InitRegistrationContext)
}

func RegisterComplianceMetadataSingleton(ctx RegistrationContext) {
	ctx.RegisterParallelSingletonType("compliance_metadata_singleton", complianceMetadataSingletonFactory)
}

var (
	complianceMetadataImport = pctx.HostBinToolVariable("complianceMetadataImport", "compliance_metadata_import")

	// Command to import the records of the compliance metadata into a sqlite3 database, one table
	// for each file.
	importComplianceMetadata = pctx.AndroidStaticRule("importComplianceMetadata",
		blueprint.RuleParams{
			Command:         `${complianceMetadataImport} --output $out ${roots} @$out.rsp`,
			CommandDeps:     []string{"${complianceMetadataImport}"},
			Rspfile:         `$out.rsp`,
			RspfileContent:  `$in`,
			SandboxDisabled: true,
		}, "roots")
)

func complianceMetadataSingletonFactory() Singleton {
	return &complianceMetadataSingleton{}
}

type complianceMetadataSingleton struct {
}

// GetComplianceFiles returns the files that module provides for the compliance metadata of the
// modules containing it.
func GetComplianceFiles(ctx OtherModuleProviderContext, module ModuleProxy) *ComplianceFilesInfo {
	if commInfo, ok := OtherModuleProvider(ctx, module, CommonModuleInfoProvider); ok {
		return commInfo.ComplianceFiles
	}
	return nil
}

// complianceMetadataDb returns the compliance metadata database of the regular build.
func complianceMetadataDb(ctx PathContext) OutputPath {
	return PathForOutput(ctx, "compliance-metadata", ctx.Config().DeviceProduct(), "compliance-metadata.db")
}

// A complianceRootDir is the directory of a root of the paths in the records, in one run.
type complianceRootDir struct {
	root pathRoot
	dir  string
}

// complianceRootArgs returns the arguments of the importer that set the directories of the roots
// of the paths in the records, each one shell-escaped.
func complianceRootArgs(dirs ...complianceRootDir) string {
	args := make([]string, len(dirs))
	for i, dir := range dirs {
		args[i] = "--root " + proptools.ShellEscape(dir.root.name+"="+dir.dir)
	}
	return strings.Join(args, " ")
}

// buildComplianceMetadataImport builds the step that imports records into db.
func buildComplianceMetadataImport(ctx SingletonContext, records Paths, db WritablePath) {
	ctx.Build(pctx, BuildParams{
		Rule:   importComplianceMetadata,
		Inputs: records,
		Output: db,
		Args: map[string]string{"roots": complianceRootArgs(
			complianceRootDir{root: pathRootOut, dir: ctx.Config().OutDir()},
			// An empty directory leaves the source paths relative to the top of the tree.
			complianceRootDir{root: pathRootSrc, dir: ""},
		)},
	})
}

// Import the records of the compliance metadata from Make and Soong to a sqlite3 database.
func (c *complianceMetadataSingleton) GenerateBuildActions(ctx SingletonContext) {
	config := ctx.Config()
	if !config.HasDeviceProduct() {
		return
	}
	var records Paths
	for _, table := range complianceTables(config) {
		records = append(records, complianceTablePath(config, table.name))
		if table.ofMake && table.writtenBySoong(config) {
			// registerComplianceRecordStreams read the header of Make.
			ctx.AddNinjaFileDeps(makeComplianceHeaderPath(table.name))
		}
	}
	db := complianceMetadataDb(ctx)
	buildComplianceMetadataImport(ctx, records, db)

	// Phony rule "compliance-metadata.db". "m compliance-metadata.db" to create the compliance metadata database.
	ctx.Build(pctx, BuildParams{
		Rule:   blueprint.Phony,
		Inputs: []Path{db},
		Output: PathForPhony(ctx, "compliance-metadata.db"),
	})
}
