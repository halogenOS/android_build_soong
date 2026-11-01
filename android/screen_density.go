// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import (
	"fmt"
	"strconv"
	"strings"
)

// screenDensity is a resource density qualifier with its dots per inch, the DENSITY_* constants of
// android.util.DisplayMetrics in frameworks/base/core/java/android/util/DisplayMetrics.java.
type screenDensity struct {
	name string
	dpi  uint64
}

// screenDensityBuckets are the buckets that device images are made for, in ascending order.
var screenDensityBuckets = []screenDensity{
	{"mdpi", 160},
	{"hdpi", 240},
	{"xhdpi", 320},
	{"xxhdpi", 480},
	{"xxxhdpi", 640},
}

// namedScreenDensities are the qualifiers that are no bucket.
var namedScreenDensities = []screenDensity{
	{"ldpi", 120},
	{"tvdpi", 213},
}

// bucketForDpi returns the name of the bucket nearest to dpi, the higher one at the midpoint between
// two. This gives the thresholds 200, 280, 400 and 560 of recovery_density in build/make/core/Makefile.
func bucketForDpi(dpi uint64) string {
	chosen := screenDensityBuckets[0]
	for _, bucket := range screenDensityBuckets[1:] {
		if 2*dpi >= chosen.dpi+bucket.dpi {
			chosen = bucket
		}
	}
	return chosen.name
}

func findScreenDensity(densities []screenDensity, name string) (screenDensity, bool) {
	for _, density := range densities {
		if density.name == name {
			return density, true
		}
	}
	return screenDensity{}, false
}

// ScreenDensityBucket returns the density bucket of the device's screen, such as "xxhdpi", from
// TARGET_SCREEN_DENSITY, else PRODUCT_AAPT_PREF_CONFIG, else mdpi. Either variable contains a bucket
// name, ldpi or tvdpi, or dots per inch with or without the suffix "dpi".
func ScreenDensityBucket(config Config) (string, error) {
	variable, value := "TARGET_SCREEN_DENSITY", config.ProductVariables().PartitionVarsForSoongMigrationOnlyDoNotUse.TargetScreenDensity
	if value == "" {
		variable, value = "PRODUCT_AAPT_PREF_CONFIG", config.ProductAAPTPreferredConfig()
	}
	if value == "" {
		return screenDensityBuckets[0].name, nil
	}
	if bucket, found := findScreenDensity(screenDensityBuckets, value); found {
		return bucket.name, nil
	}
	if named, found := findScreenDensity(namedScreenDensities, value); found {
		return bucketForDpi(named.dpi), nil
	}
	dpi, err := strconv.ParseUint(strings.TrimSuffix(value, "dpi"), 10, 64)
	if err != nil {
		return "", fmt.Errorf("%s is %q. Set it to a density bucket such as xxhdpi, to ldpi or tvdpi, "+
			"or to dots per inch such as 420", variable, value)
	}
	return bucketForDpi(dpi), nil
}
