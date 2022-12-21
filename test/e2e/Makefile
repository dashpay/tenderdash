ROOT_PATH := $(abspath $(lastword $(MAKEFILE_LIST)/../../../))

all: docker generator runner tests

CGO_ENABLE ?= 1

CGO_LDFLAGS ?= "-L/src/tenderdash/third_party/bls-signatures/build/depends/minialloc \
-L/src/tenderdash/third_party/bls-signatures/build/depends/relic/lib \
-L/src/tenderdash/third_party/bls-signatures/build/src \
-ldashbls -lrelic_s -lgmp -lminialloc"

CGO_CXXFLAGS ?= "-I/src/tenderdash/third_party/bls-signatures/build/depends/relic/include \
-I/src/tenderdash/third_party/bls-signatures/src/depends/minialloc/include \
-I/src/tenderdash/third_party/bls-signatures/src/depends/relic/include \
-I/src/tenderdash/third_party/bls-signatures/src/include"

docker:
	docker build --tag tenderdash/e2e-node -f docker/Dockerfile ../..

node:
	GOTRACEBACK=crash go build -gcflags="all=-N -l" -o build/node -tags badgerdb,boltdb,cleveldb,deadlock ./node

e2e/app/compile:
	docker run --rm -it --entrypoint "/src/tenderdash/test/e2e/entrypoint.sh" -w "/src/tenderdash/test/e2e" -v $(ROOT_PATH)/test/e2e:/src/tenderdash/test/e2e tenderdash/e2e-node

runner/simple: runner tests e2e/app/compile
	./build/runner -f networks/simple.toml

runner/ci: runner tests e2e/app/compile
	./build/runner -f networks/ci.toml

runner/dashcore: runner tests e2e/app/compile
	./build/runner -f networks/dashcore.toml

runner/rotate: runner tests e2e/app/compile
	./build/runner -f networks/rotate.toml

generator:
	go build -o build/generator ./generator

runner:
	go build -o build/runner ./runner

tests:
	go test -o build/tests ./tests

.PHONY: all docker generator runner tests node
