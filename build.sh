#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
BLS=$PWD/third_party/bls-signatures
GMP=$(brew --prefix gmp)
export CGO_ENABLED=1
export CGO_CXXFLAGS="-I$BLS/build/depends/relic/include -I$BLS/src/depends/mimalloc/include -I$BLS/src/depends/relic/include -I$BLS/src/include -I$GMP/include"
export CGO_LDFLAGS="-L$BLS/build/depends/mimalloc -L$BLS/build/depends/relic/lib -L$BLS/build/src -ldashbls -lrelic_s -lmimalloc-secure -lgmp -L$GMP/lib"
go build -trimpath -o build/tenderdash ./cmd/tenderdash
echo "built build/tenderdash"
