// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package android

import "github.com/google/blueprint"

func init() {
	registerBuildDateComponents(InitRegistrationContext)
}

func registerBuildDateComponents(ctx RegistrationContext) {
	ctx.RegisterParallelSingletonType("build_date_files", buildDateFilesSingletonFactory)
}

var formatBuildDateTool = pctx.HostTool("format_build_date")

// formatBuildDate writes SigningDate in UTC as Unix time into $out and as text into $textOut. Its
// command contains build_date, so it runs again when build_date changes.
var formatBuildDate = pctx.AndroidStaticRule("formatBuildDate",
	blueprint.RuleParams{
		Command2: blueprint.NewCommand(
			formatBuildDateTool, ` --build-date=`, SigningDate.Ref(), ` --unix-time-out=$out --text-out=$textOut`),
		Description: "build date $out",
	}, "textOut")

// buildDateUnixTimePath returns the path of the file that contains the build date as Unix time.
func buildDateUnixTimePath(ctx PathContext) OutputPath {
	return PathForOutput(ctx, "build_date_unix_time.txt")
}

// buildDateTextPath returns the path of the file that contains the build date as text in UTC.
func buildDateTextPath(ctx PathContext) OutputPath {
	return PathForOutput(ctx, "build_date_text.txt")
}

// buildDateFilesSingleton writes the files that contain the build date for the build.prop files
// written by Soong and by Make.
type buildDateFilesSingleton struct{}

func buildDateFilesSingletonFactory() Singleton {
	return &buildDateFilesSingleton{}
}

func (s *buildDateFilesSingleton) GenerateBuildActions(ctx SingletonContext) {
	ctx.Build(pctx, BuildParams{
		Rule:           formatBuildDate,
		Output:         buildDateUnixTimePath(ctx),
		ImplicitOutput: buildDateTextPath(ctx),
		Args: map[string]string{
			"textOut": buildDateTextPath(ctx).String(),
		},
	})
}

func (s *buildDateFilesSingleton) MakeVars(ctx MakeVarsContext) {
	ctx.Strict("SOONG_BUILD_DATE_UNIX_TIME_FILE", buildDateUnixTimePath(ctx).String())
	ctx.Strict("SOONG_BUILD_DATE_TEXT_FILE", buildDateTextPath(ctx).String())
}
