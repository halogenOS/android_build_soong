// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

package filesystem

import "android/soong/android"

// addAvbSaltFromBuild adds a --salt computed from the build number and the build date. Builds from
// the same inputs get the same salt, and the salt changes with every release.
func addAvbSaltFromBuild(ctx android.ModuleContext, cmd *android.RuleBuilderCommand) {
	buildNumber := ctx.Config().BuildNumberFile(ctx)
	buildDate := android.BuildDateUnixTimePath(ctx)
	cmd.Textf(`--salt $(sha256sum "%s" "%s" | cut -d " " -f 1 | tr -d '\n')`,
		cmd.PathForInput(buildNumber), cmd.PathForInput(buildDate)).
		OrderOnly(buildNumber).
		Implicit(buildDate)
}
