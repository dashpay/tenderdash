# This is a multi-stage Dockerfile.
# * base - base dependencies that rarely change
# * deps - branch-dependent dependencies
# * compile - builds final binaries
# * image - creates final image of minimal size

ARG ALPINE_VERSION=3.23
ARG GOLANG_VERSION=1.25.6
#################################
# STAGE 1: install dependencies #
#################################
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS base

RUN apk update && \
    apk upgrade && \
    apk --no-cache add bash git gmp-dev gmp-static sudo cmake build-base libpcap-dev leveldb-dev && \
    rm -rf /var/cache/apk/*

WORKDIR /src/bls

# Install BLS library
COPY third_party ./third_party
RUN ./third_party/bls-signatures/build.sh

#####################################
# STAGE 2: install golang libraries #
#####################################
FROM base AS deps

WORKDIR /src/tenderdash

# Fetch dependencies separately (for layer caching)
COPY go.mod go.sum ./
RUN go mod download

#########################
# STAGE 3: BUILD SOURCE #
#########################
FROM deps AS compile

WORKDIR /src/tenderdash

# Copy Tenderdash source
# Avoid overwriting third-party libs
COPY . .

ARG TENDERMINT_BUILD_OPTIONS=tenderdash
ARG GOOS=linux

RUN rm -r /src/tenderdash/third_party && ln -s /src/bls/third_party /src/tenderdash/third_party && \
    make build-binary && \
    make build_abcidump && \
    go clean && go clean -cache -fuzzcache -testcache -modcache

########################
# STAGE 4: FINAL IMAGE #
########################
FROM alpine:${ALPINE_VERSION} AS image

LABEL maintainer="developers@dash.org"

# Tenderdash will be looking for the genesis file in /tenderdash/config/genesis.json
# (unless you change `genesis_file` in config.toml). You can put your config.toml and
# private validator file into /tenderdash/config.
#
# The /tenderdash/data dir is used by tenderdash to store state.
ENV TMHOME=/tenderdash

# OS environment setup
# Set user right away for determinism, create directory for persistence and give our user ownership
# jq and curl used for extracting `pub_key` from private validator while
# deploying tenderdash with Kubernetes. It is nice to have bash so the users
# could execute bash commands.

RUN apk update && \
    apk upgrade && \
    apk --no-cache add curl jq bash gmp libsodium-dev libstdc++ libpcap && \
    rm -rf /var/cache/apk/*

# Create tmuser with UID=100 and GID=1000
#
# For backward compatibility, we need to run tenderdash as UID 100 (the
# default UID used in previous tenderdash docker images). However, UID 100
# is often already taken by other system users (e.g., klogd in Alpine 3.21).
# Therefore, we delete any existing user with UID 100 and reassign that UID to
# our tmuser. We create a dedicated group tmuser with GID 1000
RUN addgroup --gid 1000 tmuser && \
    run_user="$(awk -F: '$3==100{print $1; exit}' /etc/passwd)" && \
    if [ -n "$run_user" ]; then deluser "$run_user"; fi && \
    adduser -u 100 -S -G tmuser tmuser -h "$TMHOME"


# Run the container with tmuser by default. (UID=100, GID=1000)
USER tmuser

WORKDIR $TMHOME

# p2p, rpc and prometheus port
EXPOSE 26656 26657 26660

STOPSIGNAL SIGTERM

COPY --from=compile /src/tenderdash/build/tenderdash /src/tenderdash/build/abcidump /usr/bin/

# You can overwrite these before the first run to influence
# config.json and genesis.json. Additionally, you can override
# CMD to add parameters to `tenderdash node`.
ENV PROXY_APP=kvstore MONIKER=dockernode CHAIN_ID=dockerchain ABCI=""

COPY ./DOCKER/docker-entrypoint.sh /usr/local/bin/

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["start"]

# Expose the data directory as a volume since there's mutable state in there
VOLUME [ "$TMHOME" ]
