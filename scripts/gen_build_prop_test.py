#!/usr/bin/env python3
# SPDX-FileCopyrightText: The halogenOS Project
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for gen_build_prop.py."""

import argparse
import contextlib
import io
import unittest

from gen_build_prop import build_prop


class BuildPropTest(unittest.TestCase):
  def test_build_date_in_written_properties(self):
    args = argparse.Namespace(
        config={
            "PRODUCT_PRODUCT_PROPERTIES": [
                "ro.example.version=example-17.0-00000000-000000-UNOFFICIAL",
                "ro.example.build_type=UNOFFICIAL",
            ],
        },
        prop_files=[],
        build_broken_dup_sysprop=False,
        build_date="20261023-143000",
        build_date_placeholder="00000000-000000",
    )

    out = io.StringIO()
    with contextlib.redirect_stdout(out):
      build_prop(args, gen_build_info=False, gen_common_build_props=False,
                 variables=["PRODUCT_PRODUCT_PROPERTIES"])

    self.assertIn("ro.example.version=example-17.0-20261023-143000-UNOFFICIAL\n"
                  "ro.example.build_type=UNOFFICIAL\n", out.getvalue())


if __name__ == "__main__":
  unittest.main(verbosity=2)
