// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"fmt"
	"slices"
)

// vendorSideImageVariations are the image variations that compile for the vendor targets: the
// vendor image, which holds the odm modules as well, the vendor ramdisk and the recovery.
var vendorSideImageVariations = []string{VendorVariation, VendorRamdiskVariation, RecoveryVariation}

// decodeVendorTargets converts the vendor arch product variables into the vendor targets: the
// device targets with the arch and CPU variants of the device's own CPU. It returns nil for a
// product without device targets or without a vendor arch variant, and every image then compiles
// for the device targets.
func decodeVendorTargets(variables ProductVariables) ([]Target, error) {
	if variables.VendorArchVariant == nil || String(variables.DeviceArch) == "" {
		return nil, nil
	}

	type vendorArch struct {
		arch, archVariant, cpuVariant *string
		abi                           []string
	}
	var targets []Target
	for _, a := range []vendorArch{
		{variables.DeviceArch, variables.VendorArchVariant, variables.VendorCpuVariant, variables.DeviceAbi},
		{variables.DeviceSecondaryArch, variables.VendorSecondaryArchVariant, variables.VendorSecondaryCpuVariant,
			variables.DeviceSecondaryAbi},
	} {
		if String(a.arch) == "" {
			continue
		}
		arch, err := decodeArch(Android, *a.arch, a.archVariant, a.cpuVariant, a.abi)
		if err != nil {
			return nil, fmt.Errorf("could not decode the vendor target of %s: %w", *a.arch, err)
		}
		targets = append(targets, Target{Os: Android, Arch: arch})
	}
	return targets, nil
}

// targetsForImage returns the targets that variants of the image variation compile for on the OS:
// the vendor targets for a vendor-side image of a product that names them, the targets of the OS
// otherwise.
func (c *config) targetsForImage(os OsType, image string) []Target {
	if c.vendorTargets != nil && slices.Contains(vendorSideImageVariations, image) {
		return c.vendorTargets
	}
	return c.Targets[os]
}

// archVariationForImage returns the arch variation that an Android variant of the image variation
// takes for a dependency asking for a variation it does not have. A dependency from an image of the
// other target set asks for the variation of its own target, and the target of the same arch in the
// set of this image takes its place. Any other variation stays as it is.
func (c *config) archVariationForImage(image, variation string) string {
	androidTargets := slices.Concat(c.Targets[Android], c.vendorTargets)
	for _, own := range c.targetsForImage(Android, image) {
		if slices.ContainsFunc(androidTargets, func(t Target) bool {
			return t.ArchVariation() == variation && t.Arch.ArchType == own.Arch.ArchType &&
				t.NativeBridge == own.NativeBridge
		}) {
			return own.ArchVariation()
		}
	}
	return variation
}
