package pop

import (
	"github.com/tendermint/tendermint/internal/p2p"
	dashproto "github.com/tendermint/tendermint/proto/tendermint/dash"
)

const (
	// Dash control channel that is used to exchange dash-specific messages, like challenge-response when
	// verifying peer validator keys
	DashControlChannel = p2p.ChannelID(0xd0)
)

func getChannelDescriptors() map[p2p.ChannelID]*p2p.ChannelDescriptor {
	return map[p2p.ChannelID]*p2p.ChannelDescriptor{
		// Dash control channel that is used to exchange dash-specific messages, like challenge-response when
		// verifying peer validator keys
		DashControlChannel: {
			ID:                  DashControlChannel,
			MessageType:         new(dashproto.ControlMessage),
			Priority:            2,
			SendQueueCapacity:   64,
			RecvMessageCapacity: 4096,
			RecvBufferCapacity:  256,
			Name:                "dash_control",
		},
	}
}
