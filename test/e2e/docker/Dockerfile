# We need to build in a Linux environment to support C libraries, e.g. RocksDB.
# We use Debian instead of Alpine, so that we can use binary database packages
# instead of spending time compiling them.
# FIXME: Migrate back to Alpine when possible, and reuse some stages from /DOCKER/Dockerfile

#################################
# STAGE 1: install dependencies #
#################################
FROM golang:1.19-bullseye AS base

RUN apt-get -qq update -y && \
    apt-get -qq upgrade -y >/dev/null && \
    apt-get -qq install -y cmake sudo libgmp-dev libleveldb-dev \
        librocksdb-dev libpcap-dev >/dev/null && \
    apt-get clean && \
    rm -r /var/lib/apt/lists/*

WORKDIR /src/bls

# Install BLS library
COPY third_party ./third_party
RUN ./third_party/bls-signatures/build.sh && \
    ./third_party/bls-signatures/install.sh && \
    rm -fr ./third_party

#####################################
# STAGE 2: install golang libraries #
#####################################
FROM base AS deps

WORKDIR /src/tenderdash

# Fetch dependencies separately (for layer caching)
COPY go.mod go.sum ./
RUN go mod download && go clean -cache -fuzzcache -testcache


###########################
# STAGE 2a: INSTALL DELVE #
###########################
# This is a separate stage to "go clean" unneeded deps and optimize space
FROM deps AS delve

RUN go install github.com/go-delve/delve/cmd/dlv@latest && go clean -cache -fuzzcache -testcache -modcache

######################
# STAGE 3: BUILD E2E #
######################
FROM deps AS e2e

WORKDIR /src/tenderdash

# Install DLV debugger
ENV DEBUG ""
COPY --from=delve /go/bin/dlv /go/bin/dlv

LABEL maintainer="developers@dash.org"

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
