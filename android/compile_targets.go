// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import "fmt"

// CompileTargets are the targets that the build compiles for, computed from the product variables
// and the machine the build runs on.
type CompileTargets struct {
	Targets                  map[OsType][]Target
	BuildOSTarget            Target // the Target for tools run on the build machine
	BuildOSCommonTarget      Target // the Target for common (java) tools run on the build machine
	AndroidCommonTarget      Target // the Target for common modules for the Android device
	AndroidFirstDeviceTarget Target // the first Target for modules for the Android device
	AndroidLFITarget         Target

	// deviceSideTargets are the Android targets of the device-side images, for the device's CPU, or
	// nil where these are the same as Targets[Android], the targets of the system-side images.
	deviceSideTargets []Target
	// archVariationsBySide maps each image side and requested arch variation to the variation used
	// on that side, see translateArchVariationToSide. It is nil with nil deviceSideTargets.
	archVariationsBySide map[ImageSide]map[string]string

	// multilibConflicts for an ArchType is true if there is earlier configured
	// device architecture with the same multilib value.
	multilibConflicts map[ArchType]bool
}

// CompileTargets returns the targets that the build compiles for, and records the configuration
// reads they were computed from.
func (c Config) CompileTargets() *CompileTargets {
	return c.compileTargets.readBy(c)
}

// computeCompileTargets computes the targets that the build compiles for from c.
func computeCompileTargets(c Config) (*CompileTargets, error) {
	buildOS := determineBuildOS(c)

	// Sets up the map of target OSes to the finer grained compilation targets
	// that are configured from the product variables.
	targets, deviceSideTargets, err := decodeTargetProductVariables(c, buildOS)
	if err != nil {
		return nil, fmt.Errorf("could not decode the targets of the product variables: %w", err)
	}

	// Make the CommonOS OsType available for all products.
	targets[CommonOS] = []Target{commonTargetMap[CommonOS.Name]}

	var archConfig []archConfig
	if c.NdkAbis() {
		archConfig = getNdkAbisConfig()
	} else if c.AmlAbis() {
		archConfig = getAmlAbisConfig()
	}

	if archConfig != nil {
		androidTargets, err := decodeAndroidArchSettings(archConfig)
		if err != nil {
			return nil, fmt.Errorf("could not decode the targets of the fixed ABI list: %w", err)
		}
		targets[Android] = androidTargets
		// Only the product's own targets have a device side. Builds for the fixed ABI lists above
		// compile every image for those.
		deviceSideTargets = nil
	}

	compileTargets := &CompileTargets{
		Targets:           targets,
		deviceSideTargets: deviceSideTargets,
		multilibConflicts: make(map[ArchType]bool),
	}

	multilib := make(map[string]bool)
	for _, target := range targets[Android] {
		if seen := multilib[target.Arch.ArchType.Multilib]; seen {
			compileTargets.multilibConflicts[target.Arch.ArchType] = true
		}
		multilib[target.Arch.ArchType.Multilib] = true
	}

	// Compilation targets for host tools.
	compileTargets.BuildOSTarget = targets[buildOS][0]
	compileTargets.BuildOSCommonTarget = getCommonTargets(targets[buildOS])[0]

	// Compilation targets for Android.
	if len(targets[Android]) > 0 {
		compileTargets.AndroidCommonTarget = getCommonTargets(targets[Android])[0]
		compileTargets.AndroidFirstDeviceTarget = FirstTarget(targets[Android], "lib64", "lib32")[0]
		compileTargets.AndroidLFITarget = findLFITarget(targets[Android])
	}

	compileTargets.archVariationsBySide, err = mapArchVariationsToSides(targets[Android], deviceSideTargets)
	if err != nil {
		return nil, fmt.Errorf("could not map the arch variations to the image sides: %w", err)
	}
	return compileTargets, nil
}
