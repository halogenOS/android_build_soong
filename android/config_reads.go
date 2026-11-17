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
	"strings"
	"sync"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

// ConfigVariableKind names the source a configuration variable is read from.
type ConfigVariableKind struct{ name string }

var (
	ProductVariableKind     = ConfigVariableKind{"product variable"}
	EnvironmentVariableKind = ConfigVariableKind{"environment variable"}
	BuildFlagKind           = ConfigVariableKind{"build flag"}
	BuildFlagTypeKind       = ConfigVariableKind{"build flag type"}
	// A vendor variable is named by its namespace and its name.
	VendorVariableKind     = ConfigVariableKind{"vendor variable"}
	VendorVariableTypeKind = ConfigVariableKind{"vendor variable type"}
	// A vendor variable namespace is read whole, with every variable in it.
	VendorNamespaceKind = ConfigVariableKind{"vendor variable namespace"}
	// The dexpreopt global config is read whole, by the hash of its contents, without a name.
	DexpreoptGlobalConfigKind = ConfigVariableKind{"dexpreopt global config"}
	// The product variables are read whole, without a name.
	ProductVariablesKind = ConfigVariableKind{"product variables"}
)

// configVariableReaders contains, for each kind, the accessor that reads and records the current
// value of a variable of that kind.
var configVariableReaders = map[ConfigVariableKind]func(c Config, variable ConfigVariable){
	ProductVariableKind:     func(c Config, v ConfigVariable) { readProductVariableAtPath(c, v.Name) },
	EnvironmentVariableKind: func(c Config, v ConfigVariable) { c.Getenv(v.Name) },
	BuildFlagKind:           func(c Config, v ConfigVariable) { c.GetBuildFlag(v.Name) },
	BuildFlagTypeKind:       func(c Config, v ConfigVariable) { c.GetBuildFlagType(v.Name) },
	VendorVariableKind:      func(c Config, v ConfigVariable) { c.VendorVariable(v.Namespace, v.Name) },
	VendorVariableTypeKind:  func(c Config, v ConfigVariable) { c.VendorVariableType(v.Namespace, v.Name) },
	VendorNamespaceKind:     func(c Config, v ConfigVariable) { c.VendorConfig(v.Name) },
	DexpreoptGlobalConfigKind: func(c Config, _ ConfigVariable) {
		c.recordDexpreoptGlobalConfigRead()
	},
	ProductVariablesKind: func(c Config, _ ConfigVariable) { c.ProductVariables() },
}

var _ encoding.TextMarshaler = ConfigVariableKind{}
var _ encoding.TextUnmarshaler = (*ConfigVariableKind)(nil)

func (k ConfigVariableKind) MarshalText() ([]byte, error) {
	return []byte(k.name), nil
}

func (k *ConfigVariableKind) UnmarshalText(text []byte) error {
	for kind := range configVariableReaders {
		if kind.name == string(text) {
			*k = kind
			return nil
		}
	}
	return fmt.Errorf("%q is not a kind of configuration variable", text)
}

// ConfigVariable is one configuration variable that a module or singleton read.
type ConfigVariable struct {
	Kind ConfigVariableKind
	// Namespace is the namespace of a vendor variable or of its type, and empty for other kinds.
	Namespace string `json:",omitempty"`
	Name      string
	// Generic is true for a variable read from the generic configuration of modules that use it.
	Generic bool `json:",omitempty"`
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

// configValuesRead returns the reads recorded by r as the cache stores them, sorted by key: each
// variable encoded as JSON, with the hash of the JSON encoding of its value.
func (r *ConfigReads) configValuesRead() ([]blueprint.ConfigValueRead, error) {
	values, err := r.Values()
	if err != nil {
		return nil, err
	}
	reads := make([]blueprint.ConfigValueRead, 0, len(values))
	for variable, value := range values {
		key, err := json.Marshal(variable)
		if err != nil {
			return nil, fmt.Errorf("could not encode %s %s: %w", variable.Kind.name, variable.Name, err)
		}
		hash, err := proptools.CalculateHashReflection(value)
		if err != nil {
			return nil, fmt.Errorf("could not hash the value of %s %s: %w", variable.Kind.name, variable.Name, err)
		}
		reads = append(reads, blueprint.ConfigValueRead{Key: string(key), Hash: hash})
	}
	slices.SortFunc(reads, func(a, b blueprint.ConfigValueRead) int { return strings.Compare(a.Key, b.Key) })
	return reads, nil
}

var _ blueprint.ConfigValueHasher = Config{}

// configValueHashOnceKey is the key of the hash of the current value of the configuration variable
// encoded in it.
type configValueHashOnceKey string

type configValueHashResult struct {
	hash proptools.Hash
	err  error
}

// ConfigValueHash returns the hash of the current value of the configuration variable that key
// encodes, as ConfigReads returns it for the cache.
func (c Config) ConfigValueHash(key string) (proptools.Hash, error) {
	result := c.Once(NewCustomOnceKey(configValueHashOnceKey(key)), func() any {
		hash, err := c.hashCurrentConfigValue(key)
		return configValueHashResult{hash, err}
	}).(configValueHashResult)
	return result.hash, result.err
}

func (c Config) hashCurrentConfigValue(key string) (proptools.Hash, error) {
	var variable ConfigVariable
	if err := json.Unmarshal([]byte(key), &variable); err != nil {
		return proptools.Hash{}, fmt.Errorf("could not decode the configuration variable %s: %w", key, err)
	}
	source := c
	if variable.Generic {
		source = c.genericConfig()
	}
	reads := &ConfigReads{}
	configVariableReaders[variable.Kind](source.recordingReadsInto(reads), variable)
	current, err := reads.configValuesRead()
	if err != nil {
		return proptools.Hash{}, err
	}
	if len(current) != 1 || current[0].Key != key {
		return proptools.Hash{}, fmt.Errorf("the configuration has no variable %s", key)
	}
	return current[0].Hash, nil
}

// configReadRecorder records the reads made through one configuration, the main or the generic
// one, into the reads of a module or singleton.
type configReadRecorder struct {
	reads   *ConfigReads
	generic bool
}

func (c Config) readRecorder() configReadRecorder {
	return configReadRecorder{c.reads, c.isGeneric}
}

func (c DeviceConfig) readRecorder() configReadRecorder {
	return configReadRecorder{c.reads, c.config.isGeneric}
}

// record records the read of variable with value as a read of the configuration it was made through.
func (r configReadRecorder) record(variable ConfigVariable, value any) {
	variable.Generic = r.generic
	r.reads.record(variable, value)
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

// ReadOnceFromConfig returns the value of key computed by an earlier call of OnceFromConfig, and
// records the configuration reads of its computation into c.
func ReadOnceFromConfig[T any](c Config, key OnceKey) T {
	return c.Get(key).(configComputedValue[T]).readBy(c)
}

// readMapEntry returns the entry of key in entries and records its read as variable, with null
// as the value of a missing entry.
func readMapEntry[V any](reads configReadRecorder, variable ConfigVariable, entries map[string]V, key string) (V, bool) {
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
	productVariablesAndReads() (*ProductVariables, configReadRecorder)
}

func (c Config) productVariablesAndReads() (*ProductVariables, configReadRecorder) {
	return &c.config.productVariables, c.readRecorder()
}

func (c DeviceConfig) productVariablesAndReads() (*ProductVariables, configReadRecorder) {
	return &c.config.productVariables, c.readRecorder()
}

// readProductVariable returns the product variable that field points to and records its read.
// field must point to a field of the product variables of source, as in
// readProductVariable(c, &c.productVariables.DeviceName).
func readProductVariable[T any](source productVariableSource, field *T) T {
	variables, reads := source.productVariablesAndReads()
	reads.record(ConfigVariable{Kind: ProductVariableKind, Name: productVariableName(variables, field)}, *field)
	return *field
}

// readProductVariableAtPath records the read of the product variable at path, the names of the
// fields leading to it joined by dots as productVariableName writes them. It records nothing where
// the product variables have no field at path.
func readProductVariableAtPath(source productVariableSource, path string) {
	variables, reads := source.productVariablesAndReads()
	value := reflect.ValueOf(variables).Elem()
	for _, name := range strings.Split(path, ".") {
		if value = value.FieldByName(name); !value.IsValid() {
			return
		}
	}
	reads.record(ConfigVariable{Kind: ProductVariableKind, Name: path}, value.Interface())
}

// readProductVariableNamed returns the product variable name of source and records its read, or
// the zero Value when the product variables have no field name.
func readProductVariableNamed(source productVariableSource, name string) reflect.Value {
	variables, reads := source.productVariablesAndReads()
	value := reflect.ValueOf(variables).Elem().FieldByName(name)
	if value.IsValid() {
		reads.record(ConfigVariable{Kind: ProductVariableKind, Name: name}, value.Interface())
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
// Errorf reports path errors found by the computation to ctx.
type pathContextRecordingReads struct {
	PathContext
	config Config
}

var _ errorfContext = pathContextRecordingReads{}

func (ctx pathContextRecordingReads) Config() Config {
	return ctx.config
}

func (ctx pathContextRecordingReads) Errorf(format string, args ...interface{}) {
	ReportPathErrorf(ctx.PathContext, format, args...)
}

// pathContextRecordingReadsWithMissingDependencies is pathContextRecordingReads with
// AddMissingDependencies of ctx.
type pathContextRecordingReadsWithMissingDependencies struct {
	pathContextRecordingReads
	AddMissingDependenciesContext
}

var _ AddMissingDependenciesContext = pathContextRecordingReadsWithMissingDependencies{}

// newPathContextRecordingReads returns ctx with config, which records the reads made by one
// computation. The result has AddMissingDependencies only when ctx has it, because callers check
// for that method by type.
func newPathContextRecordingReads(ctx PathContext, config Config) PathContext {
	recording := pathContextRecordingReads{ctx, config}
	if missing, ok := ctx.(AddMissingDependenciesContext); ok {
		return pathContextRecordingReadsWithMissingDependencies{recording, missing}
	}
	return recording
}

// OnceFromPathContext is OnceFromConfig for a value computed from a PathContext. compute gets ctx
// with a configuration that records the reads of the computation.
func OnceFromPathContext[T any](ctx PathContext, key OnceKey, compute func(PathContext) T) T {
	return OnceFromConfig(ctx.Config(), key, func(c Config) T {
		return compute(newPathContextRecordingReads(ctx, c))
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

// loadHookContextRecordingReads is ctx with a configuration that records the reads of one
// computation.
type loadHookContextRecordingReads struct {
	LoadHookContext
	config Config
}

func (ctx loadHookContextRecordingReads) Config() Config {
	return ctx.config
}

func (ctx loadHookContextRecordingReads) DeviceConfig() DeviceConfig {
	return ctx.config.DeviceConfig()
}

// OnceFromLoadHookContext is OnceFromConfig for a value computed from a LoadHookContext. compute
// gets ctx with a configuration that records the reads of the computation.
func OnceFromLoadHookContext[T any](ctx LoadHookContext, key OnceKey, compute func(LoadHookContext) T) T {
	return OnceFromConfig(ctx.Config(), key, func(c Config) T {
		return compute(loadHookContextRecordingReads{ctx, c})
	})
}
