package dexpreopt

import "android/soong/android"

func init() {
	android.InitRegistrationContext.RegisterParallelSingletonType("dexpreopt_tools_zip_singleton", dexpreoptToolsZipSingletonFactory)
}

func dexpreoptToolsZipSingletonFactory() android.Singleton {
	return &dexpreoptToolsZipSingleton{}
}

type dexpreoptToolsZipSingleton struct{}

func (s *dexpreoptToolsZipSingleton) GenerateBuildActions(ctx android.SingletonContext) {
	// The mac build doesn't build dex2oat, so create the zip file only if the build OS is linux.
	if !ctx.Config().BuildOS.Linux() {
		return
	}
	global := GetGlobalConfig(ctx)
	if global.DisablePreopt {
		return
	}
	config := globalSoongConfigFromModules(ctx)
	if config == nil {
		return
	}

	deps := android.Paths{
		ctx.Config().HostToolPath(ctx, "dexpreopt_gen"),
		ctx.Config().HostToolPath(ctx, "dexdump"),
		ctx.Config().HostToolPath(ctx, "oatdump"),
		config.Profman,
		config.Dex2oat,
		config.Aapt,
		config.SoongZip,
		config.Zip2zip,
		config.ManifestCheck,
		config.ConstructContext,
		config.UffdGcFlag,
	}

	out := android.PathForOutput(ctx, "dexpreopt_tools.zip")
	builder := android.NewRuleBuilder(pctx, ctx).SandboxDisabled()

	cmd := builder.Command().BuiltTool("soong_zip").
		Flag("-d").
		FlagWithOutput("-o ", out).
		Flag("-j")

	for _, dep := range deps {
		cmd.FlagWithInput("-f ", dep)
	}

	// This reads through a symlink to include the file it points to. This isn't great for
	// build reproducibility, will need to be revisited later.
	cmd.Text("-f $(").BuiltTool("symlink_resolver").Input(config.Dex2oat).Text(")")

	builder.Build("dexpreopt_tools_zip", "building dexpreopt_tools.zip")

	ctx.DistForGoal("droidcore", out)
}
