#!/bin/bash -eu

set -o pipefail

HARDWIRED_MOCK_TOP=
# Uncomment this to be able to view the source tree after a test is run
# HARDWIRED_MOCK_TOP=/tmp/td

REAL_TOP="$(readlink -f "$(dirname "$0")"/../../..)"

function make_mock_top {
  mock=$(mktemp -t -d st.XXXXXX)
  echo "$mock"
}

MOCK_TOP_TO_CLEAN_UP=()
WARMED_UP_MOCK_TOP_TO_CLEAN_UP=()
if [[ -n "$HARDWIRED_MOCK_TOP" ]]; then
  MOCK_TOP="$HARDWIRED_MOCK_TOP"
elif [[ -z "${MOCK_TOP:-}" ]]; then
  MOCK_TOP=$(make_mock_top)
  MOCK_TOP_TO_CLEAN_UP+=("$MOCK_TOP")
fi

function warmup_mock_top {
  info "Warming up mock top ..."
  info "Mock top warmup archive: $WARMED_UP_MOCK_TOP"
  cleanup_mock_top
  mkdir -p "$MOCK_TOP"
  cd "$MOCK_TOP"

  create_mock_soong
  run_soong

  tar cf "$WARMED_UP_MOCK_TOP" --exclude out/.path_interposer_log *
}

function extend_mock_top {
  local old_mock_top="$WARMED_UP_MOCK_TOP"
  WARMED_UP_MOCK_TOP=$(mktemp -t soong_integration_tests_warmup.tar.XXXXXX)
  WARMED_UP_MOCK_TOP_TO_CLEAN_UP+=("$WARMED_UP_MOCK_TOP")

  info "Extending mock top with $1 ..."
  info "New mock top archive: $WARMED_UP_MOCK_TOP"

  cleanup_mock_top
  mkdir -p "$MOCK_TOP"
  cd "$MOCK_TOP"

  tar xf "$old_mock_top"
  $1
  tar cf "$WARMED_UP_MOCK_TOP" --exclude out/.path_interposer_log *
}

function cleanup_mock_top {
  cd /
  if [[ -n "$MOCK_TOP" && -d "$MOCK_TOP" ]]; then
    rm -fr "$MOCK_TOP"
  fi
}

function cleanup_all {
  cd /
  if [[ -n "${MOCK_TOP_TO_CLEAN_UP:-}" ]]; then
    for m in ${MOCK_TOP_TO_CLEAN_UP[@]}; do
      if [[ -d "$m" ]]; then
        rm -fr "$m"
      fi
    done
  fi
  if [[ -n "${WARMED_UP_MOCK_TOP_TO_CLEAN_UP:-}" ]]; then
    for m in ${WARMED_UP_MOCK_TOP_TO_CLEAN_UP[@]}; do
      if [[ -f "$m" ]]; then
        rm -fr "$m"
      fi
    done
  fi
}

function info {
  echo -e "\e[92;1m[TEST HARNESS INFO]\e[0m" "$*"
}

function fail {
  echo -e "\e[91;1mFAILED:\e[0m" "$*"
  exit 1
}

function copy_directory {
  local dir="$1"
  local -r parent="$(dirname "$dir")"

  mkdir -p "$MOCK_TOP/$parent"
  cp -R "$REAL_TOP/$dir" "$MOCK_TOP/$parent"
}

function delete_directory {
  rm -rf "$MOCK_TOP/$1"
}

function symlink_file {
  local file="$1"

  mkdir -p "$MOCK_TOP/$(dirname "$file")"
  ln -s "$REAL_TOP/$file" "$MOCK_TOP/$file"
}

function symlink_directory {
  local dir="$1"

  mkdir -p "$MOCK_TOP/$dir"
  # We need to symlink the contents of the directory individually instead of
  # using one symlink for the whole directory because finder.go doesn't follow
  # symlinks when looking for Android.bp files
  for i in "$REAL_TOP/$dir"/*; do
    i=$(basename "$i")
    local target="$MOCK_TOP/$dir/$i"
    local source="$REAL_TOP/$dir/$i"

    if [[ -e "$target" ]]; then
      if [[ ! -d "$source" || ! -d "$target" ]]; then
        fail "Trying to symlink $dir twice"
      fi
    else
      ln -s "$REAL_TOP/$dir/$i" "$MOCK_TOP/$dir/$i";
    fi
  done
}

function create_mock_soong {
  copy_directory build/blueprint
  copy_directory build/soong
  copy_directory build/make

  symlink_directory external/compiler-rt
  symlink_directory external/go-cmp
  symlink_directory external/golang-protobuf
  symlink_directory external/licenseclassifier
  symlink_directory external/pogreb
  symlink_directory external/protobuf
  symlink_directory external/python
  symlink_directory external/spdx-tools
  symlink_directory external/sqlite
  symlink_directory external/starlark-go
  symlink_directory prebuilts/build-tools
  symlink_directory prebuilts/siso
  symlink_directory prebuilts/clang/host
  symlink_directory prebuilts/go
  symlink_directory prebuilts/sdk

  touch "$MOCK_TOP/Android.bp"
}

function setup {
  cleanup_mock_top
  mkdir -p "$MOCK_TOP"

  echo
  echo ----------------------------------------------------------------------------
  info "Running test case \e[96;1m${FUNCNAME[1]}\e[0m"
  cd "$MOCK_TOP"

  tar xf "$WARMED_UP_MOCK_TOP"
}

# shellcheck disable=SC2120
function run_soong {
  OUT_DIR=out USE_RBE=false TARGET_PRODUCT=test_arm64 TARGET_RELEASE=trunk_staging TARGET_BUILD_VARIANT=eng build/soong/soong_ui.bash --make-mode --skip-ninja --skip-config --soong-only --skip-soong-tests "$@"
}

function run_ninja {
  OUT_DIR=out USE_RBE=false TARGET_PRODUCT=test_arm64 TARGET_RELEASE=trunk_staging TARGET_BUILD_VARIANT=eng build/soong/soong_ui.bash --make-mode --skip-config --soong-only --skip-soong-tests "$@"
}

info "Starting Soong integration test suite $(basename "$0")"
info "Mock top: $MOCK_TOP"

trap cleanup_all EXIT

export ALLOW_MISSING_DEPENDENCIES=true
export ALLOW_BP_UNDER_SYMLINKS=true

if [[ -z "${WARMED_UP_MOCK_TOP:-}" ]]; then
  WARMED_UP_MOCK_TOP=$(mktemp -t soong_integration_tests_warmup.tar.gz.XXXXXX)
  warmup_mock_top
  WARMED_UP_MOCK_TOP_TO_CLEAN_UP+=("$WARMED_UP_MOCK_TOP")
fi

function scan_and_run_tests {
  test_fns=("$@")
  # find all test_ functions
  # NB "declare -F" output is sorted, hence test order is deterministic
  if [[ ${#test_fns[*]} -eq 0 ]]; then
    while IFS= read test_fn; do
        test_fns+=($test_fn)
    done < <(declare -F | sed -n -e 's/^declare -f \(test_.*\)$/\1/p')
  fi
  info "Found ${#test_fns[*]} tests"
  if [[ ${#test_fns[*]} -eq 0 ]]; then
    fail "No tests found"
  fi
  for f in ${test_fns[*]}; do
    $f
    info "Completed test case \e[96;1m$f\e[0m"
  done
}

function assert_files_equal {
  if [ $# -ne 2 ]; then
    echo "Usage: assert_files_equal file1 file2"
    exit 1
  fi

  if ! diff -ru "$1" "$2"; then
    echo "Files are different: $1 $2"
    exit 1
  fi
}

function compare_incremental_files() {
  local dir_full=$1; shift
  local dir_incremental=$1; shift
  count=0
  for file_before in ${dir_full}/*.ninja*; do
    basename=$(basename "$file_before")
    file_after="${dir_incremental}/${basename}"
    # The incremental file should be a superset of the full one.
    if [[ "$basename" == "build.test_arm64.ninja" ]]; then
      echo "Performing superset check for $basename..."
      extra_lines=$(comm -23 <(sort "$file_before") <(sort "$file_after"))
      if [[ -n "$extra_lines" ]]; then
        # If there are extra lines, print an error and the differing lines, then exit.
        echo "ERROR: $file_after is NOT a superset of $file_before."
        echo "The following lines were in the 'before' file but not the 'after' file:"
        echo "$extra_lines"
        exit 1
      fi
    elif [[ "${basename}" == "build.test_arm64.ninja.globs_time" ]]; then
      : # skip timestamp file
    elif [[ "${basename}" == "build.test_arm64.ninja.glob_results" ]]; then
      : # skip timestamp file
    else
      assert_files_equal $file_before $file_after
    fi
    ((count++)) || true
  done
  echo "Compared $count ninja files"
}

function compare_files_parity() {
  local dir_before=$1; shift
  local dir_after=$1; shift
  count=0
  for file_before in ${dir_before}/*.*; do
    basename="$(basename "${file_before}")"
    file_after="${dir_after}/${basename}"
    if [[ "${basename}" == "build.test_arm64.ninja.globs_time" ]]; then
      : # skip timestamp file
    elif [[ "${basename}" == "build.test_arm64.ninja.glob_results" ]]; then
      : # skip timestamp file
    else
    assert_files_equal $file_before $file_after
    ((count++)) || true
    fi
  done
  echo "Compared $count ninja files"
}

# Assumes an initial run of "run_soong SOONG_INCREMENTAL_ANALYSIS=true" and then
# the tree is modified.
function compare_incremental_and_full_analysis() {
    run_soong SOONG_INCREMENTAL_ANALYSIS=true "$@"
    mkdir incremental
    cp -pr out/soong/build.test_arm64*.ninja* incremental

    touch Android.bp
    run_soong SOONG_INCREMENTAL_ANALYSIS=false "$@"
    mkdir full
    cp -pr out/soong/build.test_arm64*.ninja* full

    compare_incremental_files full incremental
}
