# SPDX-FileCopyrightText: The halogenOS Project
# SPDX-License-Identifier: Apache-2.0

load("@builtin//encoding.star", "json")
load("@builtin//path.star", "path")
load("@builtin//struct.star", "module", "struct")

def __filegroups(ctx, vars):
    return {}

# The copy preserves mode bits and symlinks, as `cp -d` does.
# The inputs of a step mix the explicit input with the implicit ones, so only a step with
# a single input and a single output is copied here. Any other step runs its command.
def __copy_file(ctx, cmd):
    if len(cmd.inputs) != 1 or len(cmd.outputs) != 1:
        return
    ctx.actions.copy(src = cmd.inputs[0], dst = cmd.outputs[0])
    ctx.actions.exit(exit_status = 0)

# The metadata key of the install commands, set by the step config and read by the handler.
__INSTALL_COMMANDS = "install_commands"

# Returns the scripts that the Kati install steps run with `bash -c`, built from
# install_commands, the commands of the install rules that Soong writes to
# install_commands.json (installCommands in android/makevars.go). {out} stands for the
# installed file and {src} for the file it is copied from, or the target of the symlink.
# Kati joins the commands of a rule with " && " and wraps each in "(" and " )" when there
# are several.
def __install_scripts(install_commands):
    commands = json.decode(install_commands)
    return struct(
        copy = struct(script = commands["copy"], is_executable = False),
        copy_executable = struct(
            script = "(%s ) && (%s )" % (commands["copy"], commands["make_executable"]),
            is_executable = True,
        ),
        symlink = commands["symlink"],
    )

# The characters that bash passes on unchanged in a word without quotes.
__LITERAL_PATH_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789/._+-,=@%:"

# Whether bash passes word on unchanged and the command reads it as a path that does not
# start with a dash.
def __is_literal_path(word):
    return word != "" and word.strip(__LITERAL_PATH_CHARS) == "" and not word.startswith("-")

# Returns the {src} of script when the step runs `bash -c` with exactly script, filled in
# for its only output, and bash passes both paths on unchanged. Returns None otherwise.
def __install_source(cmd, script):
    if len(cmd.outputs) != 1 or len(cmd.args) != 3 or cmd.args[1] != "-c":
        return None
    out = cmd.outputs[0]
    if not __is_literal_path(out):
        return None
    head, tail = script.replace("{out}", out).split("{src}")
    command = cmd.args[2]
    src = command.removeprefix(head).removesuffix(tail)
    if command != head + src + tail or not __is_literal_path(src):
        return None
    return src

# Copies the file of an install step that runs the script of install, and returns whether
# it did. The source must be an input of the step, so that siso has it ready. An executable
# copy of a symlink runs its command, as chmod +x changes the target of the copied symlink.
def __copy_installed_file(ctx, cmd, install):
    src = __install_source(cmd, install.script)
    if src == None or src not in cmd.inputs:
        return False
    if install.is_executable and ctx.fs.is_symlink(src):
        return False
    ctx.actions.copy(src = src, dst = cmd.outputs[0], is_executable = install.is_executable)
    return True

# Makes the symlink of an install step that runs script, and returns whether it did.
def __link_installed_symlink(ctx, cmd, script):
    target = __install_source(cmd, script)
    if target == None:
        return False
    ctx.actions.symlink(target = target, linkpath = cmd.outputs[0])
    return True

# Copies the file, or makes the symlink, of an install step whose command runs one of the
# install scripts. A step whose command runs none of them runs its command.
def __install(ctx, cmd):
    scripts = __install_scripts(ctx.metadata[__INSTALL_COMMANDS])
    if (__copy_installed_file(ctx, cmd, scripts.copy) or
        __copy_installed_file(ctx, cmd, scripts.copy_executable) or
        __link_installed_symlink(ctx, cmd, scripts.symlink)):
        ctx.actions.exit(exit_status = 0)

__handlers = {
    "copy_file": __copy_file,
    "install": __install,
}

# Returns the step rules that send the install steps to the install handler, one for each
# start that the install scripts have before their first path. siso compares the command
# with argv[0] shortened to its base name, `bash` for the `/bin/bash` that Kati runs.
def __install_rules(scripts):
    prefixes = {}
    for script in [scripts.copy.script, scripts.copy_executable.script, scripts.symlink]:
        prefixes["bash -c \"" + script[:script.index("{")]] = None
    return [
        {
            "name": "kati.install.%d" % i,
            "command_prefix": prefix,
            "handler": "install",
        }
        for i, prefix in enumerate(prefixes)
    ]

def __step_config(ctx, vars, step_config):
    step_config["rules"].append({
        "name": "g.android.CpRule",
        "action": "g.android.CpRule",
        "handler": "copy_file",
    })

    # Soong writes the file, so it does not exist yet while siso builds Soong.
    install_commands_file = path.join(vars.OUT_DIR, "soong", "install_commands.json")
    if ctx.fs.exists(install_commands_file):
        install_commands = str(ctx.fs.read(install_commands_file))
        ctx.actions.metadata(__INSTALL_COMMANDS, install_commands)
        step_config["rules"].extend(__install_rules(__install_scripts(install_commands)))
    return step_config

copy = module(
    "copy",
    step_config = __step_config,
    filegroups = __filegroups,
    handlers = __handlers,
)
