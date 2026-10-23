// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

// A buildActionsState is a value that the build actions of a module collect while its
// GenerateAndroidBuildActions runs. The module calls finish once GenerateAndroidBuildActions has
// returned, such as to set a provider from the collected values.
type buildActionsState interface {
	finish(ctx ModuleContext)
}

// A buildActionsStateKey is the key of a buildActionsState of type T, made by newState for each
// module on its first use.
type buildActionsStateKey[T buildActionsState] struct {
	newState func() T
}

// moduleState returns the state stored under key for the module that ctx builds.
func moduleState[T buildActionsState](ctx ModuleContext, key *buildActionsStateKey[T]) T {
	// The state stored under key was made by key.newState, so it has the type T.
	return ctx.buildActionsState(key, func() buildActionsState { return key.newState() }).(T)
}

type keyedBuildActionsState struct {
	key   any
	state buildActionsState
}

func (m *moduleContext) buildActionsState(key any, newState func() buildActionsState) buildActionsState {
	for _, stored := range m.buildActionsStates {
		if stored.key == key {
			return stored.state
		}
	}
	state := newState()
	m.buildActionsStates = append(m.buildActionsStates, keyedBuildActionsState{key: key, state: state})
	return state
}

func (m *moduleContext) finishBuildActionsStates() {
	for _, stored := range m.buildActionsStates {
		stored.state.finish(m)
	}
}
