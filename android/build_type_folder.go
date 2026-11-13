// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/google/blueprint"
)

// The output folder name of a module variant contains the build type values read by its commands
// when they are true, so that the user, userdebug and eng builds keep the results that differ
// between them side by side and userdebug and eng share the results that only read Debuggable.
// The commands of a module also contain the output paths of the modules it depends on, so a module
// gets the build type values in the folder names of those modules as well.
//
// Values read in mutators, in product_variables and in selects are recorded automatically. Module
// code that reads a build type value in GenerateAndroidBuildActions, after the folder is named,
// records that value in a mutator with RecordBuildTypeRead.

// BuildTypeValue is a product variable whose value tells the build types user, userdebug and eng
// apart.
type BuildTypeValue struct {
	// name is the spelling of the value in product_variables and selects, and its part of an
	// output folder name.
	name     string
	bit      buildTypeSet
	isTrueIn func(Config) bool
}

var (
	// BuildTypeDebuggable is true in userdebug and eng builds.
	BuildTypeDebuggable = BuildTypeValue{name: "debuggable", bit: 1 << 0, isTrueIn: Config.Debuggable}
	// BuildTypeEng is true in eng builds.
	BuildTypeEng = BuildTypeValue{name: "eng", bit: 1 << 1, isTrueIn: Config.Eng}

	// allBuildTypeValues is in the order of the values in an output folder name.
	allBuildTypeValues = []BuildTypeValue{BuildTypeDebuggable, BuildTypeEng}
)

// buildTypeSet contains one bit per build type value. Blueprint copies it with the properties of a
// module.
type buildTypeSet uint

// parseBuildTypeValue returns the build type value spelled name in product_variables.
func parseBuildTypeValue(name string) (BuildTypeValue, bool) {
	for _, value := range allBuildTypeValues {
		if value.name == name {
			return value, true
		}
	}
	return BuildTypeValue{}, false
}

func trueBuildTypeValues(config Config) buildTypeSet {
	var set buildTypeSet
	for _, value := range allBuildTypeValues {
		if value.isTrueIn(config) {
			set |= value.bit
		}
	}
	return set
}

// RecordBuildTypeRead records, in a mutator, that GenerateAndroidBuildActions of the module variant
// reads value.
func RecordBuildTypeRead(ctx BaseModuleContext, value BuildTypeValue) {
	// GenerateAndroidBuildActions reads the value itself.
	_ = ctx.Module().base().readBuildTypeValue(ctx.Config(), value)
}

// readBuildTypeValue returns value as the module variant sees it, false where the build variant
// does not apply to the module variant, and records the value read.
func (m *ModuleBase) readBuildTypeValue(config Config, value BuildTypeValue) bool {
	if !TargetBuildVariantAppliesTo(m.module) {
		return false
	}
	m.buildTypeValuesReadMutex.Lock()
	m.baseProperties.Build_type_values_read |= value.bit
	m.buildTypeValuesReadMutex.Unlock()
	return value.isTrueIn(config)
}

func (m *ModuleBase) buildTypeValuesRead() buildTypeSet {
	m.buildTypeValuesReadMutex.Lock()
	defer m.buildTypeValuesReadMutex.Unlock()
	return m.baseProperties.Build_type_values_read
}

// buildTypeFolder is the output folder of a module variant.
type buildTypeFolder struct {
	// Values are the build type values that appear in the output folder names of the module variant
	// and of the modules using it where they are true.
	Values buildTypeSet

	// OutSubDir is the folder under the output directory of the module: the variant name followed
	// by the true build type values.
	OutSubDir string
}

var buildTypeFolderProvider = blueprint.NewMutatorProvider[buildTypeFolder]("build_type_folder")

// DependencyOutputUse is implemented by module types whose commands leave out the outputs of some
// direct dependencies. The build type values of such a dependency do not enter the folder name.
type DependencyOutputUse interface {
	UsesDependencyOutputs(tag blueprint.DependencyTag) bool
}

// errBuildTypeFolderMissing is the error for a module variant that the build_type_folder mutator
// did not run on.
func errBuildTypeFolderMissing(moduleName string) error {
	return fmt.Errorf("%s does not have an output folder name because the build_type_folder mutator did not run on it",
		moduleName)
}

// registerBuildTypeFolderMutator registers the mutator that sets the output folder names. It runs
// after every other mutator, so that it sees every value recorded by a mutator.
func registerBuildTypeFolderMutator(ctx RegisterMutatorsContext) {
	ctx.BottomUp("build_type_folder", buildTypeFolderMutator)
}

func buildTypeFolderMutator(ctx BottomUpMutatorContext) {
	m := ctx.Module().base()
	if selectsCanNameFolder(ctx) {
		evaluateConfigurableProperties(m.ConfigurableEvaluator(errorlessEvaluatorContext{ctx}), reflect.ValueOf(m.GetProperties()))
	}
	outputUse, filtersDeps := ctx.Module().(DependencyOutputUse)
	var depValues buildTypeSet
	ctx.VisitDirectDepsProxy(func(dep ModuleProxy) {
		tag := ctx.OtherModuleDependencyTag(dep)
		if tag == RequiredDepTag {
			// A required module is installed with the module. Its output paths are not in the
			// commands of the module.
			return
		}
		if filtersDeps && !outputUse.UsesDependencyOutputs(tag) {
			return
		}
		depFolder, ok := OtherModuleProvider(ctx, dep, buildTypeFolderProvider)
		if !ok {
			panic(errBuildTypeFolderMissing(ctx.OtherModuleName(dep)))
		}
		depValues |= depFolder.Values
	})
	values := m.buildTypeValuesRead() | depValues
	SetProvider(ctx, buildTypeFolderProvider, buildTypeFolder{
		Values:    values,
		OutSubDir: buildTypeFolderName(ctx.Config(), ctx.OtherModuleSubDir(ctx.Module()), values),
	})
}

// selectsCanNameFolder reports whether build type values read by the selects of the module variant
// can appear in its output folder name. Defaults modules and disabled modules do not build anything.
func selectsCanNameFolder(ctx BottomUpMutatorContext) bool {
	_, isDefaults := ctx.Module().(Defaults)
	return trueBuildTypeValues(ctx.Config()) != 0 && TargetBuildVariantAppliesTo(ctx.Module()) &&
		!isDefaults && ctx.Module().Enabled(ctx)
}

func buildTypeFolderName(config Config, variant string, folderValues buildTypeSet) string {
	var parts []string
	if variant != "" {
		parts = append(parts, variant)
	}
	nameValues := folderValues & trueBuildTypeValues(config)
	for _, v := range allBuildTypeValues {
		if nameValues&v.bit != 0 {
			parts = append(parts, v.name)
		}
	}
	return strings.Join(parts, "_")
}
