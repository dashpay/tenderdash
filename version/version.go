package version

import tmversion "github.com/dashpay/tenderdash/proto/tendermint/version"

var (
	TMCoreSemVer = TMVersionDefault
)

const (
	// TMVersionDefault is the used as the fallback version for Tenderdash
	// when not using git describe. It is formatted with semantic versioning.
	TMVersionDefault = "1.4.0"
	// ABCISemVer is the semantic version of the ABCI library
	ABCISemVer = "1.3.0"

	ABCIVersion = ABCISemVer
)

var (
	// P2PProtocol versions all p2p behavior and msgs.
	// This includes proposer selection.
	P2PProtocol uint64 = 10

	// BlockProtocol versions all block data structures and processing.
	// This includes validity of blocks and state updates.
	BlockProtocol uint64 = 14
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
