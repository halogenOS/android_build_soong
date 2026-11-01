// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package shared

// SigningDatePlaceholder is the value of the build_date Ninja variable in the
// combined Ninja file. Product properties contain it where the date of the
// signing build belongs.
const SigningDatePlaceholder = "00000000-000000"

// SigningDateLayout is the form of the date of the signing build, as a layout
// of time.Format. In the signing run, siso sets build_date to the newest
// modification time of the files read by the steps of the signing run, in UTC
// and in this layout.
const SigningDateLayout = "20060102-150405"

// The Ninja variables that the signing run sets to other values than the
// regular build. soong_ui writes them into the combined Ninja file, and siso
// runs in the signing run only the steps that refer to them and the steps that
// read the outputs of those steps.
const (
	// KeysDirVariable is the directory with the keys that the build steps
	// read.
	KeysDirVariable = "keys_dir"
	// SigningDateVariable is the date of the signing build. The combined
	// Ninja file sets it to SigningDatePlaceholder.
	SigningDateVariable = "build_date"
)

// NinjaVariable is a top-level variable of a Ninja file, with its value.
type NinjaVariable struct {
	Name, Value string
}

// SigningVariables returns every signing variable with its value in a build
// with the keys in keysDir. The date is SigningDatePlaceholder.
func SigningVariables(keysDir string) []NinjaVariable {
	return []NinjaVariable{
		{Name: KeysDirVariable, Value: keysDir},
		{Name: SigningDateVariable, Value: SigningDatePlaceholder},
	}
}
