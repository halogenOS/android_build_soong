// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
	"google.golang.org/protobuf/proto"

	"android/soong/compliance/license_metadata_proto"
)

//go:generate go run ../../blueprint/gobtools/codegen

// A public source is a public copy of prebuilt files. The license metadata of every prebuilt file
// found in one names it, so that the file can be fetched from there instead of being shipped.
//
// A public_source module declares the copy once. A prebuilt module names the copy of its file with
// its public_source property, a package names the copy of all its other prebuilt files and those of
// the directories below it with its default_public_source property, and a public_source_file module
// names the copy of one file of the source tree that no module installs, such as a file copied by
// PRODUCT_COPY_FILES or a radio file.

func init() {
	RegisterPublicSourceBuildComponents(InitRegistrationContext)
}

func RegisterPublicSourceBuildComponents(ctx RegistrationContext) {
	ctx.RegisterModuleType("public_source", PublicSourceFactory)
	ctx.RegisterModuleType("public_source_file", PublicSourceFileFactory)
}

// PublicSourceInfo is the public copy declared by a public_source module.
// @auto-generate: gob
type PublicSourceInfo struct {
	Name     string
	Location PublicSourceLocation
}

// A PublicSourceLocation is the place a public copy is fetched from: a PublicGitRepository, a
// PublicSourceArchive or a PublicImageArchive.
type PublicSourceLocation interface {
	setOn(message *license_metadata_proto.PublicSource)
}

var _ PublicSourceLocation = PublicGitRepository{}
var _ PublicSourceLocation = PublicSourceArchive{}
var _ PublicSourceLocation = PublicImageArchive{}

// @auto-generate: gob
type PublicGitRepository struct {
	Url    string
	Commit string
}

func (r PublicGitRepository) setOn(message *license_metadata_proto.PublicSource) {
	message.Location = &license_metadata_proto.PublicSource_Git{Git: &license_metadata_proto.GitRepository{
		Url:    proto.String(r.Url),
		Commit: proto.String(r.Commit),
	}}
}

// @auto-generate: gob
type PublicSourceArchive struct {
	Url    string
	Sha256 string
	// The length of the archive in bytes.
	Size int64
}

func (a PublicSourceArchive) setOn(message *license_metadata_proto.PublicSource) {
	message.Location = &license_metadata_proto.PublicSource_Archive{Archive: &license_metadata_proto.Archive{
		Url:    proto.String(a.Url),
		Sha256: proto.String(a.Sha256),
		Size:   proto.Int64(a.Size),
	}}
}

// PublicImageArchive is a 7z archive of partition images, each named <partition>.img, split into
// parts, such as a firmware release of a phone. The path of a file in it starts with the partition
// whose image contains the file. A path of a partition name alone names the whole image of the
// partition.
// @auto-generate: gob
type PublicImageArchive struct {
	Parts []ArchivePart
}

// @auto-generate: gob
type ArchivePart struct {
	Url    string
	Sha256 string
	// The length of the part in bytes.
	Size int64
}

func (a PublicImageArchive) setOn(message *license_metadata_proto.PublicSource) {
	parts := make([]*license_metadata_proto.ArchivePart, 0, len(a.Parts))
	for _, part := range a.Parts {
		parts = append(parts, &license_metadata_proto.ArchivePart{
			Url:    proto.String(part.Url),
			Sha256: proto.String(part.Sha256),
			Size:   proto.Int64(part.Size),
		})
	}
	message.Location = &license_metadata_proto.PublicSource_ImageArchive{
		ImageArchive: &license_metadata_proto.ImageArchive{Parts: parts},
	}
}

// PublicSourceFile is a prebuilt file of the tree found in a public source.
// @auto-generate: gob
type PublicSourceFile struct {
	// The file in the tree.
	File Path
	// The path of the file inside the public source.
	Path string
	// The SHA-256 named for the file in the public source, nil when the declaration names none.
	Sha256 *string
	// The length of the file in the public source in bytes, nil when the declaration names none.
	Size *int64
	// The delta file of our edit, which makes the file in the tree from the file in the public
	// source with its fixups made, nil for a file that we did not edit.
	Delta Path
	// The fixups of extract-utils, made in order on the file in the public source before the delta.
	Fixups []PublicSourceFixup
}

// A PublicSourceFixup is a fixup that extract-utils makes on a stock file: a ReplaceNeededFixup, an
// AddNeededFixup, a SetSonameFixup, a BinaryRegexReplaceFixup, a RegexReplaceFixup or an
// AddLineIfMissingFixup.
type PublicSourceFixup interface {
	message() *license_metadata_proto.Fixup
}

var _ PublicSourceFixup = ReplaceNeededFixup{}
var _ PublicSourceFixup = AddNeededFixup{}
var _ PublicSourceFixup = SetSonameFixup{}
var _ PublicSourceFixup = BinaryRegexReplaceFixup{}
var _ PublicSourceFixup = RegexReplaceFixup{}
var _ PublicSourceFixup = AddLineIfMissingFixup{}

// ReplaceNeededFixup is the replace_needed method of the blob_fixups of a device tree: the needed
// library From becomes To.
// @auto-generate: gob
type ReplaceNeededFixup struct {
	From string
	To   string
}

func (f ReplaceNeededFixup) message() *license_metadata_proto.Fixup {
	return &license_metadata_proto.Fixup{Edit: &license_metadata_proto.Fixup_ReplaceNeeded{
		ReplaceNeeded: &license_metadata_proto.LibraryReplacement{From: proto.String(f.From), To: proto.String(f.To)},
	}}
}

// AddNeededFixup is the add_needed method of the blob_fixups of a device tree: the file needs
// Library too.
// @auto-generate: gob
type AddNeededFixup struct {
	Library string
}

func (f AddNeededFixup) message() *license_metadata_proto.Fixup {
	return &license_metadata_proto.Fixup{Edit: &license_metadata_proto.Fixup_AddNeeded{AddNeeded: f.Library}}
}

// SetSonameFixup is the FIX_SONAME argument of a file in the proprietary-files.txt of a device
// tree: the soname of the file becomes Soname.
// @auto-generate: gob
type SetSonameFixup struct {
	Soname string
}

func (f SetSonameFixup) message() *license_metadata_proto.Fixup {
	return &license_metadata_proto.Fixup{Edit: &license_metadata_proto.Fixup_SetSoname{SetSoname: f.Soname}}
}

// BinaryRegexReplaceFixup is the binary_regex_replace method of the blob_fixups of a device tree:
// every match of the Python regular expression Pattern in the bytes of the file becomes
// Replacement.
// @auto-generate: gob
type BinaryRegexReplaceFixup struct {
	Pattern     string
	Replacement string
}

func (f BinaryRegexReplaceFixup) message() *license_metadata_proto.Fixup {
	return &license_metadata_proto.Fixup{Edit: &license_metadata_proto.Fixup_BinaryRegexReplace{
		BinaryRegexReplace: &license_metadata_proto.RegexReplacement{
			Pattern:     proto.String(f.Pattern),
			Replacement: proto.String(f.Replacement),
		},
	}}
}

// RegexReplaceFixup is the regex_replace method of the blob_fixups of a device tree: every match of
// the Python regular expression Pattern in the text of the file becomes Replacement.
// @auto-generate: gob
type RegexReplaceFixup struct {
	Pattern     string
	Replacement string
}

func (f RegexReplaceFixup) message() *license_metadata_proto.Fixup {
	return &license_metadata_proto.Fixup{Edit: &license_metadata_proto.Fixup_RegexReplace{
		RegexReplace: &license_metadata_proto.RegexReplacement{
			Pattern:     proto.String(f.Pattern),
			Replacement: proto.String(f.Replacement),
		},
	}}
}

// AddLineIfMissingFixup is the add_line_if_missing method of the blob_fixups of a device tree: Line
// is added at the end of the text of the file unless one of its lines is Line.
// @auto-generate: gob
type AddLineIfMissingFixup struct {
	Line string
}

func (f AddLineIfMissingFixup) message() *license_metadata_proto.Fixup {
	return &license_metadata_proto.Fixup{Edit: &license_metadata_proto.Fixup_AddLineIfMissing{AddLineIfMissing: f.Line}}
}

// message returns the PublicSourceFile message of the license metadata that records f, without the
// file that the build installs from it.
func (f PublicSourceFile) message() *license_metadata_proto.PublicSourceFile {
	message := &license_metadata_proto.PublicSourceFile{
		File:   proto.String(f.File.String()),
		Path:   proto.String(f.Path),
		Sha256: f.Sha256,
		Size:   f.Size,
	}
	if f.Delta != nil {
		message.Delta = proto.String(f.Delta.String())
	}
	for _, fixup := range f.Fixups {
		message.Fixups = append(message.Fixups, fixup.message())
	}
	return message
}

// inputs returns the files of the source tree that build_license_metadata reads to record f.
func (f PublicSourceFile) inputs() Paths {
	if f.Delta != nil {
		return Paths{f.File, f.Delta}
	}
	return Paths{f.File}
}

// PublicSourceFileInfo is the public copy of one file of the source tree, declared by a
// public_source_file module.
// @auto-generate: gob
type PublicSourceFileInfo struct {
	Source PublicSourceInfo
	File   PublicSourceFile
}

// An installedPrebuiltFile is a prebuilt file of the source tree together with the file that the
// build installs from it.
type installedPrebuiltFile struct {
	src       Path
	installed Path
}

// An installedPublicSourceFile is a prebuilt file found in a public source, together with the file
// that the build installs from it.
type installedPublicSourceFile struct {
	PublicSourceFile
	installed Path
}

var PublicSourceInfoProvider = blueprint.NewProvider[PublicSourceInfo]()
var PublicSourceFileInfoProvider = blueprint.NewProvider[PublicSourceFileInfo]()

type publicSourceDependencyTag struct {
	blueprint.BaseDependencyTag
}

var publicSourceTag = publicSourceDependencyTag{}

// The installer fetches git sources from GitHub, which names commits by SHA-1.
var gitCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type publicSourceProperties struct {
	// URL of the git repository holding the files.
	Git *string
	// Full hash of the commit that contains the files.
	Commit *string
	// URL of the archive holding the files.
	Archive *string
	// SHA-256 of the archive.
	Sha256 *string
	// Length of the archive in bytes.
	Size *int64
	// The parts, in order, of a 7z archive of partition images split into parts.
	Image_archive_parts []archivePartProperties
}

type archivePartProperties struct {
	// URL of the part.
	Url *string
	// SHA-256 of the part.
	Sha256 *string
	// Length of the part in bytes.
	Size *int64
}

// location returns the location that the properties declare, reporting the properties that do not
// form one.
func (p publicSourceProperties) location(ctx ModuleContext) (PublicSourceLocation, bool) {
	imageArchive := len(p.Image_archive_parts) > 0
	switch {
	case p.Git != nil && p.Commit != nil && p.Archive == nil && p.Sha256 == nil && p.Size == nil && !imageArchive:
		checkPublicUrl(ctx, "git", *p.Git)
		if !gitCommitPattern.MatchString(*p.Commit) {
			ctx.PropertyErrorf("commit", "must be the 40-digit hash of a commit, not %q", *p.Commit)
		}
		return PublicGitRepository{Url: *p.Git, Commit: *p.Commit}, true
	case p.Archive != nil && p.Sha256 != nil && p.Size != nil && p.Git == nil && p.Commit == nil && !imageArchive:
		checkPublicUrl(ctx, "archive", *p.Archive)
		checkSha256(ctx, "sha256", *p.Sha256)
		if *p.Size <= 0 {
			ctx.PropertyErrorf("size", "must be the length of the archive in bytes, not %d", *p.Size)
		}
		return PublicSourceArchive{Url: *p.Archive, Sha256: *p.Sha256, Size: *p.Size}, true
	case imageArchive && p.Git == nil && p.Commit == nil && p.Archive == nil && p.Sha256 == nil && p.Size == nil:
		return PublicImageArchive{Parts: archiveParts(ctx, p.Image_archive_parts)}, true
	default:
		ctx.ModuleErrorf("needs either git and commit, archive, sha256 and size, or image_archive_parts")
		return nil, false
	}
}

// archiveParts returns the parts that properties declare, reporting each part that lacks a
// property.
func archiveParts(ctx ModuleContext, properties []archivePartProperties) []ArchivePart {
	parts := make([]ArchivePart, 0, len(properties))
	for i, part := range properties {
		property := fmt.Sprintf("image_archive_parts[%d]", i)
		if part.Url == nil || part.Sha256 == nil || part.Size == nil {
			ctx.PropertyErrorf(property, "needs url, sha256 and size")
			continue
		}
		checkPublicUrl(ctx, property+".url", *part.Url)
		checkSha256(ctx, property+".sha256", *part.Sha256)
		if *part.Size <= 0 {
			ctx.PropertyErrorf(property+".size", "must be the length of the part in bytes, not %d", *part.Size)
		}
		parts = append(parts, ArchivePart{Url: *part.Url, Sha256: *part.Sha256, Size: *part.Size})
	}
	return parts
}

func checkPublicUrl(ctx ModuleContext, property, value string) {
	if u, err := url.Parse(value); err != nil || u.Scheme != "https" || u.Host == "" {
		ctx.PropertyErrorf(property, "must be an https URL, not %q", value)
	}
}

func checkSha256(ctx ModuleContext, property, value string) {
	if !sha256Pattern.MatchString(value) {
		ctx.PropertyErrorf(property, "must be 64 lowercase hexadecimal digits, not %q", value)
	}
}

type publicSourceModule struct {
	ModuleBase

	properties publicSourceProperties
}

func (m *publicSourceModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	if location, ok := m.properties.location(ctx); ok {
		SetProvider(ctx, PublicSourceInfoProvider, PublicSourceInfo{Name: ctx.ModuleName(), Location: location})
	}
}

func PublicSourceFactory() Module {
	module := &publicSourceModule{}
	module.AddProperties(&module.base().nameProperties, &module.properties)
	initAndroidModuleBase(module)
	return module
}

// PublicSourceFileProperties name the public copy of one prebuilt file.
type PublicSourceFileProperties struct {
	// Name of the public_source module that contains the file, the default_public_source of the
	// package when not set.
	From *string `android:"arch_variant"`
	// Path of the file inside the public source.
	Path *string `android:"arch_variant"`
	// SHA-256 of the file in the public source, which the installer checks the download against.
	// The file in the tree can differ from it by our edit, which delta names. The dist package needs
	// it for every file of an archive.
	Sha256 *string `android:"arch_variant"`
	// Length of the file in the public source in bytes.
	Size *int64 `android:"arch_variant"`
	// The delta file of our edit, written by fetch-public-sources write-deltas, which makes the file
	// in the tree from the file in the public source with its fixups made. A file with a delta needs
	// sha256 and size.
	Delta *string `android:"path,arch_variant"`
	// The fixups of extract-utils, made in order on the file in the public source before the delta.
	// A file with fixups needs sha256 and size.
	Fixups []publicSourceFixupProperties `android:"arch_variant"`
}

// publicSourceFixupProperties name one fixup of extract-utils with exactly one of their properties.
type publicSourceFixupProperties struct {
	// The needed library to replace and the library that replaces it, as the replace_needed method
	// of the blob_fixups of a device tree.
	Replace_needed []string
	// A library that the file needs too, as the add_needed method of the blob_fixups of a device
	// tree.
	Add_needed *string
	// The soname that the file gets, as the FIX_SONAME argument of a file in the
	// proprietary-files.txt of a device tree.
	Set_soname *string
	// The Python regular expression of the bytes to replace and the bytes that replace each match,
	// as the binary_regex_replace method of the blob_fixups of a device tree.
	Binary_regex_replace []string
	// The Python regular expression of the text to replace and the text that replaces each match, as
	// the regex_replace method of the blob_fixups of a device tree.
	Regex_replace []string
	// A line that the file gets at the end of its text unless it has the line already, as the
	// add_line_if_missing method of the blob_fixups of a device tree.
	Add_line_if_missing *string
}

// fixup returns the fixup that the properties name, reporting properties that name none or more
// than one.
func (p publicSourceFixupProperties) fixup(ctx ModuleContext, property string) (PublicSourceFixup, bool) {
	edits := 0
	for _, set := range []bool{p.Replace_needed != nil, p.Add_needed != nil, p.Set_soname != nil, p.Binary_regex_replace != nil,
		p.Regex_replace != nil, p.Add_line_if_missing != nil} {
		if set {
			edits++
		}
	}
	if edits == 1 {
		switch {
		case len(p.Replace_needed) == 2:
			return ReplaceNeededFixup{From: p.Replace_needed[0], To: p.Replace_needed[1]}, true
		case p.Add_needed != nil:
			return AddNeededFixup{Library: *p.Add_needed}, true
		case p.Set_soname != nil:
			return SetSonameFixup{Soname: *p.Set_soname}, true
		case len(p.Binary_regex_replace) == 2:
			return BinaryRegexReplaceFixup{Pattern: p.Binary_regex_replace[0], Replacement: p.Binary_regex_replace[1]}, true
		case len(p.Regex_replace) == 2:
			return RegexReplaceFixup{Pattern: p.Regex_replace[0], Replacement: p.Regex_replace[1]}, true
		case p.Add_line_if_missing != nil:
			return AddLineIfMissingFixup{Line: *p.Add_line_if_missing}, true
		}
	}
	ctx.PropertyErrorf(property, "needs either replace_needed with two library names, add_needed, set_soname, "+
		"binary_regex_replace or regex_replace with a pattern and a replacement, or add_line_if_missing")
	return nil, false
}

// declared reports whether the properties name the public copy of a file.
func (p PublicSourceFileProperties) declared() bool {
	return p.From != nil || p.Path != nil || p.Sha256 != nil || p.Size != nil || p.Delta != nil || len(p.Fixups) > 0
}

// foundAt returns file as found at the path inside the public source that the properties name.
func (p PublicSourceFileProperties) foundAt(ctx ModuleContext, propertyPrefix string, file Path) PublicSourceFile {
	if p.Path == nil {
		ctx.PropertyErrorf(propertyPrefix+"path", "is required")
	}
	if p.Sha256 != nil {
		checkSha256(ctx, propertyPrefix+"sha256", *p.Sha256)
	}
	if p.Size != nil && *p.Size < 0 {
		ctx.PropertyErrorf(propertyPrefix+"size", "must be the length of the file in bytes, not %d", *p.Size)
	}
	found := PublicSourceFile{File: file, Path: proptools.String(p.Path), Sha256: p.Sha256, Size: p.Size}
	if (p.Delta != nil || len(p.Fixups) > 0) && (p.Sha256 == nil || p.Size == nil) {
		ctx.PropertyErrorf(propertyPrefix+"sha256", "is required together with size for a file with a delta or fixups")
	}
	if p.Delta != nil {
		found.Delta = PathForModuleSrc(ctx, *p.Delta)
	}
	for i, properties := range p.Fixups {
		if fixup, ok := properties.fixup(ctx, fmt.Sprintf("%sfixups[%d]", propertyPrefix, i)); ok {
			found.Fixups = append(found.Fixups, fixup)
		}
	}
	return found
}

// PublicSourceProperties are the properties of a prebuilt module that name the public copy of its
// prebuilt file.
type PublicSourceProperties struct {
	// The public copy of the prebuilt file, for a file that is not in the default_public_source of
	// its package at its path in the package.
	Public_source PublicSourceFileProperties `android:"arch_variant"`
}

// InitPublicSourceProperty adds the public_source property to a prebuilt module. The prebuilt files
// of the module are the files that it records with ModuleContext.AddInstalledPrebuiltFile.
func InitPublicSourceProperty(module Module) {
	properties := &PublicSourceProperties{}
	module.AddProperties(properties)
	module.base().publicSourceProperties = properties
}

// addPublicSourceDependency adds the dependency of a prebuilt module on the public_source module
// holding its prebuilt files, if any.
func addPublicSourceDependency(ctx BottomUpMutatorContext, properties *PublicSourceProperties) {
	own := properties.Public_source
	name := own.From
	if name == nil {
		name = closestPackageDefaultPublicSource(ctx.Config(), ctx.ModuleDir())
	}
	switch {
	case name != nil:
		ctx.AddDependency(ctx.Module(), publicSourceTag, *name)
	case own.declared():
		ctx.PropertyErrorf("public_source.from", "is required without a default_public_source")
	}
}

// publicSourceDependency returns the public source that the module depends on, if any. It reports
// a dependency that is no public_source module as an error of property.
func publicSourceDependency(ctx blueprint.BaseModuleContext, property string) (PublicSourceInfo, bool) {
	var source PublicSourceInfo
	found := false
	ctx.VisitDirectDepsProxy(func(dep blueprint.ModuleProxy) {
		if ctx.OtherModuleDependencyTag(dep) != publicSourceTag {
			return
		}
		if info, ok := blueprint.OtherModuleProvider(ctx, dep, PublicSourceInfoProvider); ok {
			source, found = info, true
		} else {
			ctx.PropertyErrorf(property, "%q is not a public_source module", ctx.OtherModuleName(dep))
		}
	})
	return source, found
}

// modulePublicSourceFiles returns the prebuilt files of the module found in a public source,
// together with that source.
func modulePublicSourceFiles(ctx *moduleContext) (PublicSourceInfo, []installedPublicSourceFile) {
	properties := ctx.Module().base().publicSourceProperties
	if properties == nil {
		return PublicSourceInfo{}, nil
	}
	// A variant that does not use the prebuilt, such as the static variant of a prebuilt shared
	// library, installs none of its files.
	if prebuilt := GetEmbeddedPrebuilt(ctx.Module()); prebuilt != nil && !prebuilt.UsePrebuilt() {
		return PublicSourceInfo{}, nil
	}
	source, ok := publicSourceDependency(ctx.blueprintBaseModuleContext(), "public_source.from")
	if !ok {
		return PublicSourceInfo{}, nil
	}

	prebuilts := ctx.installedPrebuiltFiles
	if own := properties.Public_source; own.declared() {
		if len(prebuilts) != 1 {
			ctx.PropertyErrorf("public_source", "names one file, the module has %d prebuilt files", len(prebuilts))
			return PublicSourceInfo{}, nil
		}
		file := own.foundAt(ctx, "public_source.", prebuilts[0].src)
		return source, []installedPublicSourceFile{{PublicSourceFile: file, installed: prebuilts[0].installed}}
	}

	packageDir, _ := closestPackageDir(ctx.Config(), ctx.ModuleDir())
	files := make([]installedPublicSourceFile, 0, len(prebuilts))
	for _, prebuilt := range prebuilts {
		path, inPackage := pathInPackage(packageDir, prebuilt.src.String())
		if !inPackage {
			ctx.ModuleErrorf("prebuilt file %s is outside the package of its default_public_source", prebuilt.src)
			continue
		}
		files = append(files, installedPublicSourceFile{
			PublicSourceFile: PublicSourceFile{File: prebuilt.src, Path: path},
			installed:        prebuilt.installed,
		})
	}
	return source, files
}

// writePublicSourceMessage writes to file the PublicSource message of the license metadata that
// records files as found in source. It returns the files that build_license_metadata reads with
// it.
func writePublicSourceMessage(ctx BuilderContext, file WritablePath, source PublicSourceInfo, files []installedPublicSourceFile) Paths {
	messages := make([]*license_metadata_proto.PublicSourceFile, 0, len(files))
	inputs := Paths{file}
	for _, f := range files {
		message := f.message()
		message.Installed = proto.String(f.installed.String())
		messages = append(messages, message)
		inputs = append(inputs, f.inputs()...)
		inputs = append(inputs, f.installed)
	}
	writePublicSourceFilesMessage(ctx, file, source, messages)
	return inputs
}

// writePublicSourceFilesMessage writes to file the PublicSource message that records the files of
// messages as found in source.
func writePublicSourceFilesMessage(ctx BuilderContext, file WritablePath, source PublicSourceInfo, messages []*license_metadata_proto.PublicSourceFile) {
	message := &license_metadata_proto.PublicSource{Name: proto.String(source.Name), Files: messages}
	source.Location.setOn(message)
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		panic(fmt.Errorf("could not encode the public source %s: %w", source.Name, err))
	}
	WriteFileRuleVerbatim(ctx, file, string(data))
}

// closestPackageDir returns the directory of the closest package at or above dir. The
// default_public_source of that package applies to the prebuilt files in dir.
func closestPackageDir(config Config, dir string) (string, bool) {
	_, packageDir, ok := closestPackage(config, dir)
	return packageDir, ok
}

func closestPackageDefaultPublicSource(config Config, dir string) *string {
	pkg, _, ok := closestPackage(config, dir)
	if !ok {
		return nil
	}
	return pkg.defaultPublicSource
}

// closestPackage returns the closest package at or above dir, and its directory.
func closestPackage(config Config, dir string) (licensesContainer, string, bool) {
	packages := moduleToPackageDefaultLicensesMap(config)
	for ; dir != "." && dir != "/"; dir = filepath.Dir(dir) {
		if pkg, ok := packages.Load(dir); ok {
			return pkg.(licensesContainer), dir, true
		}
	}
	return licensesContainer{}, "", false
}

// pathInPackage returns the path of file inside the package in packageDir.
func pathInPackage(packageDir, file string) (string, bool) {
	return strings.CutPrefix(file, packageDir+"/")
}

type publicSourceFileModuleProperties struct {
	// The file of the source tree.
	Src *string `android:"path"`
}

type publicSourceFileModule struct {
	ModuleBase

	properties     publicSourceFileModuleProperties
	fileProperties PublicSourceFileProperties
}

func (m *publicSourceFileModule) DepsMutator(ctx BottomUpMutatorContext) {
	if m.fileProperties.From == nil {
		ctx.PropertyErrorf("from", "is required")
		return
	}
	ctx.AddDependency(ctx.Module(), publicSourceTag, *m.fileProperties.From)
}

func (m *publicSourceFileModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	// A public_source_file of a namespace the product does not use declares a file of another
	// product. The module reads the namespace itself: its minimal module base does not keep
	// NamespaceExportedToMake in its variants.
	if !ctx.Namespace().exportToKati {
		return
	}
	source, ok := publicSourceDependency(ctx.blueprintBaseModuleContext(), "from")
	if !ok {
		return
	}
	file := PathForModuleSrc(ctx, *m.properties.Src)
	SetProvider(ctx, PublicSourceFileInfoProvider, PublicSourceFileInfo{
		Source: source,
		File:   m.fileProperties.foundAt(ctx, "", file),
	})
}

// PublicSourceFileFactory returns a public_source_file module. It has no name property; its name is
// its src.
func PublicSourceFileFactory() Module {
	module := &publicSourceFileModule{}
	module.AddProperties(&module.properties, &module.fileProperties)
	initAndroidModuleBase(module)
	AddLoadHook(module, func(ctx LoadHookContext) {
		if module.properties.Src == nil {
			ctx.PropertyErrorf("src", "is required")
			return
		}
		module.nameProperties.Name = module.properties.Src
	})
	return module
}
