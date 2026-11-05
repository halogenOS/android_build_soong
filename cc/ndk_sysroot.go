// Copyright 2016 Google Inc. All rights reserved.
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

package cc

// The platform needs to provide the following artifacts for the NDK:
// 1. Bionic headers.
// 2. Platform API headers.
// 3. NDK stub shared libraries.
// 4. Bionic static libraries.
//
// TODO(danalbert): All of the above need to include NOTICE files.
//
// Components 1 and 2: Headers
// The bionic and platform API headers are generalized into a single
// `ndk_headers` rule. This rule has a `from` property that indicates a base
// directory from which headers are to be taken, and a `to` property that
// indicates where in the sysroot they should reside relative to usr/include.
// There is also a `srcs` property that is glob compatible for specifying which
// headers to include.
//
// Component 3: Stub Libraries
// The shared libraries in the NDK are not the actual shared libraries they
// refer to (to prevent people from accidentally loading them), but stub
// libraries with placeholder implementations of everything for use at build time
// only.
//
// Since we don't actually need to know anything about the stub libraries aside
// from a list of functions and globals to be exposed, we can create these for
// every platform level in the current tree. This is handled by the
// ndk_library rule.
//
// Component 4: Static Libraries
// The NDK only provides static libraries for bionic, not the platform APIs.
// Since these need to be the actual implementation, we can't build old versions
// in the current platform tree. As such, legacy versions are checked in
// prebuilt to development/ndk, and a current version is built and archived as
// part of the platform build. The platfrom already builds these libraries, our
// NDK build rules only need to archive them for retrieval so they can be added
// to the prebuilts.
//
// TODO(danalbert): Write `ndk_static_library` rule.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"android/soong/android"
	"android/soong/cc/config"

	"github.com/google/blueprint"
)

var (
	verifyCCompat = pctx.AndroidStaticRule("verifyCCompat",
		blueprint.RuleParams{
			Command:         "$ccCmd -x c -fsyntax-only $flags $in && touch $out",
			CommandDeps:     []string{"$ccCmd"},
			SandboxDisabled: true,
		},
		"ccCmd",
		"flags",
	)
)

func init() {
	RegisterNdkModuleTypes(android.InitRegistrationContext)
}

func RegisterNdkModuleTypes(ctx android.RegistrationContext) {
	ctx.RegisterModuleType("ndk_headers", NdkHeadersFactory)
	ctx.RegisterModuleType("ndk_library", NdkLibraryFactory)
	ctx.RegisterModuleType("preprocessed_ndk_headers", preprocessedNdkHeadersFactory)
	ctx.RegisterParallelSingletonType("ndk", NdkSingleton)
}

func getNdkInstallBase(ctx android.PathContext) android.OutputPath {
	return android.PathForNdkInstall(ctx)
}

// Returns the main install directory for the NDK sysroot. Usable with --sysroot.
func getNdkSysrootBase(ctx android.PathContext) android.OutputPath {
	return getNdkInstallBase(ctx).Join(ctx, "sysroot")
}

// Returns the clang file map that maps every header path of the sysroot to the
// file with that header, so that compiles need no copies of the headers. Usable
// with -ivfsoverlay. It is outside the NDK install directory, which the ndk and
// ndk_sysroot goals pack into archives.
func getNdkSysrootOverlay(ctx android.PathContext) android.WritablePath {
	return android.PathForOutput(ctx, "ndk_sysroot.vfs.yaml")
}

// Returns the flags that let a compile for toolchain find the headers of the
// NDK sysroot through the header map of getNdkSysrootOverlay. The driver adds
// the include directories of a sysroot only when they exist on disk, so the
// flags turn that off with -nostdlibinc and list usr/include/<triple> and
// usr/include of the sysroot in the order used by the driver. The search order
// then does not depend on what earlier builds left in the sysroot. --sysroot is
// still in the flags for the tools that read the sysroot from them, such as the
// CMakeLists.txt generator.
func ndkSysrootCflags(ctx android.PathContext, toolchain config.Toolchain) []string {
	includeDir := getCurrentIncludePath(ctx)
	return []string{
		"--sysroot " + getNdkSysrootBase(ctx).String(),
		"-nostdlibinc",
		"-ivfsoverlay " + getNdkSysrootOverlay(ctx).String(),
		"-Xclang -internal-externc-isystem -Xclang " + includeDir.Join(ctx, config.NDKTriple(toolchain)).String(),
		"-Xclang -internal-externc-isystem -Xclang " + includeDir.String(),
	}
}

// Returns the files that a compile with ndkSysrootCflags depends on besides
// its sources. The header map is an input, so a changed map compiles again.
// The headers timestamp waits for every header listed in the map and is
// order-only: the dependency file of the compile lists the headers it read.
func ndkSysrootCompileDeps(ctx android.PathContext) (implicits, orderOnly android.Paths) {
	return android.Paths{getNdkSysrootOverlay(ctx)}, android.Paths{getNdkHeadersTimestampFile(ctx)}
}

// Returns the flags that let clang-tidy read the headers of the NDK sysroot
// through the header map. clang-tidy ignores -ivfsoverlay in the compile flags
// and reads the map only through an option of its own.
func ndkSysrootTidyFlags(ctx android.PathContext) []string {
	return []string{"-vfsoverlay=" + getNdkSysrootOverlay(ctx).String()}
}

// addNdkSysrootFlags lets the compiles of flags and their clang-tidy steps read
// the headers of the NDK sysroot, and makes them depend on what they read.
func addNdkSysrootFlags(ctx android.PathContext, flags Flags) Flags {
	implicits, orderOnly := ndkSysrootCompileDeps(ctx)
	flags.SystemIncludeFlags = append(flags.SystemIncludeFlags, ndkSysrootCflags(ctx, flags.Toolchain)...)
	flags.TidyFlags = append(flags.TidyFlags, ndkSysrootTidyFlags(ctx)...)
	flags.CFlagsDeps = append(flags.CFlagsDeps, implicits...)
	flags.orderOnlyDeps = append(flags.orderOnlyDeps, orderOnly...)
	flags.headerCopies = append(flags.headerCopies, getNdkSysrootHeadersTimestampFile(ctx))
	return flags
}

// The headers timestamp file depends on the header map of getNdkSysrootOverlay
// and the files listed in it, not on the copies in the sysroot. Compiles of SDK
// variants wait for it.
func getNdkHeadersTimestampFile(ctx android.PathContext) android.WritablePath {
	return android.PathForOutput(ctx, "ndk_headers.timestamp")
}

// The sysroot headers timestamp file depends on the NDK headers copied into the
// sysroot. header-abi-dumper, the Kythe extractor and the C compatibility check
// of the headers read the copies, because they cannot read the header map.
func getNdkSysrootHeadersTimestampFile(ctx android.PathContext) android.WritablePath {
	return android.PathForOutput(ctx, "ndk_sysroot_headers.timestamp")
}

// The full timestamp file depends on the NDK headers, stub shared libraries and
// static libraries copied into the sysroot, the combined NOTICE file and the C
// compatibility checks of the headers.
func getNdkFullTimestampFile(ctx android.PathContext) android.WritablePath {
	return android.PathForOutput(ctx, "ndk.timestamp")
}

// The list of all NDK headers as they are located in the repo.
// Used for ABI monitoring to track only structures defined in NDK headers.
func getNdkABIHeadersFile(ctx android.PathContext) android.WritablePath {
	return android.PathForOutput(ctx, "ndk_abi_headers.txt")
}

func verifyNdkHeaderIsCCompatible(ctx android.SingletonContext,
	src android.Path, dest android.Path) android.Path {
	baseOutputDir := android.PathForOutput(ctx, "c-compat-verification")
	installRelPath, err := ndkHeaderIncludePath(ctx, dest)
	if err != nil {
		ctx.Errorf("could not verify the C compatibility of %s: %s", src, err)
	}
	output := baseOutputDir.Join(ctx, installRelPath)
	ctx.Build(pctx, android.BuildParams{
		Rule:        verifyCCompat,
		Description: fmt.Sprintf("Verifying C compatibility of %s", src),
		Output:      output,
		Input:       dest,
		// Ensures that all the headers in the sysroot are already installed
		// before testing any of the headers for C compatibility, and also that
		// the check will be re-run whenever the sysroot changes. This is
		// necessary because many of the NDK headers depend on other NDK
		// headers, but we don't have explicit dependency tracking for that.
		Implicits: []android.Path{getNdkSysrootHeadersTimestampFile(ctx)},
		Args: map[string]string{
			"ccCmd": "${config.ClangBin}/clang",
			"flags": fmt.Sprintf(
				// Ideally we'd check each ABI, multiple API levels,
				// fortify/non-fortify, and a handful of other variations. It's
				// a lot more difficult to do that though, and would eat up more
				// build time. All the problems we've seen so far that this
				// check would catch have been in arch-generic and
				// minSdkVersion-generic code in frameworks though, so this is a
				// good place to start.
				"-target aarch64-linux-android%d --sysroot %s",
				android.FutureApiLevel.FinalOrFutureInt(),
				getNdkSysrootBase(ctx).String(),
			),
		},
	})
	return output
}

func NdkSingleton() android.Singleton {
	return &ndkSingleton{}
}

// Collect all NDK exported headers paths into a file that is used to
// detect public types that should be ABI monitored.
//
// Assume that we have the following code in exported header:
//
//	typedef struct Context Context;
//	typedef struct Output {
//	    ...
//	} Output;
//	void DoSomething(Context* ctx, Output* output);
//
// If none of public headers exported to end-users contain definition of
// "struct Context", then "struct Context" layout and members shouldn't be
// monitored. However we use DWARF information from a real library, which
// may have access to the definition of "string Context" from
// implementation headers, and it will leak to ABI.
//
// STG tool doesn't access source and header files, only DWARF information
// from compiled library. And the DWARF contains file name where a type is
// defined. So we need a rule to build a list of paths to public headers,
// so STG can distinguish private types from public and do not monitor
// private types that are not accessible to library users.
func writeNdkAbiSrcFilter(ctx android.BuilderContext,
	headerSrcPaths android.Paths, outputFile android.WritablePath) {
	var filterBuilder strings.Builder
	filterBuilder.WriteString("[decl_file_allowlist]\n")
	for _, headerSrcPath := range headerSrcPaths {
		filterBuilder.WriteString(headerSrcPath.String())
		filterBuilder.WriteString("\n")
	}

	android.WriteFileRule(ctx, outputFile, filterBuilder.String())
}

// ndkHeaderIncludePath returns the path of the header installed at
// installPath relative to usr/include of the sysroot, the path under which
// compiles include it.
func ndkHeaderIncludePath(ctx android.PathContext, installPath android.Path) (string, error) {
	includeDir := getCurrentIncludePath(ctx)
	relPath, err := filepath.Rel(includeDir.String(), installPath.String())
	if err != nil {
		return "", fmt.Errorf("could not place NDK header %s under %s: %w", installPath, includeDir, err)
	}
	return relPath, nil
}

// clangOverlay is a clang file map, read by clang through -ivfsoverlay and by
// clang-tidy through -vfsoverlay. The file follows the format of
// RedirectingFileSystem, described in
// llvm/include/llvm/Support/VirtualFileSystem.h.
type clangOverlay struct {
	roots []clangOverlayDirectory
}

// MarshalJSON writes the map in version 0, the only version of the format, and
// with use-external-names, so that clang writes the files that the map points
// at into dependency files and diagnostics.
func (o clangOverlay) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Version          int                     `json:"version"`
		UseExternalNames bool                    `json:"use-external-names"`
		Roots            []clangOverlayDirectory `json:"roots"`
	}{Version: 0, UseExternalNames: true, Roots: o.roots})
}

// clangOverlayEntryType is the kind of an entry of a clang file map.
type clangOverlayEntryType struct{ name string }

var (
	clangOverlayEntryDirectory = clangOverlayEntryType{"directory"}
	clangOverlayEntryFile      = clangOverlayEntryType{"file"}
)

func (t clangOverlayEntryType) MarshalText() ([]byte, error) {
	return []byte(t.name), nil
}

// clangOverlayDirectory is a directory of a clang file map with the files in it.
type clangOverlayDirectory struct {
	name     string
	contents []clangOverlayFile
}

func (d clangOverlayDirectory) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type     clangOverlayEntryType `json:"type"`
		Name     string                `json:"name"`
		Contents []clangOverlayFile    `json:"contents"`
	}{Type: clangOverlayEntryDirectory, Name: d.name, Contents: d.contents})
}

// clangOverlayFile places the file externalContents at the path name of its
// directory.
type clangOverlayFile struct {
	name             string
	externalContents string
}

func (f clangOverlayFile) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type             clangOverlayEntryType `json:"type"`
		Name             string                `json:"name"`
		ExternalContents string                `json:"external-contents"`
	}{Type: clangOverlayEntryFile, Name: f.name, ExternalContents: f.externalContents})
}

// writeNdkSysrootOverlay writes the clang file map that places the Content of
// each header at its Install path. With use-external-names, clang writes the
// Content files into dependency files and diagnostics.
func writeNdkSysrootOverlay(ctx android.SingletonContext, headers []NdkHeaderFile) {
	files := make([]clangOverlayFile, 0, len(headers))
	for _, header := range headers {
		name, err := ndkHeaderIncludePath(ctx, header.Install)
		if err != nil {
			ctx.Errorf("could not write the NDK header map: %s", err)
			return
		}
		files = append(files, clangOverlayFile{name: name, externalContents: header.Content.String()})
	}
	slices.SortFunc(files, func(a, b clangOverlayFile) int { return strings.Compare(a.name, b.name) })

	overlay, err := json.Marshal(clangOverlay{roots: []clangOverlayDirectory{{
		name:     getCurrentIncludePath(ctx).String(),
		contents: files,
	}}})
	if err != nil {
		ctx.Errorf("could not encode the NDK header map: %s", err)
		return
	}
	android.WriteFileRule(ctx, getNdkSysrootOverlay(ctx), string(overlay))
}

type ndkSingleton struct{}

func (n *ndkSingleton) GenerateBuildActions(ctx android.SingletonContext) {
	var staticLibInstallPaths android.Paths
	var headers []NdkHeaderFile
	var headersToVerify []NdkHeaderFile
	var headerCCompatVerificationTimestampPaths android.Paths
	var installPaths android.Paths
	var licensePaths android.Paths
	ctx.VisitAllModuleProxies(func(module android.ModuleProxy) {

		if !android.OtherModulePointerProviderOrDefault(ctx, module, android.CommonModuleInfoProvider).Enabled {
			return
		}

		for _, provider := range []blueprint.ProviderKey[NdkHeaderInfo]{
			NdkHeaderInfoProvider, NdkPreprocessedHeaderInfoProvider} {
			if m, ok := android.OtherModuleProvider(ctx, module, provider); ok {
				headers = append(headers, m.Headers...)
				if !m.SkipVerification {
					headersToVerify = append(headersToVerify, m.Headers...)
				}
				for _, header := range m.Headers {
					installPaths = append(installPaths, header.Install)
				}
				licensePaths = append(licensePaths, m.LicensePath)
			}
		}

		if ccInfo, ok := android.OtherModuleProvider(ctx, module, CcInfoProvider); ok {
			if installer := ccInfo.InstallerInfo; installer != nil && installer.StubDecoratorInfo != nil &&
				ccInfo.LibraryInfo != nil && ccInfo.LibraryInfo.BuildStubs {
				installPaths = append(installPaths, installer.StubDecoratorInfo.InstallPath)
			}

			if ccInfo.LinkerInfo != nil {
				if library := ccInfo.LinkerInfo.LibraryDecoratorInfo; library != nil {
					if library.NdkSysrootPath != nil {
						staticLibInstallPaths = append(
							staticLibInstallPaths, library.NdkSysrootPath)
					}
				}

				if object := ccInfo.LinkerInfo.ObjectLinkerInfo; object != nil {
					if object.NdkSysrootPath != nil {
						staticLibInstallPaths = append(
							staticLibInstallPaths, object.NdkSysrootPath)
					}
				}
			}
		}
	})

	// Include only a single copy of each license file. The Bionic NOTICE is
	// long and is referenced by multiple Bionic modules.
	licensePaths = android.FirstUniquePaths(licensePaths)

	combinedLicense := getNdkInstallBase(ctx).Join(ctx, "NOTICE")
	ctx.Build(pctx, android.BuildParams{
		Rule:        android.CatRule,
		Description: "combine licenses",
		Output:      combinedLicense,
		Inputs:      licensePaths,
	})

	var headerSrcPaths, headerInstallPaths, headerContentPaths android.Paths
	for _, header := range headers {
		headerSrcPaths = append(headerSrcPaths, header.Src)
		headerInstallPaths = append(headerInstallPaths, header.Install)
		headerContentPaths = append(headerContentPaths, header.Content)
	}

	writeNdkSysrootOverlay(ctx, headers)

	ctx.Build(pctx, android.BuildParams{
		Rule:      android.TouchRule,
		Output:    getNdkHeadersTimestampFile(ctx),
		Implicits: append(android.Paths{getNdkSysrootOverlay(ctx)}, headerContentPaths...),
	})

	ctx.Build(pctx, android.BuildParams{
		Rule:      android.TouchRule,
		Output:    getNdkSysrootHeadersTimestampFile(ctx),
		Implicits: headerInstallPaths,
	})

	for _, header := range headersToVerify {
		headerCCompatVerificationTimestampPaths = append(
			headerCCompatVerificationTimestampPaths,
			verifyNdkHeaderIsCCompatible(ctx, header.Src, header.Install))
	}

	writeNdkAbiSrcFilter(ctx, headerSrcPaths, getNdkABIHeadersFile(ctx))

	fullDepPaths := append(append(staticLibInstallPaths, installPaths...), combinedLicense)

	// There's a phony "ndk" rule defined in core/main.mk that depends on this.
	// `m ndk` will build the sysroots for the architectures in the current
	// lunch target. `build/soong/scripts/build-ndk-prebuilts.sh` will build the
	// sysroots for all the NDK architectures and package them so they can be
	// imported into the NDK's build.
	ctx.Build(pctx, android.BuildParams{
		Rule:      android.TouchRule,
		Output:    getNdkFullTimestampFile(ctx),
		Implicits: append(fullDepPaths, headerCCompatVerificationTimestampPaths...),
	})
}
