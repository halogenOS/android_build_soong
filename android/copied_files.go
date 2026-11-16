// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

//go:generate go run ../../blueprint/gobtools/codegen

// The files copied by PRODUCT_COPY_FILES have no module. Make gives each one the licenses of the
// closest package above it, when no makefile declares licenses for it, and the public source it is
// found in. The same holds for the files that a makefile installs itself, such as radio files,
// which Soong does not know: Make looks up the closest package above each one in the packages
// written here. A public_source_file declares the public source of one file of the source tree,
// whatever installs it. build/make/core/definitions.mk reads the variables written here.

func init() {
	RegisterParallelSingletonType("copied_files_license_metadata", copiedFilesSingletonFactory)
}

// PackageFilesInfo is what a package declares for the files in its directory that no module
// declares, such as the files copied by PRODUCT_COPY_FILES.
// @auto-generate: gob
type PackageFilesInfo struct {
	License             LicenseInfo
	DefaultPublicSource *PublicSourceInfo
}

var PackageFilesInfoProvider = blueprint.NewProvider[PackageFilesInfo]()

// copiedFileLicenseMetadata is what the Android.bp files declare for a file of the source tree
// that PRODUCT_COPY_FILES copies. Make knows where each copy goes and adds the installed file.
type copiedFileLicenseMetadata struct {
	src string
	// The directory of the closest package above the file, and the licenses of that package, nil
	// when it declares none.
	packageDir string
	license    *LicenseInfo
	// The PublicSource message of the source the file is found in, without files, the files that
	// build_license_metadata reads with it, and where the file lies in that source. publicSource is
	// nil when the file is found in no public source.
	publicSource       Path
	publicSourceInputs Paths
	publicSourceFile   PublicSourceFile
}

// packageFilesMetadata is what a package declares for the files in its directory that a makefile
// installs itself.
type packageFilesMetadata struct {
	dir string
	// The licenses of the package, nil when it declares none.
	license *LicenseInfo
	// The PublicSource message of the default_public_source of the package, without files, and the
	// files that build_license_metadata reads with it, nil when the package names none.
	publicSource       Path
	publicSourceInputs Paths
}

// declaredPublicSourceFile is what a public_source_file module of the product declares for its file
// of the source tree.
type declaredPublicSourceFile struct {
	src string
	// The PublicSource message of the source, without files, and the files that
	// build_license_metadata reads with it.
	publicSource       Path
	publicSourceInputs Paths
	publicSourceFile   PublicSourceFile
}

type copiedFilesSingleton struct {
	files         []copiedFileLicenseMetadata
	packages      []packageFilesMetadata
	declaredFiles []declaredPublicSourceFile
}

func copiedFilesSingletonFactory() Singleton {
	return &copiedFilesSingleton{}
}

func (s *copiedFilesSingleton) GenerateBuildActions(ctx SingletonContext) {
	packages := make(map[string]PackageFilesInfo)
	declaredFiles := make(map[string]PublicSourceFileInfo)
	ctx.VisitAllModuleProxies(func(m ModuleProxy) {
		if info, ok := OtherModuleProvider(ctx, m, PackageFilesInfoProvider); ok {
			packages[ctx.ModuleDir(m)] = info
		}
		if info, ok := OtherModuleProvider(ctx, m, PublicSourceFileInfoProvider); ok {
			src := info.File.File.String()
			if _, declared := declaredFiles[src]; declared {
				ctx.ModuleErrorf(m, "declares a second public source for %s", src)
			}
			declaredFiles[src] = info
		}
	})

	for _, src := range SortedKeys(declaredFiles) {
		info := declaredFiles[src]
		message := PathForOutput(ctx, "public_source_files", src+".pb")
		inputs := writePublicSourceMessage(ctx, message, info.Source, nil)
		if info.File.Delta != nil {
			inputs = append(inputs, info.File.Delta)
		}
		s.declaredFiles = append(s.declaredFiles, declaredPublicSourceFile{
			src:                src,
			publicSource:       message,
			publicSourceInputs: inputs,
			publicSourceFile:   info.File,
		})
	}

	for _, src := range productCopyFileSrcs(ctx.Config()) {
		// A missing file is reported by the copy rule of Make, when something needs the copy.
		file := ExistentPathForSource(ctx, filepath.Clean(src))
		if !file.Valid() {
			continue
		}
		metadata := copiedFileLicenseMetadata{src: src}
		packageDir, inPackage := closestPackageDir(ctx.Config(), filepath.Dir(file.String()))
		pkg := packages[packageDir]
		if inPackage && len(pkg.License.EffectiveLicenseKinds) > 0 {
			metadata.packageDir = packageDir
			metadata.license = &pkg.License
		}

		if _, declared := declaredFiles[file.String()]; !declared && pkg.DefaultPublicSource != nil {
			path, _ := pathInPackage(packageDir, file.String())
			message := PathForOutput(ctx, "copied_files_public_sources", file.String()+".pb")
			metadata.publicSource = message
			metadata.publicSourceInputs = writePublicSourceMessage(ctx, message, *pkg.DefaultPublicSource, nil)
			metadata.publicSourceFile = PublicSourceFile{File: file.Path(), Path: path}
		}

		if metadata.license != nil || metadata.publicSource != nil {
			s.files = append(s.files, metadata)
		}
	}

	// A file outside every package with a default_public_source has no public source, so only these
	// packages and the packages below them are written.
	for _, dir := range SortedKeys(packages) {
		if !isBelowDefaultPublicSource(packages, dir) {
			continue
		}
		pkg := packages[dir]
		metadata := packageFilesMetadata{dir: dir}
		if len(pkg.License.EffectiveLicenseKinds) > 0 {
			metadata.license = &pkg.License
		}
		if pkg.DefaultPublicSource != nil {
			message := PathForOutput(ctx, "package_public_sources", dir+".pb")
			metadata.publicSource = message
			metadata.publicSourceInputs = writePublicSourceMessage(ctx, message, *pkg.DefaultPublicSource, nil)
		}
		s.packages = append(s.packages, metadata)
	}
}

// isBelowDefaultPublicSource reports whether the package in dir, or a package above it, has a
// default_public_source.
func isBelowDefaultPublicSource(packages map[string]PackageFilesInfo, dir string) bool {
	for ; dir != "." && dir != "/"; dir = filepath.Dir(dir) {
		if packages[dir].DefaultPublicSource != nil {
			return true
		}
	}
	return false
}

func (s *copiedFilesSingleton) MakeVars(ctx MakeVarsContext) {
	for _, file := range s.files {
		prefix := "COPIED_FILE." + file.src + "."
		if license := file.license; license != nil {
			ctx.StrictRaw(prefix+"PACKAGE_DIR", file.packageDir)
			writeLicenseMakeVars(ctx, prefix, *license)
		}
		if file.publicSource != nil {
			writePublicSourceMakeVars(ctx, prefix, file.publicSource, file.publicSourceInputs)
			ctx.StrictRaw(prefix+"PUBLIC_SOURCE_PATH", file.publicSourceFile.Path)
			ctx.StrictRaw(prefix+"PUBLIC_SOURCE_SHA256", proptools.String(file.publicSourceFile.Sha256))
		}
	}

	for _, file := range s.declaredFiles {
		prefix := "PUBLIC_SOURCE_FILE." + file.src + "."
		writePublicSourceMakeVars(ctx, prefix, file.publicSource, file.publicSourceInputs)
		ctx.StrictRaw(prefix+"PUBLIC_SOURCE_PATH", file.publicSourceFile.Path)
		ctx.StrictRaw(prefix+"PUBLIC_SOURCE_SHA256", proptools.String(file.publicSourceFile.Sha256))
		if size := file.publicSourceFile.Size; size != nil {
			ctx.StrictRaw(prefix+"PUBLIC_SOURCE_SIZE", strconv.FormatInt(*size, 10))
		}
		if delta := file.publicSourceFile.Delta; delta != nil {
			ctx.StrictRaw(prefix+"PUBLIC_SOURCE_DELTA", delta.String())
		}
	}

	dirs := make([]string, 0, len(s.packages))
	for _, pkg := range s.packages {
		dirs = append(dirs, pkg.dir)
		prefix := "PACKAGE_FILES." + pkg.dir + "."
		if license := pkg.license; license != nil {
			writeLicenseMakeVars(ctx, prefix, *license)
		}
		if pkg.publicSource != nil {
			writePublicSourceMakeVars(ctx, prefix, pkg.publicSource, pkg.publicSourceInputs)
		}
	}
	ctx.StrictRaw("PACKAGE_FILES_DIRS", strings.Join(dirs, " "))
}

// writeLicenseMakeVars writes license as the Make variables whose names start with prefix.
func writeLicenseMakeVars(ctx MakeVarsContext, prefix string, license LicenseInfo) {
	ctx.StrictRaw(prefix+"LICENSE_KINDS", strings.Join(license.EffectiveLicenseKinds, " "))
	ctx.StrictRaw(prefix+"LICENSE_CONDITIONS", strings.Join(license.EffectiveLicenseConditions, " "))
	ctx.StrictRaw(prefix+"NOTICES", strings.Join(license.EffectiveLicenseText.Strings(), " "))
	ctx.StrictRaw(prefix+"LICENSE_PACKAGE_NAME", proptools.String(license.PackageName))
}

// writePublicSourceMakeVars writes the PublicSource message and its inputs as the Make variables
// whose names start with prefix.
func writePublicSourceMakeVars(ctx MakeVarsContext, prefix string, message Path, inputs Paths) {
	ctx.StrictRaw(prefix+"PUBLIC_SOURCE", message.String())
	ctx.StrictRaw(prefix+"PUBLIC_SOURCE_INPUTS", strings.Join(inputs.Strings(), " "))
}

// productCopyFileSrcs returns the files of the source tree that PRODUCT_COPY_FILES copies, each
// once, spelled as in PRODUCT_COPY_FILES.
func productCopyFileSrcs(config Config) []string {
	var srcs []string
	for _, entry := range config.ProductCopyFiles() {
		src, _, _ := strings.Cut(entry, ":")
		srcs = append(srcs, src)
	}
	return SortedUniqueStrings(srcs)
}
