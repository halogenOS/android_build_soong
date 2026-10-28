// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"android/soong/shared"
	"android/soong/ui/metrics"
)

// The signing run builds the goals that need signing on the Ninja files and the
// siso state of the regular build in OUT_DIR. It runs neither Soong nor Kati,
// and writes everything into the signed directory, which WriteDir returns in
// that run.

// signedProductOutVar is the dump variable with the product output directory
// of the signing run.
const signedProductOutVar = "SIGNED_PRODUCT_OUT"

// signedProductOut returns the place of productOut, the product output
// directory of the regular build, in the signed directory.
func (c *configImpl) signedProductOut(productOut string) (string, error) {
	rel, err := filepath.Rel(c.OutDir(), productOut)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("PRODUCT_OUT %q is not in the output directory %q", productOut, c.OutDir())
	}
	return filepath.Join(c.signedOutDir(), rel), nil
}

// BuildSigningRun builds the goals of the signing run, or the default goals of
// the graph when it has none.
func BuildSigningRun(ctx Context, config Config) {
	ctx.Verboseln("Starting the signing run with args:", config.Arguments())
	ctx.Verboseln("Environment:", config.Environment().Environ())

	e := ctx.BeginTrace(metrics.Total, "total")
	defer e.End()

	checkSigningRunConfig(ctx, config)

	// A regular build must not change the files that the signing run reads.
	regularLock := lockRegularOutDir(ctx, config)
	defer regularLock.Unlock()
	buildLock := BecomeSingletonOrFail(ctx, config)
	defer buildLock.Unlock()

	checkBuildInfo(ctx, config)

	SetupTempDir(ctx, config)
	variables := signingVariables(ctx, config)
	SetupPath(ctx, config)
	runMakeProductConfig(ctx, config)
	loadRegularKatiSuffix(ctx, config)
	writeCombinedBuildNinjaFile(ctx, config, variables)

	// siso continues from the state of the regular build, and runs into the
	// signed directory the steps that refer to the signing variables and the
	// steps that read their outputs. Every other step that the goals need has
	// to be current in the regular build. The goals in the product
	// configuration of the regular build do not apply. siso sets the date to
	// the newest modification time of the files that those steps read and
	// that none of them writes, so a run on unchanged files runs no step.
	args := []string{
		deltaVariablesArg(variables),
		deltaInputTimeArg(),
		"-delta_base_dir", absPath(ctx, config.OutDir()),
		"-delta_dir", absPath(ctx, config.WriteDir()),
	}
	args = append(args, wholeDirOutputsArgs(ctx, config)...)
	runNinja(ctx, config, append(args, config.Arguments()...))
}

// wholeDirOutputsArgs returns the siso arguments that pass, one flag for each, the primary files
// of the installs of the regular build that extract an extra files zip into the directory that
// contains the primary file, or none when the regular build has no such install. Such an install
// declares only the primary file, so siso copies the whole directory from the regular build where a
// step of the signing run reads that primary file in the signed directory.
func wholeDirOutputsArgs(ctx Context, config Config) []string {
	var primaries []string
	for _, entry := range readRegularBuildList(ctx, config, config.SoongReplacedInstallDirs(), readReplacedInstallDirs) {
		primaries = append(primaries, entry.Primary)
	}
	primaries = append(primaries, readRegularBuildList(ctx, config, config.SoongExtractedZipInstalls(), readExtractedZipInstalls)...)
	if len(primaries) == 0 {
		return nil
	}
	requireSisoCapabilities(ctx, config, "The regular build has installs that extract an extra files zip next to "+
		"the one file they declare, and the signing run copies their directories as a whole.", sisoDeltaWholeDirOutputs)
	productOut := absPath(ctx, config.ProductOut())
	var args []string
	for _, primary := range primaries {
		args = append(args, "-delta_whole_dir_output", filepath.Join(productOut, primary))
	}
	return args
}

// readRegularBuildList reads with read the list in file, which soong_build wrote for the regular
// build, and fails the signing run when that does not work.
func readRegularBuildList[T any](ctx Context, config Config, file string, read func(string) ([]T, error)) []T {
	entries, err := read(file)
	if err != nil {
		ctx.Fatalf("The signing run builds on the regular build in %s. %s", config.OutDir(),
			describeInstallZipListError(file, err, "Run the regular build first, which writes the list."))
	}
	return entries
}

// signingVariables links the keys of this build and returns the signing
// variables with their values in this build.
func signingVariables(ctx Context, config Config) []shared.NinjaVariable {
	return shared.SigningVariables(linkSigningKeys(ctx, config))
}

// deltaVariablesArg returns the siso argument that passes the names of
// variables, the variables that the signing run sets to other values.
func deltaVariablesArg(variables []shared.NinjaVariable) string {
	names := make([]string, len(variables))
	for i, variable := range variables {
		names[i] = variable.Name
	}
	return "-delta_variables=" + strings.Join(names, ",")
}

// deltaInputTimeArg returns the siso argument that makes siso set the date of
// the signing run to the newest modification time of the files that its steps
// read.
func deltaInputTimeArg() string {
	return "-delta_input_time=" + shared.SigningDateVariable + ":" + shared.SigningDateLayout
}

// lockRegularOutDir locks OUT_DIR with the lock file of the regular build,
// which it opens without creating it.
func lockRegularOutDir(ctx Context, config Config) *fileLock {
	file, err := os.Open(filepath.Join(config.OutDir(), ".lock"))
	if err != nil {
		ctx.Fatalf("The signing run builds on the regular build in %s, and could not open its lock file: %v. "+
			"Run the regular build first.", config.OutDir(), err)
	}
	lock := &fileLock{File: file}
	waitForLock(ctx, lock)
	return lock
}

// checkSigningRunConfig fails the start of the signing run when its
// configuration would make it write into OUT_DIR, or when siso cannot build the
// delta builds or set the date of the signing run.
func checkSigningRunConfig(ctx Context, config Config) {
	if config.Dist() {
		ctx.Fatalf("The signing run writes nothing into OUT_DIR, and dist writes there. Leave out dist.")
	}
	if config.IsActionSandboxedBuild() {
		ctx.Fatalf("The signing run writes nothing into OUT_DIR, and action sandboxing writes into %s. "+
			"Unset SOONG_ACTION_SANDBOXING.", config.SoongOutDir())
	}
	checkSisoDeltaVariables(ctx, config)
	requireSisoCapabilities(ctx, config, "The date of the signing run comes from the newest file that the steps of "+
		"the run read, and siso sets it.", sisoDeltaInputTime)
}

// checkSisoDeltaVariables fails the start of the signing run and of a regular
// build with --signing-goals when the build cannot run the siso that selects
// the steps of the signing run by the signing variables.
func checkSisoDeltaVariables(ctx Context, config Config) {
	if config.ninjaCommand != NINJA_SISO {
		ctx.Fatalf("The signing run and the regular build with --signing-goals build with siso, "+
			"and SOONG_NINJA selects %s. Leave SOONG_NINJA unset, or set it to siso.", config.ninjaCommand)
	}
	requireSisoCapabilities(ctx, config, "The signing run and the regular build with --signing-goals need a siso "+
		"that selects the steps of the signing run by the signing variables.", sisoAbsoluteOutputs, sisoDeltaVariables)
}

// requireSisoCapabilities fails the build when the siso that it runs does not
// show every one of capabilities in its version answer. need is the sentence
// that tells what the build needs them for.
func requireSisoCapabilities(ctx Context, config Config, need string, capabilities ...toolCapability) {
	siso := config.sisoTool()
	if !slices.ContainsFunc(capabilities, func(capability toolCapability) bool { return !siso.supports(capability) }) {
		return
	}
	shown := make([]string, len(capabilities))
	for i, capability := range capabilities {
		shown[i] = "+" + capability.name
	}
	ctx.Fatalf("%s The siso at %s cannot do that. Put a siso built from build/siso on PATH, whose version shows %s.",
		need, siso.path, strings.Join(shown, " and "))
}

// checkBuildInfo fails the signing run when the information about the build
// differs from what the regular build wrote into OUT_DIR, which the steps of
// the signing run read.
func checkBuildInfo(ctx Context, config Config) {
	for _, file := range buildInfoFiles(ctx, config) {
		written, err := os.ReadFile(file.path)
		if err != nil {
			ctx.Fatalf("The signing run builds on the regular build in %s, and could not read %s of it: %v. "+
				"Run the regular build first.", config.OutDir(), file.path, err)
		}
		if string(written) != file.value {
			ctx.Fatalf("The regular build wrote %q into %s, and the signing run has %q. "+
				"Run the signing run with the environment of the regular build.", written, file.path, file.value)
		}
	}
}

// loadRegularKatiSuffix sets the Kati suffix of the last regular build, which
// selects the Ninja files that the signing run builds on.
func loadRegularKatiSuffix(ctx Context, config Config) {
	suffix, err := os.ReadFile(config.LastKatiSuffixFile())
	if err != nil {
		ctx.Fatalf("The signing run builds on the regular build in %s, and found no Kati suffix of it: %v. "+
			"Run the regular build first.", config.OutDir(), err)
	}
	config.SetKatiSuffix(string(suffix))
}

// lunchTarget returns the lunch target that a build runs for, from its
// environment.
func lunchTarget(config Config) string {
	get := func(name string) string {
		value, _ := config.environ.Get(name)
		return value
	}
	return strings.Join([]string{get("TARGET_PRODUCT"), get("TARGET_RELEASE"), get("TARGET_BUILD_VARIANT")}, "-")
}

// productConfigRecord is the product configuration of the regular build. The
// signing run reads it in place of running the product configuration, which
// writes into OUT_DIR.
type productConfigRecord struct {
	Lunch    string
	MakeVars map[string]string
}

func (c *configImpl) productConfigRecordFile() string {
	return filepath.Join(c.OutDir(), "product_config_vars.json")
}

// recordProductConfig writes the product configuration of the regular build,
// with the Make variables makeVars that it dumped.
func recordProductConfig(ctx Context, config Config, makeVars map[string]string) {
	data, err := json.Marshal(productConfigRecord{Lunch: lunchTarget(config), MakeVars: makeVars})
	if err != nil {
		ctx.Fatalln("Failed to encode the product configuration:", err)
	}
	writeValueIfChanged(ctx, config.productConfigRecordFile(), string(data))
}

// readRecordedProductConfig returns the Make variables that the product
// configuration of the regular build dumped. It fails when that build ran for
// another lunch target.
func readRecordedProductConfig(ctx Context, config Config) map[string]string {
	file := config.productConfigRecordFile()
	data, err := os.ReadFile(file)
	if err != nil {
		ctx.Fatalf("The signing run builds on the regular build in %s, and found no product configuration of it: "+
			"%v. Run the regular build first.", config.OutDir(), err)
	}
	var record productConfigRecord
	if err := json.Unmarshal(data, &record); err != nil {
		ctx.Fatalf("Failed to read the product configuration in %s: %v", file, err)
	}
	if lunch := lunchTarget(config); record.Lunch != lunch {
		ctx.Fatalf("The signing run builds on the regular build in %s, which ran for %s, and this run is for %s. "+
			"Run the regular build of %s first.", config.OutDir(), record.Lunch, lunch, lunch)
	}
	return record.MakeVars
}
