#!/bin/bash

SCRIPT_PATH="$( cd "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
SRC_PATH="${SCRIPT_PATH}/src"
BUILD_PATH="${SCRIPT_PATH}/build"
BLS_SM_PATH="third_party/bls-signatures/src"
BLS_GIT_REPO="https://github.com/dashpay/bls-signatures.git"
BLS_GIT_BRANCH=${BLS_GIT_BRANCH:-"1.2.6"}

set -e

if ! git submodule update --init "${BLS_SM_PATH}" ; then
	echo "It looks like this source code is not tracked by git."
	echo "As a fallback scenario we will fetch \"${BLS_GIT_BRANCH}\" branch \"${BLS_GIT_REPO}\" library."
	echo "We would recommend to clone of this project rather than using a release archive."
	rm -r  "${BLS_SM_PATH}" || true
	git clone --single-branch --branch "${BLS_GIT_BRANCH}" "${BLS_GIT_REPO}" "${BLS_SM_PATH}"
fi

# Create folders for source and build data
mkdir -p "${BUILD_PATH}"

# Configurate the library build
cmake -B "${BUILD_PATH}" -S "${SRC_PATH}"

# Build the library
cmake --build "${BUILD_PATH}" -- -j 6

mkdir -p "${BUILD_PATH}/src/bls-dash"
cp -r ${SRC_PATH}/src/* "${BUILD_PATH}/src/bls-dash"

exit 0
