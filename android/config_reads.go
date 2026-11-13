// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
)

// ConfigVariableKind names the source a configuration variable is read from.
type ConfigVariableKind struct{ name string }

var (
	ProductVariableKind     = ConfigVariableKind{"product variable"}
	EnvironmentVariableKind = ConfigVariableKind{"environment variable"}
	BuildFlagKind           = ConfigVariableKind{"build flag"}
	BuildFlagTypeKind       = ConfigVariableKind{"build flag type"}
	// A vendor variable is named by its namespace and its name, joined by a dot.
	VendorVariableKind     = ConfigVariableKind{"vendor variable"}
	VendorVariableTypeKind = ConfigVariableKind{"vendor variable type"}
	// A vendor variable namespace is read whole, with every variable in it.
	VendorNamespaceKind = ConfigVariableKind{"vendor variable namespace"}
)

var _ encoding.TextMarshaler = ConfigVariableKind{}

func (k ConfigVariableKind) MarshalText() ([]byte, error) {
	return []byte(k.name), nil
}

// ConfigVariable is one configuration variable that a module or singleton read.
type ConfigVariable struct {
	Kind ConfigVariableKind
	Name string
}

// ConfigReads records the configuration variables that one module or singleton read, each with
// the JSON encoding of the value it had, so that a later run can tell whether any of them changed.
type ConfigReads struct {
	lock   sync.Mutex
	values map[ConfigVariable]string
	// encodeErrors contains the error of each value that could not be encoded, returned by Values.
	encodeErrors []error
}

// record stores the value of variable at its first read. A nil ConfigReads belongs to a
// configuration handed out outside any module or singleton, which records nothing.
func (r *ConfigReads) record(variable ConfigVariable, value any) {
	if r == nil {
		return
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, recorded := r.values[variable]; recorded {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		r.encodeErrors = append(r.encodeErrors,
			fmt.Errorf("could not record the value of %s %s: %w", variable.Kind.name, variable.Name, err))
		return
	}
	if r.values == nil {
		r.values = make(map[ConfigVariable]string)
	}
	r.values[variable] = string(encoded)
}

// Values returns each recorded variable with the JSON encoding of the value it had when read, or
// the error of each value that could not be encoded.
func (r *ConfigReads) Values() (map[ConfigVariable]string, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	if err := errors.Join(r.encodeErrors...); err != nil {
		return nil, err
	}
	return maps.Clone(r.values), nil
}

// recordAll records every read recorded by from. A nil from belongs to a value computed outside
// any configuration, as in tests, and records nothing.
func (r *ConfigReads) recordAll(from *ConfigReads) {
	if r == nil || from == nil {
		return
	}
	from.lock.Lock()
	values, encodeErrors := maps.Clone(from.values), slices.Clone(from.encodeErrors)
	from.lock.Unlock()

	r.lock.Lock()
	defer r.lock.Unlock()
	if r.values == nil {
		r.values = make(map[ConfigVariable]string)
	}
	maps.Copy(r.values, values)
	r.encodeErrors = append(r.encodeErrors, encodeErrors...)
}

// configComputedValue is a value computed once from the configuration, with the configuration
// reads made by its computation.
type configComputedValue[T any] struct {
	value T
	reads *ConfigReads
}

// computeFromConfig returns the value computed by compute from c, with the configuration reads made
// by that computation.
func computeFromConfig[T any](c Config, compute func(Config) T) configComputedValue[T] {
	reads := &ConfigReads{}
	return configComputedValue[T]{compute(c.recordingReadsInto(reads)), reads}
}

// readBy returns the computed value and records the configuration reads of its computation into c.
func (v configComputedValue[T]) readBy(c Config) T {
	c.reads.recordAll(v.reads)
	return v.value
}

// OnceFromConfig returns the value of key, computed at the first call by compute from the
// configuration handed to it, and records the configuration reads of that computation into c at
// every call.
func OnceFromConfig[T any](c Config, key OnceKey, compute func(Config) T) T {
	return c.Once(key, func() any {
		return computeFromConfig(c, compute)
	}).(configComputedValue[T]).readBy(c)
}

// readMapEntry returns the entry of key in entries and records its read as variable, with null
// as the value of a missing entry.
func readMapEntry[V any](reads *ConfigReads, variable ConfigVariable, entries map[string]V, key string) (V, bool) {
	value, found := entries[key]
	if found {
		reads.record(variable, value)
	} else {
		reads.record(variable, nil)
	}
	return value, found
}

// productVariableSource is a configuration whose product variables are read through
// readProductVariable.
type productVariableSource interface {
	productVariablesAndReads() (*ProductVariables, *ConfigReads)
}

func (c Config) productVariablesAndReads() (*ProductVariables, *ConfigReads) {
	return &c.config.productVariables, c.reads
}

func (c DeviceConfig) productVariablesAndReads() (*ProductVariables, *ConfigReads) {
	return &c.config.productVariables, c.reads
}

// readProductVariable returns the product variable that field points to and records its read.
// field must point to a field of the product variables of source, as in
// readProductVariable(c, &c.productVariables.DeviceName).
func readProductVariable[T any](source productVariableSource, field *T) T {
	variables, reads := source.productVariablesAndReads()
	reads.record(ConfigVariable{ProductVariableKind, productVariableName(variables, field)}, *field)
	return *field
}

// readProductVariableNamed returns the product variable name of source and records its read, or
// the zero Value when the product variables have no field name.
func readProductVariableNamed(source productVariableSource, name string) reflect.Value {
	variables, reads := source.productVariablesAndReads()
	value := reflect.ValueOf(variables).Elem().FieldByName(name)
	if value.IsValid() {
		reads.record(ConfigVariable{ProductVariableKind, name}, value.Interface())
	}
	return value
}

// productVariableField identifies a field of ProductVariables, or of a struct inside it, by its
// place in memory. A struct and its first field have the same offset and differ in type.
type productVariableField struct {
	offset    uintptr
	fieldType reflect.Type
}

var productVariableNames = sync.OnceValue(func() map[productVariableField]string {
	names := make(map[productVariableField]string)
	addProductVariableNames(names, reflect.TypeFor[ProductVariables](), 0, "")
	return names
})

func addProductVariableNames(names map[productVariableField]string, structType reflect.Type, offset uintptr, prefix string) {
	for i := range structType.NumField() {
		field := structType.Field(i)
		name := prefix + field.Name
		names[productVariableField{offset + field.Offset, field.Type}] = name
		if field.Type.Kind() == reflect.Struct {
			addProductVariableNames(names, field.Type, offset+field.Offset, name+".")
		}
	}
}

func productVariableName[T any](variables *ProductVariables, field *T) string {
	offset := reflect.ValueOf(field).Pointer() - reflect.ValueOf(variables).Pointer()
	name, found := productVariableNames()[productVariableField{offset, reflect.TypeFor[T]()}]
	if !found {
		panic(fmt.Errorf("%T at offset %d is not a product variable", field, offset))
	}
	return name
}

// pathContextRecordingReads is ctx with a configuration that records the reads of one computation.
type pathContextRecordingReads struct {
	PathContext
	config Config
}

func (ctx pathContextRecordingReads) Config() Config {
	return ctx.config
}

// OnceFromPathContext is OnceFromConfig for a value computed from a PathContext. compute gets ctx
// with a configuration that records the reads of the computation.
func OnceFromPathContext[T any](ctx PathContext, key OnceKey, compute func(PathContext) T) T {
	return OnceFromConfig(ctx.Config(), key, func(c Config) T {
		return compute(pathContextRecordingReads{ctx, c})
	})
}

// moduleContextRecordingReads is ctx with a configuration that records the reads of one
// computation.
type moduleContextRecordingReads struct {
	ModuleContext
	config Config
}

func (ctx moduleContextRecordingReads) Config() Config {
	return ctx.config
}

func (ctx moduleContextRecordingReads) DeviceConfig() DeviceConfig {
	return ctx.config.DeviceConfig()
}

// OnceFromModuleContext is OnceFromConfig for a value computed from a ModuleContext. compute gets
// ctx with a configuration that records the reads of the computation.
func OnceFromModuleContext[T any](ctx ModuleContext, key OnceKey, compute func(ModuleContext) T) T {
	return OnceFromConfig(ctx.Config(), key, func(c Config) T {
		return compute(moduleContextRecordingReads{ctx, c})
	})
}
