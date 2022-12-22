# We need to build in a Linux environment to support C libraries
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

COPY third_party ./third_party
COPY test/Makefile ./test/Makefile
COPY Makefile ./
COPY .git ./.git

# Install BLS library
RUN make build-bls

# Copy Tenderdash source
COPY . .

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
