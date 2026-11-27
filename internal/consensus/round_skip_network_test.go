package consensus

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// Validators connected through their reactors hand on the round of a proposer
// that goes silent while they wait for transactions. Each notices the silence
// on its own, their skip votes reach each other through the state channel, and
// every node, the silent proposer included, enters the next round on the
// certificate. That round waits for transactions in turn, under a proposer
// whose heartbeats arrive, and a transaction then gets the same block
// committed on every node.
func TestReactorHandsASilentProposersRoundOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const nodes = 4
	params := factory.ConsensusParams(func(c *types.ConsensusParams) {
		c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		c.Timeout.Vote = 50 * time.Millisecond
		c.Timeout.VoteDelta = 5 * time.Millisecond
		c.Timeout.ProposerHeartbeat = 50 * time.Millisecond
		c.Timeout.ProposerSilence = 300 * time.Millisecond
	})
	states := makeConsensusStateWithParams(ctx, t, nodes, "consensus_round_skip_network_test", newTickerFunc(), params,
		func(c *config.Config) { c.Consensus.CreateEmptyBlocks = false })
	// Only the proposer of a round signs heartbeats for it, so whichever
	// validator proposes round 0 of height 2 goes silent there.
	for _, state := range states {
		state.SetPrivValidator(ctx, &roundSilentSigner{PrivValidator: state.privValidator.PrivValidator, height: 2, round: 0})
	}
	rts := setup(ctx, t, nodes, states, 100)
	rts.switchToConsensus(ctx)

	// Height 1, the chain's first, does not wait for transactions.
	for _, sub := range rts.subs {
		require.Equal(t, int64(1), nextBlock(ctx, t, sub).Height)
	}

	require.Eventually(t, func() bool {
		for _, state := range states {
			stateData := state.GetStateData()
			if stateData.Height != 2 || stateData.RoundSkip == nil || stateData.Round != stateData.RoundSkip.Round+1 {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "the silent proposer's round was not handed on")
	for _, state := range states {
		assert.Equal(t, int32(0), state.GetStateData().RoundSkip.Round, "only the silent round is skipped")
	}

	tx := []byte{1, 2, 3}
	for _, state := range states {
		require.NoError(t, assertMempool(t, state.txNotifier).CheckTx(ctx, tx, nil, mempool.TxInfo{}))
	}
	var hash []byte
	for _, sub := range rts.subs {
		block := nextBlock(ctx, t, sub)
		require.Equal(t, int64(2), block.Height)
		if hash == nil {
			hash = block.Hash()
			continue
		}
		assert.Equal(t, hash, []byte(block.Hash()), "the nodes committed different blocks")
	}
}

// Validators decide whether to wait after a round skip from what they have
// seen, and a node does not pass on the votes of a round to peers that have
// moved past it. Here a polka from the skipped round reaches one validator
// only, after every node entered the next round on the certificate. That
// validator cannot wait, and the rest wait under a proposer whose heartbeats
// arrive. Its prevote in the round brings them along, and the height commits
// with no transaction anywhere, even though the first copy of that prevote to
// each peer is lost after its channel took it.
func TestReactorRoundAfterASkipFollowsTheValidatorThatSawAPolka(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const nodes = 4
	params := factory.ConsensusParams(func(c *types.ConsensusParams) {
		c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		c.Timeout.Propose = 500 * time.Millisecond
		c.Timeout.Vote = 50 * time.Millisecond
		c.Timeout.VoteDelta = 5 * time.Millisecond
		c.Timeout.ProposerHeartbeat = 50 * time.Millisecond
		c.Timeout.ProposerSilence = 300 * time.Millisecond
	})
	states := makeConsensusStateWithParams(ctx, t, nodes, "consensus_round_skip_split_polka_test", newTickerFunc(), params,
		func(c *config.Config) { c.Consensus.CreateEmptyBlocks = false })
	for _, state := range states {
		state.SetPrivValidator(ctx, &roundSilentSigner{PrivValidator: state.privValidator.PrivValidator, height: 2, round: 0})
	}
	lossy := &firstPrevoteLoss{height: 2, round: 1, lost: make(map[types.NodeID]bool)}
	rts := setupWithChannels(ctx, t, nodes, states, 100,
		func(nodeID types.NodeID, chID p2p.ChannelID, ch p2p.Channel) p2p.Channel {
			if chID != p2p.ConsensusVoteChannel {
				return ch
			}
			return &lossyVoteChannel{Channel: ch, node: nodeID, loss: lossy}
		})
	rts.switchToConsensus(ctx)
	for _, sub := range rts.subs {
		require.Equal(t, int64(1), nextBlock(ctx, t, sub).Height)
	}

	// Round 0 of height 2 is handed on, and every node waits in round 1.
	require.Eventually(t, func() bool {
		for _, state := range states {
			stateData := state.GetStateData()
			if stateData.Height != 2 || stateData.Round != 1 || stateData.Step != cstypes.RoundStepNewRound {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "the silent proposer's round was not handed on")

	// The node that hears the polka is not the proposer of round 1.
	stateData := states[0].GetStateData()
	proposer, err := stateData.ProposerSelector.GetProposer(2, 1)
	require.NoError(t, err)
	witness := -1
	for i, state := range states {
		if !state.privValidator.IsProTxHashEqual(proposer.ProTxHash) {
			witness = i
			break
		}
	}
	require.NotEqual(t, -1, witness)
	lossy.from(rts.network.NodeByProTxHash(states[witness].privValidator.ProTxHash).NodeID,
		states[witness].privValidator.ProTxHash)

	// The other three validators' prevotes for a block in round 0 reach it
	// alone.
	blockID := factory.MakeBlockID()
	valSet := stateData.Validators
	for i, state := range states {
		if i == witness {
			continue
		}
		index, _ := valSet.GetByProTxHash(state.privValidator.ProTxHash)
		require.GreaterOrEqual(t, index, int32(0))
		vs := newValidatorStub(state.privValidator.PrivValidator, index, 2)
		vote := signVote(ctx, t, vs, tmproto.PrevoteType, stateData.state.ChainID, blockID,
			valSet.QuorumType, valSet.QuorumHash)
		addVotes(states[witness], vote)
	}

	commitCtx, cancelCommit := context.WithTimeout(ctx, 30*time.Second)
	defer cancelCommit()
	for _, sub := range rts.subs {
		require.Equal(t, int64(2), nextBlock(commitCtx, t, sub).Height, "the waiting nodes did not follow")
	}
	assert.Equal(t, nodes-1, lossy.count(), "the first copy of the prevote to each peer was lost")
}

// Two validators that saw a polka from the skipped round leave the next round
// for the one after it, on the votes of a third, the round's proposer, which
// prevotes and precommits a block to them and signs nothing else in the round.
// Those votes and theirs agree on no block, so no node claims a majority to
// its peers. The other two validators wait in the round under that proposer's
// heartbeats, and every vote of the round sent to them is lost after the
// channel took it while the first two move on. Once nothing more is lost, the
// two in the later round send them the round's votes again, and the height
// commits with no transaction anywhere.
func TestReactorValidatorsLeftWaitingFollowTheRoundAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const nodes = 4
	params := factory.ConsensusParams(func(c *types.ConsensusParams) {
		c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		c.Timeout.Propose = 500 * time.Millisecond
		c.Timeout.Vote = 50 * time.Millisecond
		c.Timeout.VoteDelta = 5 * time.Millisecond
		c.Timeout.ProposerHeartbeat = 50 * time.Millisecond
		c.Timeout.ProposerSilence = 300 * time.Millisecond
	})
	states := makeConsensusStateWithParams(ctx, t, nodes, "consensus_round_skip_left_waiting_test", newTickerFunc(), params,
		func(c *config.Config) { c.Consensus.CreateEmptyBlocks = false })
	signers := make([]*voteSilentSigner, nodes)
	for i, state := range states {
		signers[i] = &voteSilentSigner{
			PrivValidator: &roundSilentSigner{PrivValidator: state.privValidator.PrivValidator, height: 2, round: 0},
			height:        2,
			round:         1,
		}
		state.SetPrivValidator(ctx, signers[i])
	}
	congestion := &roundVoteCongestion{height: 2, round: 1}
	rts := setupWithChannels(ctx, t, nodes, states, 100,
		func(nodeID types.NodeID, chID p2p.ChannelID, ch p2p.Channel) p2p.Channel {
			if chID != p2p.ConsensusVoteChannel {
				return ch
			}
			return &lossyVoteChannel{Channel: ch, node: nodeID, loss: congestion}
		})
	rts.switchToConsensus(ctx)
	for _, sub := range rts.subs {
		require.Equal(t, int64(1), nextBlock(ctx, t, sub).Height)
	}

	// Round 0 of height 2 is handed on, and every node waits in round 1.
	require.Eventually(t, func() bool {
		for _, state := range states {
			stateData := state.GetStateData()
			if stateData.Height != 2 || stateData.Round != 1 || stateData.Step != cstypes.RoundStepNewRound {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "the silent proposer's round was not handed on")

	// Round 1's proposer signs no vote of the round itself. Of the rest, two
	// move on and two wait.
	stateData := states[0].GetStateData()
	valSet := stateData.Validators
	proposer, err := stateData.ProposerSelector.GetProposer(2, 1)
	require.NoError(t, err)
	byzantine, active, waiting := -1, []int{}, []int{}
	for i, state := range states {
		switch {
		case state.privValidator.IsProTxHashEqual(proposer.ProTxHash):
			byzantine = i
			waiting = append(waiting, i)
		case len(active) < 2:
			active = append(active, i)
		default:
			waiting = append(waiting, i)
		}
	}
	require.NotEqual(t, -1, byzantine)
	signers[byzantine].mute()
	nodeID := func(i int) types.NodeID {
		return rts.network.NodeByProTxHash(states[i].privValidator.ProTxHash).NodeID
	}
	congestion.start([]types.NodeID{nodeID(active[0]), nodeID(active[1])},
		[]types.NodeID{nodeID(waiting[0]), nodeID(waiting[1])})

	signed := func(i int, voteType tmproto.SignedMsgType, round int32, blockID types.BlockID) *types.Vote {
		index, _ := valSet.GetByProTxHash(states[i].privValidator.ProTxHash)
		require.GreaterOrEqual(t, index, int32(0))
		vs := newValidatorStub(signers[i].PrivValidator, index, 2)
		vs.Round = round
		return signVote(ctx, t, vs, voteType, stateData.state.ChainID, blockID, valSet.QuorumType, valSet.QuorumHash)
	}
	// The other three validators' prevotes for a block in round 0 reach each
	// active validator, which then cannot wait in round 1. The proposer's
	// prevote and precommit of round 1, for another block, reach them too.
	polka := factory.MakeBlockID()
	other := factory.MakeBlockID()
	byzantinePrevote := signed(byzantine, tmproto.PrevoteType, 1, other)
	byzantinePrecommit := signed(byzantine, tmproto.PrecommitType, 1, other)
	for _, a := range active {
		for i := range states {
			if i != a {
				addVotes(states[a], signed(i, tmproto.PrevoteType, 0, polka))
			}
		}
		addVotes(states[a], byzantinePrevote, byzantinePrecommit)
	}

	// The active validators prevote and precommit nothing in round 1, and with
	// the proposer's votes leave it for round 2. Every vote of round 1 they
	// send the waiting two is lost: their own, each other's and the
	// proposer's, prevote and precommit, from each of them to each.
	const lostVotes = 2 * 2 * 6
	require.Eventually(t, func() bool {
		for _, a := range active {
			if states[a].GetStateData().Round < 2 {
				return false
			}
		}
		return congestion.count() == lostVotes
	}, 30*time.Second, 20*time.Millisecond, "the active validators did not move on, or not every vote was lost")
	for _, w := range waiting {
		stateData := states[w].GetStateData()
		require.Equal(t, int32(1), stateData.Round)
		require.Equal(t, cstypes.RoundStepNewRound, stateData.Step)
	}
	congestion.stop()

	commitCtx, cancelCommit := context.WithTimeout(ctx, 30*time.Second)
	defer cancelCommit()
	for _, sub := range rts.subs {
		require.Equal(t, int64(2), nextBlock(commitCtx, t, sub).Height, "the waiting validators did not follow")
	}
}

// A node that reached a later round on that round's votes alone holds no vote
// of the round its peers wait in. Here one validator is given the prevotes and
// precommits of round 2 of the other three, for three different blocks, while
// every node waits in round 1 under a proposer whose heartbeats arrive, and
// the three sign nothing else in round 2. That validator goes on to round 3.
// The others follow on the round 2 votes it sends them, and the height
// commits with no transaction anywhere.
func TestReactorWaitingValidatorsFollowANodeThatSkippedAhead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const nodes = 4
	params := factory.ConsensusParams(func(c *types.ConsensusParams) {
		c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		c.Timeout.Propose = 500 * time.Millisecond
		c.Timeout.Vote = 50 * time.Millisecond
		c.Timeout.VoteDelta = 5 * time.Millisecond
		c.Timeout.ProposerHeartbeat = 50 * time.Millisecond
		c.Timeout.ProposerSilence = 300 * time.Millisecond
	})
	states := makeConsensusStateWithParams(ctx, t, nodes, "consensus_round_skip_ahead_test", newTickerFunc(), params,
		func(c *config.Config) { c.Consensus.CreateEmptyBlocks = false })
	signers := make([]*voteSilentSigner, nodes)
	for i, state := range states {
		signers[i] = &voteSilentSigner{
			PrivValidator: &roundSilentSigner{PrivValidator: state.privValidator.PrivValidator, height: 2, round: 0},
			height:        2,
			round:         2,
		}
		state.SetPrivValidator(ctx, signers[i])
	}
	rts := setup(ctx, t, nodes, states, 100)
	rts.switchToConsensus(ctx)
	for _, sub := range rts.subs {
		require.Equal(t, int64(1), nextBlock(ctx, t, sub).Height)
	}

	// Round 0 of height 2 is handed on, and every node waits in round 1.
	require.Eventually(t, func() bool {
		for _, state := range states {
			stateData := state.GetStateData()
			if stateData.Height != 2 || stateData.Round != 1 || stateData.Step != cstypes.RoundStepNewRound {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "the silent proposer's round was not handed on")

	// The node that skips ahead is not the proposer of round 1, whose
	// heartbeats keep the others waiting. They sign nothing in round 2.
	stateData := states[0].GetStateData()
	valSet := stateData.Validators
	proposer, err := stateData.ProposerSelector.GetProposer(2, 1)
	require.NoError(t, err)
	ahead := -1
	for i, state := range states {
		if ahead == -1 && !state.privValidator.IsProTxHashEqual(proposer.ProTxHash) {
			ahead = i
			continue
		}
		signers[i].mute()
	}
	require.NotEqual(t, -1, ahead)

	// Round 2 votes of the other three, each for a block of its own.
	votesOf := func(voteType tmproto.SignedMsgType) []*types.Vote {
		votes := make([]*types.Vote, 0, nodes-1)
		for i, state := range states {
			if i == ahead {
				continue
			}
			index, _ := valSet.GetByProTxHash(state.privValidator.ProTxHash)
			require.GreaterOrEqual(t, index, int32(0))
			vs := newValidatorStub(signers[i].PrivValidator, index, 2)
			vs.Round = 2
			votes = append(votes, signVote(ctx, t, vs, voteType, stateData.state.ChainID, factory.MakeBlockID(),
				valSet.QuorumType, valSet.QuorumHash))
		}
		return votes
	}
	addVotes(states[ahead], votesOf(tmproto.PrevoteType)...)
	require.Eventually(t, func() bool {
		stateData := states[ahead].GetStateData()
		return stateData.Round == 2 && stateData.Step >= cstypes.RoundStepPrecommit
	}, 30*time.Second, 20*time.Millisecond, "the node did not follow the prevotes of round 2")
	addVotes(states[ahead], votesOf(tmproto.PrecommitType)...)

	commitCtx, cancelCommit := context.WithTimeout(ctx, 30*time.Second)
	defer cancelCommit()
	for _, sub := range rts.subs {
		require.Equal(t, int64(2), nextBlock(commitCtx, t, sub).Height, "the waiting validators did not follow")
	}
}

// roundVoteCongestion loses, while it is on, every vote of a height and round
// that the nodes it names send the peers it names, after the channel took it:
// the senders count each as delivered.
type roundVoteCongestion struct {
	height int64
	round  int32

	mtx     sync.Mutex
	senders map[types.NodeID]bool
	peers   map[types.NodeID]bool
	lost    map[lostVote]bool
}

// lostVote is a vote one node lost on the way to a peer.
type lostVote struct {
	node, peer types.NodeID
	voter      string
	voteType   tmproto.SignedMsgType
}

// start loses the votes senders send peers.
func (c *roundVoteCongestion) start(senders, peers []types.NodeID) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.senders, c.peers, c.lost = make(map[types.NodeID]bool), make(map[types.NodeID]bool), make(map[lostVote]bool)
	for _, node := range senders {
		c.senders[node] = true
	}
	for _, peer := range peers {
		c.peers[peer] = true
	}
}

// stop loses no more votes.
func (c *roundVoteCongestion) stop() {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.senders, c.peers = nil, nil
}

func (c *roundVoteCongestion) loses(node, peer types.NodeID, msg proto.Message) bool {
	vote, ok := msg.(*tmcons.Vote)
	if !ok || vote.Vote == nil || vote.Vote.Height != c.height || vote.Vote.Round != c.round {
		return false
	}
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if !c.senders[node] || !c.peers[peer] {
		return false
	}
	c.lost[lostVote{node: node, peer: peer, voter: string(vote.Vote.ValidatorProTxHash), voteType: vote.Vote.Type}] = true
	return true
}

// count returns how many different votes were lost, by sender and peer.
func (c *roundVoteCongestion) count() int {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return len(c.lost)
}

// firstPrevoteLoss loses the first prevote one node sends each peer for a
// height and round, of the validator it names, after the node's channel took
// it: as the router's queue, or the peer's limits, can drop a message the
// sender already counts as delivered.
type firstPrevoteLoss struct {
	height int64
	round  int32

	mtx   sync.Mutex
	node  types.NodeID
	voter crypto.ProTxHash
	lost  map[types.NodeID]bool
}

// from names the node whose prevotes are lost, and the validator whose.
func (l *firstPrevoteLoss) from(node types.NodeID, voter crypto.ProTxHash) {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	l.node, l.voter = node, voter
}

// loses reports whether node's message to peer is lost, and records the loss.
func (l *firstPrevoteLoss) loses(node, peer types.NodeID, msg proto.Message) bool {
	vote, ok := msg.(*tmcons.Vote)
	if !ok || vote.Vote == nil {
		return false
	}
	l.mtx.Lock()
	defer l.mtx.Unlock()
	if node != l.node || l.lost[peer] || vote.Vote.Type != tmproto.PrevoteType ||
		vote.Vote.Height != l.height || vote.Vote.Round != l.round ||
		!bytes.Equal(vote.Vote.ValidatorProTxHash, l.voter) {
		return false
	}
	l.lost[peer] = true
	return true
}

func (l *firstPrevoteLoss) count() int {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return len(l.lost)
}

// voteLoss names the messages a node's vote channel loses after taking them.
type voteLoss interface {
	loses(node, peer types.NodeID, msg proto.Message) bool
}

// lossyVoteChannel is one node's vote channel, which takes every message and
// loses those its loss names.
type lossyVoteChannel struct {
	p2p.Channel
	node types.NodeID
	loss voteLoss
}

func (ch *lossyVoteChannel) Send(ctx context.Context, envelope p2p.Envelope) error {
	if ch.loss.loses(ch.node, envelope.To, envelope.Message) {
		return nil
	}
	return ch.Channel.Send(ctx, envelope)
}

// nextBlock returns the block of the next new-block event on sub.
func nextBlock(ctx context.Context, t *testing.T, sub eventbus.Subscription) *types.Block {
	t.Helper()
	msg, err := sub.Next(ctx)
	require.NoError(t, err)
	return msg.Data().(types.EventDataNewBlock).Block
}

// roundSilentSigner is a validator's signer that signs no proposer heartbeat
// for one round, as a proposer that goes silent in it would.
type roundSilentSigner struct {
	types.PrivValidator
	height int64
	round  int32
}

func (s *roundSilentSigner) SignProposerHeartbeat(
	ctx context.Context,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	heartbeat *tmproto.ProposerHeartbeat,
) error {
	if heartbeat.Height == s.height && heartbeat.Round == s.round {
		return errors.New("silent in this round")
	}
	return s.PrivValidator.SignProposerHeartbeat(ctx, chainID, quorumType, quorumHash, heartbeat)
}

// voteSilentSigner is a validator's signer that, once muted, signs no vote of
// one round, as a validator that sends its votes of the round to some peers
// only, and not through its own node.
type voteSilentSigner struct {
	types.PrivValidator
	height int64
	round  int32
	muted  atomic.Bool
}

// mute stops the signer signing votes of its round.
func (s *voteSilentSigner) mute() {
	s.muted.Store(true)
}

func (s *voteSilentSigner) SignVote(
	ctx context.Context,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	vote *tmproto.Vote,
	logger log.Logger,
) error {
	if s.muted.Load() && vote.Height == s.height && vote.Round == s.round {
		return errors.New("signs no vote of this round")
	}
	return s.PrivValidator.SignVote(ctx, chainID, quorumType, quorumHash, vote, logger)
}
