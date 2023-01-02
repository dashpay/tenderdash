#!/bin/bash

SCRIPT_PATH="$( cd "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
SRC_PATH="$SCRIPT_PATH/src"
BUILD_PATH="$SCRIPT_PATH/build"
BLS_SM_PATH="third_party/bls-signatures/src"
# FIXME: Use shotonoff's repo, as develop_0.1 was removed from dashpay/bls-signatures
# We can get back to dashpay/bls-signatures in tenderdash v0.11, as we will use new version of bls-signatures there
# BLS_GIT_REPO="https://github.com/dashpay/bls-signatures.git"
BLS_GIT_REPO="https://github.com/shotonoff/bls-signatures.git"
BLS_GIT_BRANCH="develop_0.1"

set -ex

## Autodetect git submodule head to get rid od dependency on whole .git dir
## This is needed to correctly use layered caching in Docker builder on Github
if [[ -f ".git/modules/third_party/bls-signatures/src/HEAD" ]] ; then
	BLS_GIT_BRANCH="$(cat ".git/modules/third_party/bls-signatures/src/HEAD")"
fi

if ! git submodule update --init "${BLS_SM_PATH}" ; then
	echo "It looks like this source code is not tracked by git."
	echo "As a fallback scenario we will fetch \"${BLS_GIT_BRANCH}\" branch \"${BLS_GIT_REPO}\" library."
	echo "We would recommend to clone of this project rather than using a release archive."
	rm -r  "${BLS_SM_PATH}" || true
	git clone --no-checkout "${BLS_GIT_REPO}" "${BLS_SM_PATH}" 
	git -C "${BLS_SM_PATH}" checkout "${BLS_GIT_BRANCH}"
	rm -r "${BLS_SM_PATH}/.git"
fi

# Create folders for source and build data
mkdir -p "${BUILD_PATH}"

# Configurate the library build
cmake -B "${BUILD_PATH}" -S "${SRC_PATH}"

# Build the library
make -C "${BUILD_PATH}" chiabls

exit 0
