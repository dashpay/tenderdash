# GossipViz

## Introduction

GossipViz is a tool to visualize messages sent using the gossip protocol.
Information is read from (JSON-formatted) logs, including DEBUG logs.
At this point, only votes are supported.

## Prerequisites

In order to visualize gossip protocol, gather in one file all JSON-formatted debug logs from all Validators.
In case of end-to-end tests runnning locally, to extract logs you can use a short script, like:

```bash
LOGFILE=$(mktemp /tmp/gossipviz-in.XXXXXXXX)
for i in 01 02 03 04 05 06 07 08 09 10 11 12 ; do
    docker logs validator${i} |grep 'Sending vote message' >> $LOGFILE
done
```

## Usage

Basic usage:

```bash
go run ./cmd/gossipviz vote graph \
    --in logs-from-all-validators.json \
    --out votes.svg \
    --format svg \
    --height 1023 \
    --author 721540 \
    --round 0
```

The command above will parse all logs from `logs-from-all-validators.json`, find votes at height `1023` and round `0` authored (signed) by node with short proTxHash `721540`. Output will be saved to `votes.svg`.
