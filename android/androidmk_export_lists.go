// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

//go:generate go run ../../blueprint/gobtools/codegen

// ExportListKind is a kind of list exported by a module to the Make modules built against it.
// The Soong Make file sets the number of the list of each module, and the exports file defines
// every distinct list once under its number, so that Kati reads the lists only when a Make module
// is built against a Soong module.
// @auto-generate: gob
type ExportListKind struct {
	// numberVariable is the variable that sets the number of the list in the Soong Make file.
	numberVariable string
	// definitionPrefix, a dot and the number together form the name of the variable in the
	// exports file that sets the list.
	definitionPrefix string
}

// NewExportListKind returns the kind of list that a module would otherwise set in full as
// entryVariable, a Make variable starting with LOCAL_. The number variable of the kind is
// entryVariable with the suffix _LIST, and its definition prefix is entryVariable with SOONG_ in
// place of LOCAL_.
func NewExportListKind(entryVariable string) ExportListKind {
	name, ok := strings.CutPrefix(entryVariable, "LOCAL_")
	if !ok {
		panic(fmt.Errorf("the export list variable %q does not start with LOCAL_", entryVariable))
	}
	return ExportListKind{
		numberVariable:   entryVariable + "_LIST",
		definitionPrefix: "SOONG_" + name,
	}
}

// ExportList is the list of one kind exported by a module.
// @auto-generate: gob
type ExportList struct {
	Kind   ExportListKind
	Values []string
}

// AddExportList appends values to the export list of the given kind.
func (a *AndroidMkInfo) AddExportList(kind ExportListKind, values ...string) {
	if len(values) == 0 {
		return
	}
	i := a.indexOfExportList(kind)
	if i < 0 {
		i = len(a.ExportLists)
		a.ExportLists = append(a.ExportLists, ExportList{Kind: kind})
	}
	a.ExportLists[i].Values = append(a.ExportLists[i].Values, values...)
}

// ExportListValues returns the values of the export list of the given kind.
func (a *AndroidMkInfo) ExportListValues(kind ExportListKind) []string {
	i := a.indexOfExportList(kind)
	if i < 0 {
		return nil
	}
	return a.ExportLists[i].Values
}

// indexOfExportList returns the index in ExportLists of the list of the given kind, or -1.
func (a *AndroidMkInfo) indexOfExportList(kind ExportListKind) int {
	return slices.IndexFunc(a.ExportLists, func(list ExportList) bool { return list.Kind == kind })
}

// androidMkText is the Make text of one module. The lines setting the numbers of its export lists
// are left out of the text until appendTo numbers the lists in module order.
type androidMkText struct {
	text          bytes.Buffer
	numberedLines []deferredNumberedLine
}

var _ io.Writer = (*androidMkText)(nil)

// deferredNumberedLine is the place in the text of a module where the line setting the number of
// one of its export lists goes.
type deferredNumberedLine struct {
	offset int
	list   ExportList
}

func (t *androidMkText) Write(p []byte) (int, error) {
	return t.text.Write(p)
}

// deferNumberedLine records that the line setting the number of list goes at the end of the text
// written so far.
func (t *androidMkText) deferNumberedLine(list ExportList) {
	t.numberedLines = append(t.numberedLines, deferredNumberedLine{offset: t.text.Len(), list: list})
}

// appendTo appends the text to buf, with the number from exportLists of each deferred line.
func (t *androidMkText) appendTo(buf *bytes.Buffer, exportLists *exportListTable) {
	text := t.text.Bytes()
	written := 0
	for _, line := range t.numberedLines {
		buf.Write(text[written:line.offset])
		written = line.offset
		number := exportLists.numberOrDefine(line.list)
		AndroidMkEmitAssignList(buf, line.list.Kind.numberVariable, []string{strconv.Itoa(number)})
	}
	buf.Write(text[written:])
}

// exportListTable numbers the distinct export lists in the order of their first use by modules,
// and contains the text of the exports file, which defines each list once under its number.
type exportListTable struct {
	numbers map[exportListKey]int
	mk      bytes.Buffer
}

// exportListKey identifies an export list by its kind and its values as Make reads them, so that
// lists with the same Make text share a number.
type exportListKey struct {
	kind   ExportListKind
	mkText string
}

func newExportListTable() *exportListTable {
	return &exportListTable{numbers: make(map[exportListKey]int)}
}

// numberOrDefine returns the number of list, and defines the list in the exports file when it is
// the first list of its kind with its values.
func (t *exportListTable) numberOrDefine(list ExportList) int {
	key := exportListKey{kind: list.Kind, mkText: strings.Join(list.Values, " ")}
	if number, ok := t.numbers[key]; ok {
		return number
	}
	number := len(t.numbers) + 1
	t.numbers[key] = number
	AndroidMkEmitAssignList(&t.mk, fmt.Sprintf("%s.%d", list.Kind.definitionPrefix, number), list.Values)
	return number
}

// exportsMk returns the text of the exports file.
func (t *exportListTable) exportsMk() []byte {
	return t.mk.Bytes()
}
