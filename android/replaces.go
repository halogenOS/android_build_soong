// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"fmt"

	"github.com/google/blueprint"
)

// A replacedDefinition is a definition replaced by a module, with the entry that refers to it.
type replacedDefinition struct {
	ref       blueprint.ReplacedRef
	module    Module
	namespace blueprint.Namespace
}

var _ blueprint.ReplacingModule = (*ModuleBase)(nil)

// ReplacedRefs returns the definitions listed in replaces. An entry refers to the source module
// with the listed name and to the prebuilt of that source module, which Make knows under the same
// name.
func (m *ModuleBase) ReplacedRefs() ([]blueprint.ReplacedRef, error) {
	var refs []blueprint.ReplacedRef
	for _, entry := range m.nameProperties.Replaces {
		dir, name, ok := parseFullyQualifiedName(entry)
		if !ok || name == "" {
			return nil, fmt.Errorf("replaces: %q is not of the form //<directory>:<name>", entry)
		}
		refs = append(refs,
			blueprint.ReplacedRef{Dir: dir, Name: name, MakeName: name},
			blueprint.ReplacedRef{Dir: dir, Name: PrebuiltNameFromSource(name), MakeName: name})
	}
	return refs, nil
}

// CanReplace returns an error when replaced is not an Android module. replacesCheckMutator
// compares the two modules once defaults apply.
func (m *ModuleBase) CanReplace(ref blueprint.ReplacedRef, replaced blueprint.Module) error {
	if _, ok := replaced.(Module); !ok {
		return fmt.Errorf("module %q cannot replace %q, which is not an Android module", m.makeName(), ref.MakeName)
	}
	return nil
}

// Replaced records replaced and the namespace that contains it for replacesCheckMutator. When Make
// knows replaced under a name that m does not have, it also records that name for a phony Make
// module that requires m.
func (m *ModuleBase) Replaced(ref blueprint.ReplacedRef, replaced blueprint.Module, namespace blueprint.Namespace) {
	r := replaced.(Module)
	m.replacedDefinitions = append(m.replacedDefinitions, replacedDefinition{ref, r, namespace})
	if r.ExportedToMake() && ref.MakeName != m.makeName() {
		m.commonProperties.ReplacedMakeNames = append(m.commonProperties.ReplacedMakeNames, ref.MakeName)
	}
}

// makeName returns the name of m in Make. A module type can return another name than the one in
// the definition, as a cc prebuilt does with source_module_name.
func (m *ModuleBase) makeName() string {
	if b, ok := m.module.(baseModuleName); ok {
		return b.BaseModuleName()
	}
	return m.BaseModuleName()
}

// RegisterReplacesCheckMutator registers replacesCheckMutator, which must run after the defaults
// mutators.
func RegisterReplacesCheckMutator(ctx RegisterMutatorsContext) {
	ctx.BottomUp("replaces_check", replacesCheckMutator)
}

// replacesCheckMutator fails when a definition replaced by the module installs into another
// partition, or differs from it in host and device support. The defaults of the module apply
// already. The replaced definition is out of the build, and its defaults apply here.
func replacesCheckMutator(ctx BottomUpMutatorContext) {
	m := ctx.Module().base()
	for _, r := range m.replacedDefinitions {
		applyDefaultsByName(ctx, r.module, r.namespace)
		if got, want := installTargetOf(m), installTargetOf(r.module.base()); got != want {
			ctx.ModuleErrorf("installs to %s and cannot replace %q, which installs to %s", got, r.ref.MakeName, want)
		}
	}
}

// applyDefaultsByName applies the defaults modules listed by m, and the ones listed by those, in
// the order of defaultsMutator. The names are looked up as a module in namespace would.
func applyDefaultsByName(ctx BottomUpMutatorContext, m Module, namespace blueprint.Namespace) {
	defaultable, ok := m.(Defaultable)
	if !ok {
		return
	}
	var list []Defaults
	seen := make(map[Defaults]bool)
	var add func(names []string)
	add = func(names []string) {
		for _, name := range names {
			module, _ := ctx.ModuleFromNameIn(name, namespace)
			if defaults, ok := module.(Defaults); ok && !seen[defaults] {
				seen[defaults] = true
				list = append(list, defaults)
				add(defaults.defaults().Defaults)
			}
		}
	}
	add(defaultable.defaults().Defaults)
	defaultable.applyDefaults(ctx, list)
}

// An installTarget is where a module installs: a partition, and the device, the host or both.
type installTarget struct {
	partition    partition
	device, host bool
}

func installTargetOf(m *ModuleBase) installTarget {
	return installTarget{m.installPartition(), m.DeviceSupported(), m.HostSupported()}
}

func (t installTarget) String() string {
	switch {
	case t.device && t.host:
		return string(t.partition) + " and the host"
	case t.host:
		return "the host"
	}
	return string(t.partition)
}
