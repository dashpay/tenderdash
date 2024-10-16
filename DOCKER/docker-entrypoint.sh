#!/bin/bash

# TODO: Workaround for busy port problem happening with docker restart policy
#  we are trying to start a new container but previous process still not release
#  the port.

#  As a workaround we are sleeping for 10 seconds after the tenderdash process
#  exits with non-zero exit code.
#
#  Must be fix with graceful shutdown of tenderdash process.

got_signal=false

# Function to handle signals and forward them to the tenderdash process
# shellcheck disable=SC2317
_forward_signal() {
	echo "Caught signal! Forwarding to tenderdash process."
	got_signal=true
	kill -s "$1" "$child"
}

# Trap signals and forward them to the tenderdash process
trap '_forward_signal TERM' SIGTERM
trap '_forward_signal INT' SIGINT
trap '_forward_signal HUP' SIGHUP
trap '_forward_signal QUIT' SIGQUIT

if [ ! -d "$TMHOME/config" ]; then
	echo "Running tenderdash init to create a single node (default) configuration for docker run."
	tenderdash init validator

	sed -i \
		-e "s/^address\s*=.*/address = \"$PROXY_APP\"/" \
		-e "s/^moniker\s*=.*/moniker = \"$MONIKER\"/" \
		-e 's/^addr-book-strict\s*=.*/addr-book-strict = false/' \
		-e 's/^timeout-commit\s*=.*/timeout-commit = "500ms"/' \
		-e 's/^index-all-tags\s*=.*/index-all-tags = true/' \
		-e 's,^laddr = "tcp://127.0.0.1:26657",laddr = "tcp://0.0.0.0:26657",' \
		-e 's/^prometheus\s*=.*/prometheus = true/' \
		"$TMHOME/config/config.toml"

	if [ -n "$ABCI" ]; then
		sed -i \
			-e "s/^transport\s*=.*/transport = \"$ABCI\"/" \
			"$TMHOME/config/config.toml"
	fi

	jq ".chain_id = \"$CHAIN_ID\" | .consensus_params.block.time_iota_ms = \"500\"" \
		"$TMHOME/config/genesis.json" >"$TMHOME/config/genesis.json.new"
	mv "$TMHOME/config/genesis.json.new" "$TMHOME/config/genesis.json"
fi

# Start tenderdash in the background
tenderdash "$@" 2>&1 &
child=$!
wait "$child"
exit_code=$?

if [ $got_signal == false ] && [ $exit_code -ne 0 ] && [ "$1" == "start" ]; then
	echo "Sleeping for 10 seconds as workaround for the busy port problem. See entrypoint code for details."
	sleep 10
fi

exit $exit_code
