// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"path/filepath"
	"slices"
)

// The tables whose files soong_build or Kati write.
const (
	ModulesComplianceTable      = "modules"
	MakeMetadataComplianceTable = "make_metadata"
	MakeModulesComplianceTable  = "make_modules"
)

// A ComplianceTableKind is a kind of table of the compliance metadata database.
type ComplianceTableKind struct{ name string }

var (
	ComplianceTableOfModules = ComplianceTableKind{"modules"}
	// ComplianceTableOfMake is a table of Make, which Kati writes, and soong_build in a build
	// without Kati.
	ComplianceTableOfMake = ComplianceTableKind{"make"}
	// ComplianceTableOfUnbundledApp is the table of an unbundled app, named after the app.
	ComplianceTableOfUnbundledApp = ComplianceTableKind{"unbundled app"}
)

// A ComplianceTable is a table of the compliance metadata database.
type ComplianceTable struct {
	Name string
	Kind ComplianceTableKind
}

// ComplianceTables returns the tables of the compliance metadata database: the modules, the tables
// of Make and the table of each unbundled app.
func ComplianceTables(unbundledApps []string) []ComplianceTable {
	tables := []ComplianceTable{
		{Name: ModulesComplianceTable, Kind: ComplianceTableOfModules},
		{Name: MakeMetadataComplianceTable, Kind: ComplianceTableOfMake},
		{Name: MakeModulesComplianceTable, Kind: ComplianceTableOfMake},
	}
	for _, app := range unbundledApps {
		table := ComplianceTable{Name: app, Kind: ComplianceTableOfUnbundledApp}
		if !slices.Contains(tables, table) {
			tables = append(tables, table)
		}
	}
	return tables
}

// WrittenBySoong reports whether soong_build writes the records of t in a build that runs Kati
// when katiEnabled is true.
func (t ComplianceTable) WrittenBySoong(katiEnabled bool) bool {
	return t.Kind != ComplianceTableOfMake || !katiEnabled
}

// ComplianceTablePath returns the file with the records of table in the build of product, relative
// to the Soong output directory.
func ComplianceTablePath(product, table string) string {
	return filepath.Join("compliance-metadata", product, table+".jsonl")
}

// A ComplianceRecordStream is the file, relative to the Soong output directory, into which
// soong_build writes the records of Table.
type ComplianceRecordStream struct {
	Table ComplianceTable
	Path  string
}

// ComplianceRecordStreams returns the record streams that soong_build writes in the build of
// product with unbundledApps, in a build that runs Kati when katiEnabled is true.
func ComplianceRecordStreams(product string, unbundledApps []string, katiEnabled bool) []ComplianceRecordStream {
	var streams []ComplianceRecordStream
	for _, table := range ComplianceTables(unbundledApps) {
		if table.WrittenBySoong(katiEnabled) {
			streams = append(streams, ComplianceRecordStream{Table: table, Path: ComplianceTablePath(product, table.Name)})
		}
	}
	return streams
}
