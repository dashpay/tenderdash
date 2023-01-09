package consensus

import (
	"context"
)

func queryMaj23GossipHandler(ps *PeerState, gossiper *msgGossiper) gossipHandlerFunc {
	return func(ctx context.Context, appState AppState) {
		// If peer is not a validator, we do nothing
		if !appState.isValidator(ps.GetProTxHash()) {
			return
		}
		rs := appState.RoundState
		prs := ps.GetRoundState()
		gossiper.gossipVoteSetMaj23(ctx, rs, prs)
	}
}
