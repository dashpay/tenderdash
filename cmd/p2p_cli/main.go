package main

import (
	"github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/cmd/p2p_cli/commands"
)

// curl -s https://mnowatch.org/json/?method=emn_details | jq -r '.[] | "tcp://\(.platformNodeID)@\(.ip):\(.platformP2PPort)#\(.protx_hash)"'
//
//go:generate bash -c 'echo const hosts=\`; curl -s https://mnowatch.org/json/?method=emn_details | jq -r ".[] | \"tcp://\(.platformNodeID)@\(.ip):\(.platformP2PPort)#\(.protx_hash)\""; echo "\`" '

func main() {
	deadlock.Opts.Disable = true

	cmd := commands.NewRootCmd()
	if err := cmd.Execute(); err != nil {
		panic(err)
	}
}
