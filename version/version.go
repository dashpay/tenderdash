package version

import tmversion "github.com/tendermint/tendermint/proto/tendermint/version"

var (
	TMCoreSemVer = TMVersionDefault
)

const (
	// TMVersionDefault is the used as the fallback version for Tenderdash
	// when not using git describe. It is formatted with semantic versioning.
	TMVersionDefault = "0.10.0-dev"
	// ABCISemVer is the semantic version of the ABCI library
	ABCISemVer = "0.20.0"

	ABCIVersion = ABCISemVer
)

var (
	// P2PProtocol versions all p2p behavior and msgs.
	// This includes proposer selection.
	P2PProtocol uint64 = 8

	// BlockProtocol versions all block data structures and processing.
	// This includes validity of blocks and state updates.
	BlockProtocol uint64 = 11
)

type Consensus struct {
	Block uint64 `json:"block,string"`
	App   uint64 `json:"app,string"`
}

func (c Consensus) ToProto() tmversion.Consensus {
	return tmversion.Consensus{
		Block: c.Block,
		App:   c.App,
	}
}
