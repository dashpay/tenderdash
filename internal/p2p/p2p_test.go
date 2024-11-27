package p2p_test

import (
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/ed25519"
	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/types"
)

// Common setup for P2P tests.

var (
	chID   = p2p.ChannelID(1)
	chDesc = &p2p.ChannelDescriptor{
		ID:                  chID,
		Priority:            5,
		SendQueueCapacity:   10,
		RecvMessageCapacity: 10,
	}

	selfKey  crypto.PrivKey = ed25519.GenPrivKeyFromSecret([]byte{0xf9, 0x1b, 0x08, 0xaa, 0x38, 0xee, 0x34, 0xdd})
	selfID                  = types.NodeIDFromPubKey(selfKey.PubKey())
	selfInfo                = types.NodeInfo{
		NodeID:     selfID,
		ListenAddr: "0.0.0.0:0",
		Network:    "test",
		Moniker:    string(selfID),
		Channels:   tmsync.NewConcurrentSlice[conn.ChannelID](0x01, 0x02),
	}

	peerKey  crypto.PrivKey = ed25519.GenPrivKeyFromSecret([]byte{0x84, 0xd7, 0x01, 0xbf, 0x83, 0x20, 0x1c, 0xfe})
	peerID                  = types.NodeIDFromPubKey(peerKey.PubKey())
	peerInfo                = types.NodeInfo{
		NodeID:     peerID,
		ListenAddr: "0.0.0.0:0",
		Network:    "test",
		Moniker:    string(peerID),
		Channels:   tmsync.NewConcurrentSlice[conn.ChannelID](0x01, 0x02),
	}
)
