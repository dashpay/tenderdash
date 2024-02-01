#!/bin/bash

set -e

SCRIPT_PATH="$(realpath "$(dirname "$0")")"
BUILD_PATH="$SCRIPT_PATH/build"

if [ "$UID" -eq "0" ]; then
	CMAKE_INSTALL_PREFIX=${CMAKE_INSTALL_PREFIX:-"/usr/local"}
else
	CMAKE_INSTALL_PREFIX=${CMAKE_INSTALL_PREFIX:-"${HOME}/.local"}
fi

if [ ! -d "$BUILD_PATH" ]; then
	echo "$BUILD_PATH doesn't exist. Run \"make build-bls\" first." >/dev/stderr
	exit 1
fi

pushd "${SCRIPT_PATH}"

# Install the library
cmake -D CMAKE_INSTALL_PREFIX="${CMAKE_INSTALL_PREFIX}" -P "$BUILD_PATH/cmake_install.cmake"

popd

exit 0
