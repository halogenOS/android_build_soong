// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"reflect"
	"sync"

	"github.com/google/blueprint/proptools"
)

// errorlessEvaluatorContext evaluates selects without reporting errors. The evaluation in
// buildTypeFolderMutator only records the build type values read by selects. The module reports
// errors for a property when it reads that property itself.
type errorlessEvaluatorContext struct {
	ConfigurableEvaluatorContext
}

var _ ConfigurableEvaluatorContext = errorlessEvaluatorContext{}

func (errorlessEvaluatorContext) OtherModulePropertyErrorf(ModuleOrProxy, string, string, ...interface{}) {
}

// evaluateConfigurableProperties evaluates every configurable property in value, so that the build
// type values read by its selects are recorded before the output folder is named.
func evaluateConfigurableProperties(evaluator proptools.ConfigurableEvaluator, value reflect.Value) {
	if value.IsZero() || !mayContainConfigurable(value.Type()) {
		return
	}
	switch value.Kind() {
	case reflect.Ptr, reflect.Interface:
		evaluateConfigurableProperties(evaluator, value.Elem())
	case reflect.Slice:
		for i := range value.Len() {
			evaluateConfigurableProperties(evaluator, value.Index(i))
		}
	case reflect.Struct:
		if proptools.IsConfigurable(value.Type()) {
			// Get has a pointer receiver.
			configurable := reflect.New(value.Type())
			configurable.Elem().Set(value)
			// Evaluating the property records the build type values read by its selects. The
			// value itself is not needed.
			_ = configurable.MethodByName("Get").Call([]reflect.Value{reflect.ValueOf(evaluator)})
			return
		}
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				evaluateConfigurableProperties(evaluator, value.Field(i))
			}
		}
	}
}

var typesWithConfigurables sync.Map

func mayContainConfigurable(t reflect.Type) bool {
	if cached, ok := typesWithConfigurables.Load(t); ok {
		return cached.(bool)
	}
	var result bool
	switch t.Kind() {
	case reflect.Interface:
		// The type of the value is known only at runtime.
		result = true
	case reflect.Ptr, reflect.Slice:
		result = mayContainConfigurable(t.Elem())
	case reflect.Struct:
		result = proptools.IsConfigurable(t)
		for i := 0; !result && i < t.NumField(); i++ {
			result = t.Field(i).IsExported() && mayContainConfigurable(t.Field(i).Type)
		}
	}
	typesWithConfigurables.Store(t, result)
	return result
}
