// Copyright 2019 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package build

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"

	"android/soong/remoteexec"
	"android/soong/ui/metrics"
)

const (
	rbeLeastNProcs = 2500
	rbeLeastNFiles = 16000

	// prebuilt RBE binaries
	bootstrapCmd = "bootstrap"

	// RBE metrics proto buffer file
	rbeMetricsPBFilename = "rbe_metrics.pb"

	defaultOutDir = "out"
)

func rbeCommand(ctx Context, config Config, rbeCmd string) string {
	var cmdPath string
	if rbeDir := config.rbeDir(); rbeDir != "" {
		cmdPath = filepath.Join(rbeDir, rbeCmd)
	} else {
		ctx.Fatalf("rbe command path not found")
	}

	if _, err := os.Stat(cmdPath); err != nil && os.IsNotExist(err) {
		ctx.Fatalf("rbe command %q not found", rbeCmd)
	}

	return cmdPath
}

var defaultRBEPlatform = "container-image=" + remoteexec.DefaultImage

func getRBEVars(ctx Context, config Config) map[string]string {
	vars := map[string]string{
		"RBE_log_dir":          config.rbeProxyLogsDir(),
		"RBE_re_proxy":         config.rbeReproxy(),
		"RBE_exec_root":        config.rbeExecRoot(),
		"RBE_output_dir":       config.rbeProxyLogsDir(),
		"RBE_proxy_log_dir":    config.rbeProxyLogsDir(),
		"RBE_cache_dir":        config.rbeCacheDir(),
		"RBE_download_tmp_dir": config.rbeDownloadTmpDir(),
		// bump the maximum size a command's stdout can be before being truncated
		"RBE_max_listen_size_kb": strconv.Itoa(16 * 1024),
		"RBE_platform":           config.rbePlatform(),
	}
	if config.StartReproxy() {
		name, err := config.rbeSockAddr(absPath(ctx, config.rbeTmpDir()))
		if err != nil {
			ctx.Fatalf("Error retrieving socket address: %v", err)
			return nil
		}
		vars["RBE_server_address"] = fmt.Sprintf("unix://%v", name)
	}

	rf := 1.0
	if config.Parallel() < runtime.NumCPU() {
		rf = float64(config.Parallel()) / float64(runtime.NumCPU())
	}
	vars["RBE_local_resource_fraction"] = fmt.Sprintf("%.2f", rf)

	k, v := config.rbeAuth()
	vars[k] = v
	return vars
}

func cleanupRBELogsDir(ctx Context, config Config) {
	if !config.shouldCleanupRBELogsDir() {
		return
	}

	rbeTmpDir := config.rbeProxyLogsDir()
	if err := os.RemoveAll(rbeTmpDir); err != nil {
		fmt.Fprintln(ctx.Writer, "\033[33mUnable to remove RBE log directory: ", err, "\033[0m")
	}
}

func checkRBERequirements(ctx Context, config Config) {
	if !config.GoogleProdCredsExist() && prodCredsAuthType(config) {
		ctx.Fatalf("Unable to start RBE reproxy\nFAILED: Missing LOAS credentials.")
	}

	if u := ulimitOrFatal(ctx, config, "-u"); u < rbeLeastNProcs {
		ctx.Fatalf("max user processes is insufficient: %d; want >= %d.\n", u, rbeLeastNProcs)
	}
	if n := ulimitOrFatal(ctx, config, "-n"); n < rbeLeastNFiles {
		ctx.Fatalf("max open files is insufficient: %d; want >= %d.\n", n, rbeLeastNFiles)
	}
	if _, err := os.Stat(config.rbeProxyLogsDir()); os.IsNotExist(err) {
		if err := os.MkdirAll(config.rbeProxyLogsDir(), 0744); err != nil {
			ctx.Fatalf("Unable to create logs dir (%v) for RBE: %v", config.rbeProxyLogsDir, err)
		}
	}
}

var DEFAULT_SISO_CONFIG_DIR = "build/soong/siso_config"

func ensureSymlink(ctx Context, dir, name, target string) {
	if err := os.MkdirAll(dir, 0777); err != nil {
		ctx.Fatalf("Could not ensure that directory %q exists: %w", dir, err)
	}

	absTarget := target
	if !filepath.IsAbs(target) {
		var err error
		absTarget, err = filepath.Abs(target)
		if err != nil {
			ctx.Fatalf("Could not create absolute path for %s in %s: %w", dir, target, err)
		}
	}

	// Remove any xisting symlink.
	linkPath := filepath.Join(dir, name)
	currentTarget, err := os.Readlink(linkPath)
	if err == nil {
		if absTarget == currentTarget {
			return
		}
		if err := os.Remove(linkPath); err != nil {
			ctx.Fatalf("Failed to remove existing symlink %q: %w", linkPath, err)
		}
	} else if !os.IsNotExist(err) {
		if err := os.Remove(linkPath); err != nil {
			ctx.Fatalf("Failed to remove existing symlink %q: %w", linkPath, err)
		}
	}

	// Create the new one.
	if err := os.Symlink(absTarget, linkPath); err != nil {
		ctx.Fatalf("Failed to create symlink %q => %q: %w", linkPath, absTarget, err)
	}
}

var sisoConfigTemplate = template.Must(template.New("siso-config").Parse(`
load("@builtin//struct.star", "module")
load("main/main.star", "main")
{{ if .UseExtension -}}
load("extension/main.star", "extension")

imports = [main, extension]
{{ else -}}
imports = [main]
{{ end -}}

def init(ctx):
    vars = module(
        "config",
        {{- range $key, $value := .Config.SisoStringVars }}
        {{ $key }} = "{{ $value }}",{{ end }}
        {{- range $key, $value := .Config.SisoBoolVars }}
        {{ $key }} = {{ if $value }}True{{ else }}False{{ end }},{{ end }}
    )

    return main.generate(ctx, vars, imports)
`))

// sisoRelConfigDir makes an absolute siso config dir relative to the current
// working directory (the top of the source tree). Siso fails to load
// @config//main.star when --config_repo_dir is an absolute path, which happens
// whenever OUT_DIR is absolute, e.g. pointing outside the source tree.
func sisoRelConfigDir(ctx Context, dir string) string {
	if !filepath.IsAbs(dir) {
		return dir
	}
	cwd, err := os.Getwd()
	if err != nil {
		ctx.Verbosef("Failed to get working directory to relativize siso config dir: %v", err)
		return dir
	}
	rel, err := filepath.Rel(cwd, dir)
	if err != nil {
		ctx.Verbosef("Failed to relativize siso config dir %q: %v", dir, err)
		return dir
	}
	return rel
}

func createSisoConfigDir(ctx Context, config Config, value string) string {
	// We need to fabricate a working directory.
	confDir := filepath.Join(config.WriteDir(), "siso_config")
	if value == confDir {
		_, err := os.Stat(filepath.Join(confDir, "main.star"))
		if err != nil {
			ctx.Fatalf("${SISO_CONFIG_DIR}/main.star is missing: %w", err)
		}
		return sisoRelConfigDir(ctx, value)
	}
	templateConfig := struct {
		Config       *Config
		UseExtension bool
	}{Config: &config}
	ensureSymlink(ctx, confDir, "main", DEFAULT_SISO_CONFIG_DIR)
	if value != DEFAULT_SISO_CONFIG_DIR && value != "" {
		ensureSymlink(ctx, confDir, "extension", value)
		templateConfig.UseExtension = true
	}

	var sb bytes.Buffer
	if err := sisoConfigTemplate.Execute(&sb, templateConfig); err != nil {
		ctx.Fatalf("Failed to generate siso config:", err)
	}
	confFile := filepath.Join(confDir, "main.star")
	if err := os.WriteFile(confFile, sb.Bytes(), 0666); err != nil {
		ctx.Fatalf("Failed to write siso config to %q: %w", confFile, err)
	}
	return sisoRelConfigDir(ctx, confDir)
}

// Create a script for siso to get credentials.
// Siso will invoke ${SISO_CREDENTIAL_HELPER} with "get", so put the actual credhelper command
// invocation in `soong-convert-command`.
func createSisoCredsHelper(ctx Context, config Config) (string, error) {
	var helperPath string
	var helperArgs string

	// RBE_credentials_helper_args contains space-separated arguments for the helper
	if envArgs, ok := config.environ.Get("RBE_credentials_helper_args"); ok && envArgs != "" {
		helperArgs = envArgs
	}
	var ok bool
	if helperPath, ok = config.Environment().Get("RBE_credentials_helper"); !ok {
		helperPath = "execrel://"
	}
	if strings.HasPrefix(helperPath, "execrel://") {
		relpath, _ := strings.CutPrefix(helperPath, "execrel://")
		dir, ok := config.Environment().Get("RBE_DIR")
		if !ok {
			dir = "prebuilts/remoteexecution-client/live"
		}
		// Use the one from RBE_DIR.
		helperPath = filepath.Join(dir, relpath, "credshelper")
	}
	if helperArgs == "" {
		helperArgs = "--auth_source=automaticAuth --gcert_refresh_timeout=20"
	}
	if helperPath == "" {
		return "", fmt.Errorf("missing RBE_credentials_helper")
	}
	ctx.Verbosef("Using '%s %s' for RBE credentials helper\n", helperPath, helperArgs)
	cacheDir := config.rbeCacheDir()
	helperArgs = strings.TrimSpace(helperArgs)
	args := []string{helperPath, helperArgs, "--cache_dir", cacheDir, "-bazel_compat"}
	cmdFile := filepath.Join(cacheDir, "soong-convert-command")
	err := os.WriteFile(cmdFile, []byte(strings.Join(args, " ")), 0666)
	return "build/soong/scripts/siso-creds-helper.py", err
}

func startRBEproxy(ctx Context, config Config) {
	e := ctx.BeginTrace(metrics.RunSetupTool, "rbe_bootstrap")
	defer e.End()

	ctx.Status.Status("Starting rbe...")
	executable := config.SisoBin()
	args := []string{
		"proxy",
		"--addr", getRBEproxySocket(ctx, config),
	}
	authType, _ := config.rbeAuth()
	switch authType {
	case "RBE_credentials_helper":
		helper, err := createSisoCredsHelper(ctx, config)
		if err != nil {
			ctx.Fatalf("Failed to create credential helper script: %v\n", err)
		}
		config.environ.Set("SISO_CREDENTIAL_HELPER", helper)
	case "RBE_use_google_prod_creds":
		ctx.Printf("Using google prod credentials\n")
	default:
		config.environ.Set("SISO_CREDENTIAL_HELPER", "google-application-default")
		ctx.Printf("Using google application default credentials\n")
	}
	if instance, ok := config.environ.Get("RBE_instance"); ok {
		args = append(args, "--reapi_instance", instance)
	}
	if service, ok := config.environ.Get("RBE_service"); ok {
		// Pass the actual service to siso proxy.
		args = append(args, "--reapi_address", service)
	}
	if project := getRBEProject(ctx, config); project != "" {
		args = append(args, "--project", project)
	}

	cmd := Command(ctx, config, e, "startRbeproxy bootstrap", executable, args...)
	ctx.Printf("Starting RBE proxy\n")
	ctx.Verbosef("RBE proxy command: %s\n", cmd)
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		ctx.Fatalf("Unable to start siso proxy\nFAILED: siso proxy failed with: %v\n%s\n", err)
	}
	ctx.RBEProxyCmd = cmd
}

func getRBEproxySocket(ctx Context, config Config) string {
	service, ok := config.environ.Get("RBE_service")
	if !ok {
		ctx.Fatalf("RBE_service is not set")
	}
	config.environ.Set("RBE_actual_service", service)
	rel_socket := filepath.Join(config.OutDir(), "rbeproxy.socket")
	abs_socket, err := filepath.Abs(rel_socket)
	if err != nil {
		ctx.Fatalf("Could not resolve %s to an absolute path: %v\n", rel_socket, err)
	}
	return fmt.Sprintf("unix://%s", abs_socket)
}

func getRBEProject(ctx Context, config Config) string {
	project, found := config.environ.Get("RBE_project")
	if !found {
		// If RBE_project is not set, try RBE_metrics_project.
		project, _ = config.environ.Get("RBE_metrics_project")
	}
	if project == "" {
		ctx.Printf("No RBE project found\n")
	}
	return project
}

func startReproxy(ctx Context, config Config) {
	e := ctx.BeginTrace(metrics.RunSetupTool, "rbe_bootstrap")
	defer e.End()

	ctx.Status.Status("Starting rbe...")
	cmd := Command(ctx, config, e, "startReproxy bootstrap", rbeCommand(ctx, config, bootstrapCmd))

	if output, err := cmd.CombinedOutput(); err != nil {
		ctx.Fatalf("Unable to start RBE reproxy\nFAILED: RBE bootstrap failed with: %v\n%s\n", err, output)
	}
}

func stopRBE(ctx Context, config Config) {
	var output []byte
	var err error
	if ctx.RBEProxyCmd != nil {
		ctx.RBEProxyCmd.Kill()
	} else {
		cmd := Command(ctx, config, nil, "stopRBE bootstrap", rbeCommand(ctx, config, bootstrapCmd), "-shutdown")
		output, err = cmd.CombinedOutput()
		if err != nil {
			ctx.Fatalf("rbe bootstrap with shutdown failed with: %v\n%s\n", err, output)
		}
	}

	if !config.Environment().IsEnvTrue("ANDROID_QUIET_BUILD") && len(output) > 0 {
		ctx.PrintFinal("\n")
		ctx.PrintFinal(string(output))
	}
}

func prodCredsAuthType(config Config) bool {
	authVar, val := config.rbeAuth()
	if strings.Contains(authVar, "use_google_prod_creds") && val != "" && val != "false" {
		return true
	}
	return false
}

// Check whether proper auth exists for RBE builds run within a
// Google dev environment.
func CheckProdCreds(ctx Context, config Config) {
	if !config.IsGooglerEnvironment() {
		return
	}
	if !config.StubbyExists() && prodCredsAuthType(config) {
		fmt.Fprintln(ctx.Writer, "")
		fmt.Fprintln(ctx.Writer, fmt.Sprintf("\033[33mWARNING: %q binary not found in $PATH, follow go/build-fast-without-stubby instead for authenticating with RBE.\033[0m", "stubby"))
		fmt.Fprintln(ctx.Writer, "")
		return
	}
}

// DumpRBEMetrics creates a metrics protobuf file containing RBE related metrics.
// The protobuf file is created if RBE is enabled and the proxy service has
// started. The proxy service is shutdown in order to dump the RBE metrics to the
// protobuf file.
func DumpRBEMetrics(ctx Context, config Config, filename string) {
	e := ctx.BeginTrace(metrics.RunShutdownTool, "dump_rbe_metrics")
	defer e.End()

	// Remove the previous metrics file in case there is a failure or RBE has been
	// disable for this run.
	os.Remove(filename)

	// If RBE is not enabled then there are no metrics to generate.
	// If RBE does not require to start, the RBE proxy maybe started
	// manually for debugging purpose and can generate the metrics
	// afterwards.
	if !config.StartReproxy() {
		return
	}

	outputDir := config.rbeProxyLogsDir()
	if outputDir == "" {
		ctx.Fatal("RBE output dir variable not defined. Aborting metrics dumping.")
	}
	metricsFile := filepath.Join(outputDir, rbeMetricsPBFilename)

	// Stop the proxy first in order to generate the RBE metrics protobuf file.
	stopRBE(ctx, config)

	if metricsFile == filename {
		return
	}
	if _, err := copyFile(metricsFile, filename); err != nil {
		ctx.Fatalf("failed to copy %q to %q: %v\n", metricsFile, filename, err)
	}
}

// PrintOutDirWarning prints a warning to indicate to the user that
// setting output directory to a path other than "out" in an RBE enabled
// build can cause slow builds.
func PrintOutDirWarning(ctx Context, config Config) {
	if config.UseRBE() && config.OutDir() != defaultOutDir {
		fmt.Fprintln(ctx.Writer, "")
		fmt.Fprintln(ctx.Writer, "\033[33mWARNING:\033[0m")
		fmt.Fprintln(ctx.Writer, fmt.Sprintf("Setting OUT_DIR to a path other than %v may result in slow RBE builds.", defaultOutDir))
		fmt.Fprintln(ctx.Writer, "See http://go/android_rbe_out_dir for a workaround.")
		fmt.Fprintln(ctx.Writer, "")
	}
}

// ulimit returns ulimit result for |opt|.
// if the resource is unlimited, it returns math.MaxInt32 so that a caller do
// not need special handling of the returned value.
//
// Note that since go syscall package do not have RLIMIT_NPROC constant,
// we use bash ulimit instead.
func ulimitOrFatal(ctx Context, config Config, opt string) int {
	commandText := fmt.Sprintf("ulimit %s", opt)
	cmd := Command(ctx, config, nil, commandText, "bash", "-c", commandText)
	output := strings.TrimRight(string(cmd.CombinedOutputOrFatal()), "\n")
	ctx.Verbose(output + "\n")
	ctx.Verbose("done\n")

	if output == "unlimited" {
		return math.MaxInt32
	}
	num, err := strconv.Atoi(output)
	if err != nil {
		ctx.Fatalf("ulimit returned unexpected value: %s: %v\n", opt, err)
	}
	return num
}
