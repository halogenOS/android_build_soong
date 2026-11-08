#!/usr/bin/env python
#
# Copyright (C) 2024 The Android Open Source Project
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
"""Sets whether ART uses the userfaultfd GC, from the kernel built in the tree.

The script writes the decision twice: as the dex2oat flag and as the property of build.prop.
"""

import argparse
import os
import subprocess

# The facts of the kernel build that KernelSupportsUffd() and HaveMremapDontunmap() in
# art/runtime/gc/collector/mark_compact.cc test on the device:
# * userfaultfd(2), enabled by CONFIG_USERFAULTFD;
# * the flag UFFD_USER_MODE_ONLY and the feature UFFD_FEATURE_SIGBUS of userfaultfd(2);
# * the flag MREMAP_DONTUNMAP of mremap(2).
# ART also needs MREMAP_DONTUNMAP on a shared anonymous mapping. Mainline Linux allows that from
# 5.13 on, and some older kernels have it backported, but neither the configuration nor the
# headers of a kernel show it, so only the check in mark_compact.cc tests it. On a kernel without
# it, ro.dalvik.vm.enable_uffd_gc is true while ART does not use the GC: odrefresh then finds the
# mismatch, does not use the boot and system server artifacts compiled at build time and compiles
# them again on the device.
USERFAULTFD_ENABLED = 'CONFIG_USERFAULTFD=y'
REQUIRED_MACROS = frozenset({'UFFD_USER_MODE_ONLY', 'UFFD_FEATURE_SIGBUS', 'MREMAP_DONTUNMAP'})
HEADERS = ('linux/mman.h', 'linux/userfaultfd.h')


def parse_args():
  parser = argparse.ArgumentParser()
  parser.add_argument('--kernel-config', required=True,
                      help='the configuration used to build the kernel')
  parser.add_argument('--kernel-headers', required=True,
                      help="the include directory of the kernel's user-space headers")
  parser.add_argument('--clang', required=True,
                      help='the clang that preprocesses the headers')
  parser.add_argument('--flag-out', required=True)
  parser.add_argument('--prop-out', required=True)
  return parser.parse_args()

def defined_macros(clang, kernel_headers):
  """Returns the names of the macros defined after including HEADERS."""
  source = ''.join(f'#include <{header}>\n' for header in HEADERS)
  result = subprocess.run(
      [clang, '-E', '-dM', '-nostdinc', '-isystem', kernel_headers, '-x', 'c', '-'],
      input=source, stdout=subprocess.PIPE, text=True, check=True)
  # -dM prints each macro as one line: #define NAME[(PARAMETERS)] [VALUE]
  return {line.split()[1].partition('(')[0] for line in result.stdout.splitlines()}

def kernel_supports_uffd_gc(kernel_config, clang, kernel_headers):
  with open(kernel_config, 'r') as f:
    userfaultfd = USERFAULTFD_ENABLED in f.read().splitlines()
  macros = defined_macros(clang, kernel_headers)
  return userfaultfd and REQUIRED_MACROS <= macros

def write_if_changed(path, contents):
  # Prevent the file's mtime from being changed if the contents don't change.
  # This avoids unnecessary dexpreopt reruns.
  if os.path.isfile(path):
    with open(path, 'r') as f:
      if f.read() == contents:
        return
  with open(path, 'w') as f:
    f.write(contents)

def main():
  args = parse_args()
  enabled = kernel_supports_uffd_gc(args.kernel_config, args.clang, args.kernel_headers)
  write_if_changed(args.flag_out, '--runtime-arg -Xgc:CMC' if enabled else '')
  write_if_changed(args.prop_out,
                   f"ro.dalvik.vm.enable_uffd_gc={'true' if enabled else 'false'}\n")


if __name__ == '__main__':
  main()
