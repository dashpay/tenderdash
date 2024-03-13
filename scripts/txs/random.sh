#! /bin/bash
set -u

function toHex() {
	echo -n "$1" | hexdump -ve '1/1 "%.2X"'
}

N="$1"
PORT="$2"

for i in $(seq 1 $N); do
	# store key value pair
	KEY=$(head -c 10 /dev/urandom)
	VALUE="$i"
	KV="$(toHex "$KEY=$VALUE")"
	echo "$KV"
	curl "127.0.0.1:$PORT/broadcast_tx_sync?tx=0x${KV}"
	echo
done
