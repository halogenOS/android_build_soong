// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

// format_build_date writes the build date, given in the form of shared.SigningDateLayout, into two
// files in UTC: as Unix time and as text. The placeholder date of a build without the signing run
// gives Unix time 0.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"android/soong/shared"
)

// dateTextLayout is the default output format of date(1) in the C locale, "%a %b %e %H:%M:%S %Z
// %Y", which the ro.build.date properties use. Source:
// https://pubs.opengroup.org/onlinepubs/9799919799/utilities/date.html (STDOUT), checked
// 2026-11-01.
const dateTextLayout = "Mon Jan _2 15:04:05 MST 2006"

func main() {
	buildDate := flag.String("build-date", "", "build date, in the form "+shared.SigningDateLayout)
	unixTimeOut := flag.String("unix-time-out", "", "file to write the date into as Unix time")
	textOut := flag.String("text-out", "", "file to write the date into as text in UTC")
	flag.Parse()

	if err := run(*buildDate, *unixTimeOut, *textOut); err != nil {
		fmt.Fprintln(os.Stderr, "format_build_date:", err)
		os.Exit(1)
	}
}

func run(buildDate, unixTimeOut, textOut string) error {
	if unixTimeOut == "" || textOut == "" {
		return fmt.Errorf("--unix-time-out and --text-out are required")
	}
	date, err := parseBuildDate(buildDate)
	if err != nil {
		return err
	}
	if err := os.WriteFile(unixTimeOut, []byte(strconv.FormatInt(date.Unix(), 10)+"\n"), 0666); err != nil {
		return fmt.Errorf("could not write the build date as Unix time: %w", err)
	}
	if err := os.WriteFile(textOut, []byte(date.Format(dateTextLayout)+"\n"), 0666); err != nil {
		return fmt.Errorf("could not write the build date as text: %w", err)
	}
	return nil
}

// parseBuildDate returns the build date in UTC, or Unix time 0 for the placeholder.
func parseBuildDate(buildDate string) (time.Time, error) {
	if buildDate == shared.SigningDatePlaceholder {
		return time.Unix(0, 0).UTC(), nil
	}
	date, err := time.Parse(shared.SigningDateLayout, buildDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("could not read the build date %q: %w", buildDate, err)
	}
	return date, nil
}
