#!/bin/sh
#
# Invoke Mockery v2 to update generated mocks for the given type.
#
# This script runs a locally-installed "mockery" if available, otherwise it
# runs the published Docker container. This legerdemain is so that the CI build
# and a local build can work off the same script.
#
VERSION=v2.46.3

if ! mockery --version 2>/dev/null | grep $VERSION; then
  echo "Please install mockery $VERSION"
  exit 1
fi

mockery
