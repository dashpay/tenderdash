# This is a multi-stage Dockerfile.
# * base - base dependencies that rarely change
# * deps - branch-dependent dependencies
# * compile - builds final binaries
# * image - creates final image of minimal size

#################################
# STAGE 1: install dependencies #
#################################
FROM golang:1.19-alpine3.15 AS base

RUN apk update && \
    apk upgrade && \
    apk --no-cache add bash git gmp-dev sudo cmake build-base libpcap-dev && \
    rm -rf /var/cache/apk/*

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
# TODO: move below BLS install once on new BLS version
COPY go.mod go.sum ./
RUN go mod download && go clean -cache -fuzzcache -testcache

#########################
# STAGE 3: BUILD SOURCE #
#########################
FROM deps AS compile

WORKDIR /src/tenderdash

# Copy Tenderdash source
# Avoid overwriting third-party libs
COPY . .

ARG TENDERMINT_BUILD_OPTIONS=tenderdash
RUN make build-binary && \
    make build_abcidump && \
    go clean && go clean -cache -fuzzcache -testcache -modcache

########################
# STAGE 4: FINAL IMAGE #
########################
FROM alpine:3.15 AS image

LABEL maintainer="developers@dash.org"

# Tenderdash will be looking for the genesis file in /tenderdash/config/genesis.json
# (unless you change `genesis_file` in config.toml). You can put your config.toml and
# private validator file into /tenderdash/config.
#
# The /tenderdash/data dir is used by tenderdash to store state.
ENV TMHOME /tenderdash

# OS environment setup
# Set user right away for determinism, create directory for persistence and give our user ownership
# jq and curl used for extracting `pub_key` from private validator while
# deploying tenderdash with Kubernetes. It is nice to have bash so the users
# could execute bash commands.
RUN apk update && \
    apk upgrade && \
    apk --no-cache add curl jq bash gmp libstdc++ libpcap && \
    rm -rf /var/cache/apk/* && \
    addgroup tmuser && \
    adduser -S -G tmuser tmuser -h "$TMHOME"

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
ENV PROXY_APP=kvstore MONIKER=dockernode CHAIN_ID=dockerchain

COPY ./DOCKER/docker-entrypoint.sh /usr/local/bin/

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["start"]

# Expose the data directory as a volume since there's mutable state in there
VOLUME [ "$TMHOME" ]
