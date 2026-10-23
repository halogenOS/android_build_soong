// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package shared

// SigningDatePlaceholder is the value of the build_date Ninja variable in a
// build without --build-date. Product properties contain it where the date of
// the signing build belongs.
const SigningDatePlaceholder = "00000000-000000"

// The Ninja variables that the signing run sets to other values than the
// regular build. soong_ui writes them into the combined Ninja file, and siso
// runs in the signing run only the steps that refer to them and the steps that
// read the outputs of those steps.
const (
	// KeysDirVariable is the directory with the keys that the build steps
	// read.
	KeysDirVariable = "keys_dir"
	// SigningDateVariable is the date of the signing build, or
	// SigningDatePlaceholder.
	SigningDateVariable = "build_date"
)

// NinjaVariable is a top-level variable of a Ninja file, with its value.
type NinjaVariable struct {
	Name, Value string
}

// SigningValues are the values of the signing variables in one build.
type SigningValues struct {
	KeysDir string
	Date    string
}

// Variables returns every signing variable with its value in v.
func (v SigningValues) Variables() []NinjaVariable {
	return []NinjaVariable{
		{Name: KeysDirVariable, Value: v.KeysDir},
		{Name: SigningDateVariable, Value: v.Date},
	}
}
