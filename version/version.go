package version

import tmversion "github.com/dashpay/tenderdash/proto/tendermint/version"

var (
	TMCoreSemVer = TMVersionDefault
)

const (
	// TMVersionDefault is the used as the fallback version for Tenderdash
	// when not using git describe. It is formatted with semantic versioning.
	TMVersionDefault = "1.0.0-dev.1"
	// ABCISemVer is the semantic version of the ABCI library
	ABCISemVer = "1.0.0"

	ABCIVersion = ABCISemVer
)

var (
	// P2PProtocol versions all p2p behavior and msgs.
	// This includes proposer selection.
	//
	// This is hex-encoded SemVer-like version, prefixed with 2 zero-bytes, where
	// each component (major, minor, patch) is a 2-byte number.
	// For example, 1.2.3 would be 0x0000 0001 0002 0003
	P2PProtocol uint64 = 0x0000000100000000

	// BlockProtocol versions all block data structures and processing.
	// This includes validity of blocks and state updates.
	//
	// This is hex-encoded SemVer-like version, prefixed with 2 zero-bytes, where
	// each component (major, minor, patch) is a 2-byte number.
	// For example, 1.2.3 would be 0x0000 0001 0002 0003
	BlockProtocol uint64 = 0x0000000100000000
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
