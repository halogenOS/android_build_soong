// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/blueprint"
)

// The records of the compliance metadata are files of JSON lines, one file for each table of the
// compliance metadata database. The first line of a file is the header of its table, and each
// other line is one row, written by a module while it generates its build actions. A path in a row
// is written relative to its root, so the file does not depend on where the tree and OUT_DIR are.

// The tables whose files soong_build or Kati write.
const (
	modulesComplianceTable      = "modules"
	makeMetadataComplianceTable = "make_metadata"
	makeModulesComplianceTable  = "make_modules"
)

// A complianceTableHeader is the first line of the file of a table: its columns in table order,
// the columns that order its rows and the column that gets the position of each row.
type complianceTableHeader struct {
	Columns []string `json:"columns"`
	Sort    []string `json:"sort"`
	Number  string   `json:"number,omitempty"`
}

var (
	modulesComplianceHeader = complianceTableHeader{
		Columns: append([]string{"id"}, complianceModuleColumns...),
		Sort: []string{
			ComplianceMetadataProp.PACKAGE.name,
			ComplianceMetadataProp.NAME.name,
			ComplianceMetadataProp.VARIANT.name,
		},
		Number: "id",
	}

	// The table of an unbundled app, named after the app.
	unbundledAppComplianceHeader = complianceTableHeader{
		Columns: []string{"installed_file", "build_output_path"},
		Sort:    []string{"installed_file"},
	}
)

// A complianceTable is a table of the compliance metadata database, whose records are the file that
// complianceTablePath returns.
type complianceTable struct {
	name string
	// ofMake is true for the tables of Make, which Kati writes, and soong_build in a build without
	// Kati. Make and Soong read their header from the file that makeComplianceHeaderPath returns.
	ofMake bool
	// header is the header of a table that only soong_build writes.
	header complianceTableHeader
}

// complianceTables returns the tables of the compliance metadata database: the modules, the tables
// of Make and the table of each unbundled app.
func complianceTables(config Config) []complianceTable {
	tables := []complianceTable{
		{name: modulesComplianceTable, header: modulesComplianceHeader},
		{name: makeMetadataComplianceTable, ofMake: true},
		{name: makeModulesComplianceTable, ofMake: true},
	}
	for _, app := range FirstUniqueStrings(config.UnbundledBuildApps()) {
		tables = append(tables, complianceTable{name: app, header: unbundledAppComplianceHeader})
	}
	return tables
}

// writtenBySoong reports whether soong_build writes the records of t in the build of config.
func (t complianceTable) writtenBySoong(config Config) bool {
	return !t.ofMake || !config.KatiEnabled()
}

// headerLine returns the first line of the records of t.
func (t complianceTable) headerLine(config Config) string {
	if t.ofMake {
		return readMakeComplianceHeader(config, t.name)
	}
	return encodeComplianceLine(t.header)
}

// makeComplianceHeaderPath returns the file with the header line of table, a table of Make.
func makeComplianceHeaderPath(table string) string {
	return filepath.Join("build", "make", "core", "compliance_metadata", table+".jsonl")
}

// readMakeComplianceHeader returns the header line of table, a table of Make.
func readMakeComplianceHeader(config Config, table string) string {
	path := makeComplianceHeaderPath(table)
	file, err := config.fs.Open(path)
	if err != nil {
		panic(fmt.Errorf("could not open the header of the compliance metadata table %s: %w", table, err))
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		panic(fmt.Errorf("could not read the header of the compliance metadata table %s from %s: %w", table, path, err))
	}
	return strings.TrimSuffix(string(content), "\n")
}

// complianceTablePath returns the file of the records of table in the build of config.
func complianceTablePath(config Config, table string) OutputPath {
	return PathForOutput(NullPathContext{config}, "compliance-metadata", config.DeviceProduct(), table+".jsonl")
}

// registerComplianceRecordStreams registers the record streams of the tables that soong_build
// writes with ctx, in a build of a product.
func registerComplianceRecordStreams(ctx *Context) {
	config := ctx.Config()
	if !config.HasDeviceProduct() {
		return
	}
	for _, table := range complianceTables(config) {
		if table.writtenBySoong(config) {
			ctx.RegisterRecordStream(complianceTablePath(config, table.name).String(), table.headerLine(config))
		}
	}
}

// writeComplianceRecord writes row as one line into the records of table, in a build of a product.
// The configuration of ctx is the one that registered the record streams, also for a module that
// uses the generic configuration.
func writeComplianceRecord(ctx blueprint.ModuleContext, table string, row any) {
	config := ctx.Config().(Config)
	if !config.HasDeviceProduct() {
		return
	}
	ctx.WriteRecord(complianceTablePath(config, table).String(), encodeComplianceLine(row))
}

// A complianceValue is the value of a column in a row of the compliance metadata.
type complianceValue interface {
	isEmpty() bool
}

// complianceText is a text.
type complianceText string

// complianceTexts is a list of texts, which the importer joins with spaces.
type complianceTexts []string

// nonEmptyTexts returns the texts of values that are not empty.
func nonEmptyTexts(values []string) complianceTexts {
	return slices.DeleteFunc(slices.Clone(values), func(value string) bool { return value == "" })
}

// A pathRoot is a directory that a path in a file is relative to. The step that reads the file gets
// the directory of each root.
type pathRoot struct{ name string }

var (
	// pathRootOut is OUT_DIR.
	pathRootOut = pathRoot{"out"}
	// pathRootSrc is the top of the source tree.
	pathRootSrc = pathRoot{"src"}
)

func (r pathRoot) MarshalText() ([]byte, error) {
	return []byte(r.name), nil
}

// rootAndRel returns the root of path and the path below it. A path that a build step can write is
// below OUT_DIR, and any other path is a source path below the top of the tree.
func rootAndRel(config Config, path Path) (pathRoot, string) {
	if _, writable := path.(WritablePath); !writable {
		return pathRootSrc, path.String()
	}
	rel, ok, err := maybeRelErr(config.OutDir(), path.String())
	if err != nil || !ok {
		panic(fmt.Errorf("%q is not in the output directory %q", path.String(), config.OutDir()))
	}
	return pathRootOut, rel
}

// compliancePaths contains the paths below each root, which the importer resolves.
type compliancePaths map[pathRoot][]string

func (t complianceText) isEmpty() bool  { return t == "" }
func (t complianceTexts) isEmpty() bool { return len(t) == 0 }
func (p compliancePaths) isEmpty() bool { return len(p) == 0 }

// compliancePathsOf returns paths below their roots, each path once.
func compliancePathsOf(config Config, paths Paths) compliancePaths {
	type rootedPath struct {
		root pathRoot
		rel  string
	}
	byRoot := compliancePaths{}
	seen := make(map[rootedPath]bool, len(paths))
	for _, path := range paths {
		root, rel := rootAndRel(config, path)
		if seen[rootedPath{root, rel}] {
			continue
		}
		seen[rootedPath{root, rel}] = true
		byRoot[root] = append(byRoot[root], rel)
	}
	return byRoot
}

// A complianceRow is a row of the modules table, by the names of its columns.
type complianceRow map[string]complianceValue

// set sets column to value, and leaves an empty value out of the row.
func (r complianceRow) set(column complianceColumn, value complianceValue) {
	if !value.isEmpty() {
		r[column.columnName()] = value
	}
}

// productInstalledFileRow is a row of the make_metadata table.
type productInstalledFileRow struct {
	InstalledFile         complianceValue `json:"installed_file"`
	IsSoongModule         complianceText  `json:"is_soong_module,omitempty"`
	ProductCopyFiles      complianceText  `json:"product_copy_files,omitempty"`
	KernelModuleCopyFiles complianceText  `json:"kernel_module_copy_files,omitempty"`
	IsPlatformGenerated   complianceText  `json:"is_platform_generated,omitempty"`
	LicenseText           compliancePaths `json:"license_text,omitempty"`
}

// An UnbundledAppFile is a file that an unbundled app contains, with the file that the build
// writes for it.
type UnbundledAppFile struct {
	Installed   ComplianceFile
	BuildOutput Path
}

// unbundledAppFileRow is a row of the table of an unbundled app.
type unbundledAppFileRow struct {
	InstalledFile   complianceValue `json:"installed_file"`
	BuildOutputPath compliancePaths `json:"build_output_path"`
}

// encodeComplianceLine returns value as one line of JSON.
func encodeComplianceLine(value any) string {
	line, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("could not encode a line of the compliance metadata: %w", err))
	}
	return string(line)
}

// writeModuleComplianceRecord writes the row of the module into the modules table.
func writeModuleComplianceRecord(ctx *moduleContext, info *ComplianceMetadataInfo) {
	writeComplianceRecord(ctx.bp, modulesComplianceTable, info.moduleRow(ctx.Config()))
}

// writePackageComplianceRecord writes the row of a package module into the modules table.
func writePackageComplianceRecord(ctx blueprint.ModuleContext, primaryLicenses []string) {
	info := NewComplianceMetadataInfo()
	info.SetStringValue(ComplianceMetadataProp.NAME, ctx.ModuleName())
	info.SetStringValue(ComplianceMetadataProp.MODULE_TYPE, ctx.ModuleType())
	info.SetListValue(ComplianceMetadataProp.PKG_DEFAULT_APPLICABLE_LICENSES, primaryLicenses)
	writeComplianceRecord(ctx, modulesComplianceTable, info.moduleRow(ctx.Config().(Config)))
}

// SetUnbundledAppFiles sets files as the files that the module of ctx, the unbundled app called
// app, contains, and writes them as the rows of the table of the app.
func SetUnbundledAppFiles(ctx ModuleContext, app string, files []UnbundledAppFile) {
	contained := make([]ComplianceFile, len(files))
	for i, file := range files {
		contained[i] = file.Installed
		writeComplianceRecord(ctx.BlueprintModuleContext(), app, unbundledAppFileRow{
			InstalledFile:   file.Installed.value(ctx.Config()),
			BuildOutputPath: compliancePathsOf(ctx.Config(), Paths{file.BuildOutput}),
		})
	}
	ctx.ComplianceMetadataInfo().SetFilesContained(contained)
}

// WriteProductComplianceRecords writes the installed files of the product in files as the rows of
// the make_metadata table. The main device writes them in a build without Kati, in the place of
// Make.
func WriteProductComplianceRecords(ctx ModuleContext, files *ComplianceFilesInfo) {
	for _, row := range productInstalledFileRows(ctx, files) {
		writeComplianceRecord(ctx.BlueprintModuleContext(), makeMetadataComplianceTable, row)
	}
}

// productInstalledFileRows returns the rows of the make_metadata table for files, the files of the
// main device.
func productInstalledFileRows(ctx PathContext, files *ComplianceFilesInfo) []productInstalledFileRow {
	allFiles := slices.Clone(files.filesContained)
	deviceName := String(ctx.Config().productVariables.DeviceName)
	prebuiltCopies := make(map[string]CompliancePrebuiltCopy, len(files.prebuiltFilesCopied))
	for _, copied := range files.prebuiltFilesCopied {
		dest := PathForArbitraryOutput(ctx, "target", "product", deviceName, copied.Dest)
		allFiles = append(allFiles, ComplianceFileOfPath(dest))
		prebuiltCopies[dest.String()] = copied
	}

	platformGenerated := make(map[string]bool, len(files.platformGeneratedFiles))
	for _, file := range files.platformGeneratedFiles {
		platformGenerated[file.String()] = true
	}
	kernelModuleCopies := make(map[string]ComplianceKernelModuleCopy, len(files.kernelModuleCopyFiles))
	for _, copied := range files.kernelModuleCopyFiles {
		kernelModuleCopies[copied.Dest] = copied
	}
	productCopies := make(map[string]ComplianceProductCopy, len(files.productCopyFiles))
	for _, copied := range files.productCopyFiles {
		productCopies[copied.Dest.String()] = copied
	}

	rows := make([]productInstalledFileRow, 0, len(allFiles))
	for _, file := range allFiles {
		row := productInstalledFileRow{InstalledFile: file.value(ctx.Config())}
		if copied, ok := prebuiltCopies[file.String()]; ok {
			row.ProductCopyFiles = complianceText(copied.Src.String() + ":" + copied.Dest)
		} else if platformGenerated[file.String()] {
			row.IsPlatformGenerated = "Y"
			row.LicenseText = compliancePaths{pathRootSrc: {"build/soong/licenses/LICENSE"}}
		} else if copied, ok := kernelModuleCopies[file.String()]; ok {
			row.KernelModuleCopyFiles = complianceText(copied.Src + "::" + copied.Dest)
		} else if copied, ok := productCopies[file.String()]; ok {
			row.ProductCopyFiles = complianceText(copied.Src.String() + ":" + copied.Dest.String())
		} else {
			row.IsSoongModule = "Y"
		}
		rows = append(rows, row)
	}
	return rows
}
