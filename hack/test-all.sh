#!/bin/bash

set -e -x -u

./hack/build.sh
cp $IMGPKG_BINARY "$IMGPKG_BINARY.exe"
export IMGPKG_BINARY="$PWD/imgpkg.exe"

./hack/test.sh
./hack/test-e2e.sh
./hack/test-perf.sh

echo ALL SUCCESS
