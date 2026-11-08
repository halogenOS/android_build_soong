// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"fmt"
	"slices"
)

// archAndCpuVariants are the arch and CPU variants of the primary and the secondary device arch.
type archAndCpuVariants struct {
	archVariant, cpuVariant, secondaryArchVariant, secondaryCpuVariant *string
}

// deviceCpuVariants returns the variants of the device's CPU, which the device-side images
// compile for.
func deviceCpuVariants(variables ProductVariables) archAndCpuVariants {
	return archAndCpuVariants{
		archVariant:          variables.DeviceArchVariant,
		cpuVariant:           variables.DeviceCpuVariant,
		secondaryArchVariant: variables.DeviceSecondaryArchVariant,
		secondaryCpuVariant:  variables.DeviceSecondaryCpuVariant,
	}
}

// systemCpuVariants returns the variants that the system-side images compile for. A product
// without System* variants compiles every image for the device's CPU.
func systemCpuVariants(variables ProductVariables) archAndCpuVariants {
	if variables.SystemArchVariant == nil {
		return deviceCpuVariants(variables)
	}
	return archAndCpuVariants{
		archVariant:          variables.SystemArchVariant,
		cpuVariant:           variables.SystemCpuVariant,
		secondaryArchVariant: variables.SystemSecondaryArchVariant,
		secondaryCpuVariant:  variables.SystemSecondaryCpuVariant,
	}
}

func hasSameArchVariation(a, b Target) bool {
	return a.ArchVariation() == b.ArchVariation()
}

// FindTargetsForSide returns the targets that variants of the image side compile for on the OS.
func (c *config) FindTargetsForSide(os OsType, side ImageSide) []Target {
	if os == Android && side == ImageSideDevice && c.deviceSideTargets != nil {
		return c.deviceSideTargets
	}
	return c.Targets[os]
}

// findLFITarget returns the LFI target of the targets, which uses the first 64-bit target, or the
// first 32-bit one when there is none.
func findLFITarget(targets []Target) Target {
	target := FirstTarget(targets, "lib64", "lib32")[0]
	target.LFI = true
	return target
}

// sameKind reports whether the targets differ at most in their arch and CPU variants.
func sameKind(a, b Target) bool {
	return a.Arch.ArchType == b.Arch.ArchType && a.NativeBridge == b.NativeBridge && a.LFI == b.LFI
}

// mapArchVariationsToSides maps, for each image side, the arch variation of every Android target
// of both sides to the arch variation of the target of the same kind on that side. It returns nil
// when both sides compile for the same targets.
func mapArchVariationsToSides(systemTargets, deviceSideTargets []Target) (map[ImageSide]map[string]string, error) {
	if deviceSideTargets == nil {
		return nil, nil
	}
	sideTargets := map[ImageSide][]Target{
		ImageSideSystem: append(slices.Clone(systemTargets), findLFITarget(systemTargets)),
		ImageSideDevice: append(slices.Clone(deviceSideTargets), findLFITarget(deviceSideTargets)),
	}
	allTargets := slices.Concat(sideTargets[ImageSideSystem], sideTargets[ImageSideDevice])

	variations := make(map[ImageSide]map[string]string, len(sideTargets))
	for _, side := range []ImageSide{ImageSideSystem, ImageSideDevice} {
		sideVariations := make(map[string]string, len(allTargets))
		for _, target := range allTargets {
			for _, own := range sideTargets[side] {
				if !sameKind(target, own) {
					continue
				}
				if found, ok := sideVariations[target.ArchVariation()]; ok && found != own.ArchVariation() {
					return nil, fmt.Errorf("the %s side has two targets for the arch variation %s: %s and %s",
						side.name, target.ArchVariation(), found, own.ArchVariation())
				}
				sideVariations[target.ArchVariation()] = own.ArchVariation()
			}
		}
		variations[side] = sideVariations
	}
	return variations, nil
}

// translateArchVariationToSide returns the arch variation that a variant of the image side uses for
// a dependency that requests the variation. A dependency from the other side requests the variation
// of a target of that side, and this side uses its own target of the same kind instead.
func (c *config) translateArchVariationToSide(side ImageSide, variation string) string {
	if translated, ok := c.archVariationsBySide[side][variation]; ok {
		return translated
	}
	return variation
}
