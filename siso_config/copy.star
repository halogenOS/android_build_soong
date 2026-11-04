# SPDX-FileCopyrightText: The halogenOS Project
# SPDX-License-Identifier: Apache-2.0

load("@builtin//struct.star", "module")

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

__handlers = {
    "copy_file": __copy_file,
}

def __step_config(ctx, vars, step_config):
    step_config["rules"].append({
        "name": "g.android.CpRule",
        "action": "g.android.CpRule",
        "handler": "copy_file",
    })
    return step_config

copy = module(
    "copy",
    step_config = __step_config,
    filegroups = __filegroups,
    handlers = __handlers,
)
