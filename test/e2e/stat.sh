#!/usr/bin/env bash

function req() {
    url="http://localhost:$1/status"
    echo $2
    curl -XGET --max-time 1 -d '{}' -s $url | jq ".result.sync_info.latest_block_height"
}

req 5701 "full01"
req 5702 "light01"
req 5703 "seed01"

port=5704
for i in {0..11};
do
	p=$(($port+$i))
	i=$(printf "%02d" $(($i + 1)))
	req $p "validator$i"
done
