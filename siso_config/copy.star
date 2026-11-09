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

# The metadata key of the install scripts, set by the step config and read by the handler.
__INSTALL_SCRIPTS = "install_scripts"

# Returns the script that Kati writes for a rule with commands: it joins the commands with
# " && " and wraps each in "(" and " )" when there are several.
def __kati_script(commands):
    if len(commands) == 1:
        return commands[0]
    return " && ".join(["(%s )" % command for command in commands])

# Returns the install scripts of text, the contents of install_commands.json, which contains
# the commands of the install rules written by Soong (installCommands in android/makevars.go).
def __soong_install_scripts(text):
    commands = json.decode(text)
    return [
        struct(script = commands["copy"], action = "copy"),
        struct(script = __kati_script([commands["copy"], commands["make_executable"]]), action = "copy_executable"),
        struct(script = commands["symlink"], action = "symlink"),
    ]

# Returns the install scripts of text, the contents of make_install_commands.txt, which
# contains the recipe lines of copy-file-or-link-to-new-target in build/make, one per line.
# A file cut short by an interrupted write of Kati lacks a placeholder and gives no script.
def __make_install_scripts(text):
    script = __kati_script(text.splitlines())
    if "{out}" not in script or "{src}" not in script:
        return []
    return [struct(script = script, action = "copy_regular_file")]

# The files with the commands of the install steps, below the out directory, and the readers
# of their contents. Soong and Kati write them, so they do not exist yet while siso builds
# Soong.
__INSTALL_COMMAND_FILES = [
    (path.join("soong", "install_commands.json"), __soong_install_scripts),
    ("make_install_commands.txt", __make_install_scripts),
]

# The characters that bash passes on unchanged in a word without quotes.
__LITERAL_PATH_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789/._+-,=@%:"

# Whether bash passes word on unchanged and the command reads it as a path that does not
# start with a dash.
def __is_literal_path(word):
    return word != "" and word.strip(__LITERAL_PATH_CHARS) == "" and not word.startswith("-")

# Returns the {src} of script when the step runs `bash -c` with exactly script, filled in
# for its only output, and bash passes both paths on unchanged. Returns None otherwise.
# {out} stands for the installed file and {src} for the file it is copied from, or the
# target of the symlink. Either may stand in several places of script.
def __install_source(cmd, script):
    if len(cmd.outputs) != 1 or len(cmd.args) != 3 or cmd.args[1] != "-c":
        return None
    out = cmd.outputs[0]
    if not __is_literal_path(out):
        return None
    parts = script.replace("{out}", out).split("{src}")
    command = cmd.args[2]
    if not command.startswith(parts[0]):
        return None
    rest = command[len(parts[0]):]
    src = rest[:len(rest) - len(rest.lstrip(__LITERAL_PATH_CHARS))]
    if command != src.join(parts) or not __is_literal_path(src):
        return None
    return src

# Each install action does what the script of a step does with src, and returns whether it
# did. A copied source must be an input of the step, so that siso has it ready.
def __copy(ctx, cmd, src):
    if src not in cmd.inputs:
        return False
    ctx.actions.copy(src = src, dst = cmd.outputs[0])
    return True

# chmod +x on a copied symlink changes its target, so a symlink source runs the command.
def __copy_executable(ctx, cmd, src):
    if src not in cmd.inputs or ctx.fs.is_symlink(src):
        return False
    ctx.actions.copy(src = src, dst = cmd.outputs[0], is_executable = True)
    return True

# Make links the installed file to the absolute path of a symlink target, so a symlink source
# runs the command.
def __copy_regular_file(ctx, cmd, src):
    if ctx.fs.is_symlink(src):
        return False
    return __copy(ctx, cmd, src)

def __link(ctx, cmd, target):
    ctx.actions.symlink(target = target, linkpath = cmd.outputs[0])
    return True

__INSTALL_ACTIONS = {
    "copy": __copy,
    "copy_executable": __copy_executable,
    "copy_regular_file": __copy_regular_file,
    "symlink": __link,
}

# Does what the script of an install step does when the command of the step runs one of the
# install scripts. A step whose command runs none of them runs its command.
def __install(ctx, cmd):
    for install in [struct(**entry) for entry in json.decode(ctx.metadata[__INSTALL_SCRIPTS])]:
        src = __install_source(cmd, install.script)
        if src != None and __INSTALL_ACTIONS[install.action](ctx, cmd, src):
            ctx.actions.exit(exit_status = 0)
            return

__handlers = {
    "copy_file": __copy_file,
    "install": __install,
}

# Returns the step rules that send the install steps to the install handler, one for each
# start that the install scripts have before their first path. siso compares the command
# with argv[0] shortened to its base name, `bash` for the `/bin/bash` that Kati runs.
def __install_rules(scripts):
    prefixes = {}
    for install in scripts:
        prefixes["bash -c \"" + install.script[:install.script.index("{")]] = None
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

    scripts = []
    for name, read_install_scripts in __INSTALL_COMMAND_FILES:
        file = path.join(vars.OUT_DIR, name)
        if ctx.fs.exists(file):
            scripts.extend(read_install_scripts(str(ctx.fs.read(file))))
    ctx.actions.metadata(__INSTALL_SCRIPTS, json.encode(scripts))
    step_config["rules"].extend(__install_rules(scripts))
    return step_config

copy = module(
    "copy",
    step_config = __step_config,
    filegroups = __filegroups,
    handlers = __handlers,
)
