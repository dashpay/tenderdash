ROOT_PATH := $(abspath $(lastword $(MAKEFILE_LIST)/../../../))

all: docker generator runner tests

CGO_ENABLED ?= 1

BLS_DIR="$(ROOT_PATH)/third_party/bls-signatures"
CGO_LDFLAGS ?= "-L$(BLS_DIR)/build/depends/minialloc \
-L$(BLS_DIR)/build/depends/relic/lib \
-L$(BLS_DIR)/build/src \
-ldashbls -lrelic_s -lgmp -lminialloc"

CGO_CXXFLAGS ?= "-I$(BLS_DIR)/build/depends/relic/include \
-I$(BLS_DIR)/src/depends/minialloc/include \
-I$(BLS_DIR)/src/depends/relic/include \
-I$(BLS_DIR)/src/include"

GO := CGO_ENABLED=$(CGO_ENABLED) CGO_CXXFLAGS=$(CGO_CXXFLAGS) CGO_LDFLAGS=$(CGO_LDFLAGS) go

print_var:
	@echo $(GO)

docker:
	docker build --tag tenderdash/e2e-node -f docker/Dockerfile ../..

node:
	GOTRACEBACK=crash $(GO) build -gcflags="all=-N -l" -o build/node -tags badgerdb,boltdb,cleveldb,deadlock ./node

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
	$(GO) build -o build/generator ./generator

runner: print_var
	$(GO) build -o build/runner ./runner

tests:
	$(GO) test -o build/tests ./tests

.PHONY: all docker generator runner tests node
