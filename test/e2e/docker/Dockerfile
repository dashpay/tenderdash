# We need to build in a Linux environment to support C libraries, e.g. RocksDB.
# We use Debian instead of Alpine, so that we can use binary database packages
# instead of spending time compiling them.
FROM golang:1.19-alpine3.17

RUN apk update && \
    apk upgrade && \
    apk add bash git gmp-dev sudo cmake build-base python3-dev leveldb-dev

# Set up build directory /src/tenderdash
WORKDIR /src/tenderdash

# Install DLV debugger
ENV DEBUG ""
RUN go install github.com/go-delve/delve/cmd/dlv@latest

# Fetch dependencies separately (for layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy Tenderdash source
COPY . .

# Install BLS library
RUN make build-bls

ENV CGO_ENABLE=1

ENV CGO_LDFLAGS="\
-L/src/tenderdash/third_party/bls-signatures/build/depends/minialloc \
-L/src/tenderdash/third_party/bls-signatures/build/depends/relic/lib \
-L/src/tenderdash/third_party/bls-signatures/build/src \
-ldashbls -lrelic_s -lgmp -lminialloc"

ENV CGO_CXXFLAGS="\
-I/src/tenderdash/third_party/bls-signatures/build/depends/relic/include \
-I/src/tenderdash/third_party/bls-signatures/src/depends/minialloc/include \
-I/src/tenderdash/third_party/bls-signatures/src/depends/relic/include \
-I/src/tenderdash/third_party/bls-signatures/src/include"

COPY test/e2e/docker/entrypoint* /usr/bin/

RUN cd test/e2e && make node && cp build/node /usr/bin/app

# Set up runtime directory. We don't use a separate runtime image since we need
# e.g. leveldb and rocksdb which are already installed in the build image.
WORKDIR /tenderdash
VOLUME /tenderdash
ENV TMHOME=/tenderdash
ENV GOTRACEBACK=crash

EXPOSE 26656 26657 26660 6060
ENTRYPOINT ["/usr/bin/entrypoint"]
CMD ["start"]
STOPSIGNAL SIGTERM
