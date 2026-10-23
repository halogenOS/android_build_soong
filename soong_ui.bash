#!/bin/bash -eu
#
# Copyright 2017 Google Inc. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

source $(cd $(dirname $BASH_SOURCE) &> /dev/null && pwd)/../make/shell_utils.sh
require_top

# To track how long we took to startup.
case $(uname -s) in
  Darwin)
    export TRACE_BEGIN_SOONG=`$TOP/prebuilts/build-tools/path/darwin-x86/date +%s%3N`
    ;;
  *)
    export TRACE_BEGIN_SOONG=$(date +%s%N)
    ;;
esac

setup_cog_env_if_needed
set_network_file_system_type_env_var

# Save the current PWD for use in soong_ui
export ORIGINAL_PWD=${PWD}
export TOP=$(gettop)
source ${TOP}/build/soong/scripts/microfactory.bash

# --build-date starts the signing run, which writes nothing into the output
# directory of the regular build, so it runs the soong_ui of the regular build.
# Any other spelling of the option fails here, before a soong_ui is built.
signing_run=
for arg in "$@"; do
  case "${arg}" in
    --build-date=*)
      signing_run=true
      ;;
    *--build-date*)
      echo "The signing run needs --build-date=YYYYMMDD-hhmmss as one argument, got \"${arg}\"." >&2
      exit 1
      ;;
  esac
done
if [[ -z "${signing_run}" ]]; then
  soong_build_go soong_ui android/soong/cmd/soong_ui
  soong_build_go mk2rbc android/soong/mk2rbc/mk2rbc
  soong_build_go rbcrun rbcrun/rbcrun
  soong_build_go release-config android/soong/cmd/release_config/release_config
elif [[ ! -x "$(getoutdir)/soong_ui" ]]; then
  echo "The signing run runs the soong_ui of the regular build, and $(getoutdir) has none." \
    "Run the regular build first." >&2
  exit 1
fi

cd ${TOP}
exec "$(getoutdir)/soong_ui" "$@"
