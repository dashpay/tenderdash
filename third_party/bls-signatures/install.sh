#!/bin/bash

set -e

SCRIPT_PATH="$(realpath "$(dirname "$0")")"
BUILD_PATH="$SCRIPT_PATH/build"

if [ ! -d "$BUILD_PATH" ]; then
	echo "$BUILD_PATH doesn't exist. Run \"make build-bls\" first." >/dev/stderr
	exit 1
fi

pushd "${SCRIPT_PATH}"

# Install the library
cmake -P "$BUILD_PATH/cmake_install.cmake"

popd

exit 0
