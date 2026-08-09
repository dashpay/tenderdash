package consensus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// Nothing enters a vote set on trust. Whatever else the vote path does with a
// message — asks the application about its extensions, hands a verification
// result from one step to the next, skips a check it has already made — the set
// of votes it ends up holding must be exactly the set whose signatures check
// out, because a vote in the set counts towards a quorum and a quorum decides a
// block.
//
// This is checked by re-verifying every stored vote from scratch, against the
// validator set and chain the node itself would use, rather than by trusting
// whatever the vote path recorded on the way in. A vote that does not verify
// here is a vote the node accepted without proof, whichever step failed to
// establish it.
//
// The valid votes are asserted to be present as well: an empty vote set would
// satisfy the sweep while proving nothing at all.
func TestVoteSetOnlyEverHoldsVotesWhoseSignaturesVerify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const extensions = 4

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
	stateData := h.stateData()

	// Everything a peer can put on the vote path, valid and forged alike, from
	// distinct validators so that no rejection can be mistaken for a duplicate.
	valid := signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, extensions)
	forgedBlockSig := signPrecommitWithExtensions(ctx, t, h.vss[2], &stateData, extensions)
	forgedBlockSig.BlockSignature = wellFormedWrongSignature(ctx, t, h)
	brokenExtension := signPrecommitWithExtensions(ctx, t, h.vss[3], &stateData, extensions)
	brokenExtension.VoteExtensions[0].SetSignature(make([]byte, types.SignatureSize))

	validPrevote := honestVote(ctx, t, h.vss[1], &stateData, -1)
	forgedPrevote := honestVote(ctx, t, h.vss[2], &stateData, -1)
	forgedPrevote.BlockSignature = wellFormedWrongSignature(ctx, t, h)

	validNilPrecommit := nilPrecommit(ctx, t, h.vss[2], &stateData)
	forgedNilPrecommit := nilPrecommit(ctx, t, h.vss[3], &stateData)
	forgedNilPrecommit.BlockSignature = wellFormedWrongSignature(ctx, t, h)

	// A precommit for a round this node has not reached: the vote set that will
	// hold it does not exist yet when its signatures are checked, so the round
	// is created between the check and the insertion.
	catchUp := h.vss[1]
	incrementRound(catchUp)
	catchUpValid := signPrecommitWithExtensions(ctx, t, catchUp, &stateData, extensions)
	catchUpForged := signPrecommitWithExtensions(ctx, t, h.vss[2], &stateData, extensions)
	catchUpForged.Round = catchUpValid.Round
	catchUpForged.BlockSignature = wellFormedWrongSignature(ctx, t, h)

	dispatched := []*types.Vote{
		valid, forgedBlockSig, brokenExtension,
		validPrevote, forgedPrevote,
		validNilPrecommit, forgedNilPrecommit,
		catchUpValid, catchUpForged,
	}
	maxRound := int32(0)
	for _, vote := range dispatched {
		_ = h.dispatch(ctx, t, &VoteMessage{Vote: vote}, "peer")
		maxRound = max(maxRound, vote.Round)
	}

	stateData = h.stateData()
	checked := reverifyEveryStoredVote(t, &stateData, maxRound)
	reportf(t, "re-verified %d stored votes from scratch", checked)
	require.Equal(t, 4, checked,
		"the sweep must reach every vote the node accepted, including the one in the catch-up round")

	require.NotNil(t, storedVote(&stateData, valid),
		"the valid precommit was rejected, so the sweep above verified nothing about precommits")
	require.NotNil(t, storedVote(&stateData, validPrevote),
		"the valid prevote was rejected, so the sweep above verified nothing about prevotes")
	require.Nil(t, storedVote(&stateData, forgedBlockSig),
		"a precommit with a forged block signature reached the vote set")
	require.Nil(t, storedVote(&stateData, brokenExtension),
		"a precommit with an unverifiable vote extension reached the vote set")
	require.Nil(t, storedVote(&stateData, forgedPrevote),
		"a prevote with a forged block signature reached the vote set")
	require.Nil(t, storedVote(&stateData, forgedNilPrecommit),
		"a nil precommit with a forged block signature reached the vote set")
	require.NotNil(t, storedVote(&stateData, catchUpValid),
		"a valid precommit for a round this node has not reached was rejected, so the "+
			"catch-up round proves nothing")
	require.Nil(t, storedVote(&stateData, catchUpForged),
		"a forged precommit for a round this node has not reached bypassed verification")
}

// The application is asked to pass judgement on a vote's extensions only after
// this node has established that the vote is authentic. Otherwise anyone able to
// address the node could drive its application logic with vote extensions of
// their choosing, attributed to a validator that never produced them.
//
// The counts are read together: the accepted vote proves the callback is
// reachable at all, so a zero for a forged vote means it was withheld rather
// than that nothing ever calls it.
func TestVoteExtensionCallbackOnlySeesVotesThatVerified(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const extensions = 4

	inner, err := kvstore.NewMemoryApp()
	require.NoError(t, err)
	app := &voteExtensionCountingApp{Application: inner}
	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4, application: app})
	stateData := h.stateData()

	forged := signPrecommitWithExtensions(ctx, t, h.vss[2], &stateData, extensions)
	forged.BlockSignature = wellFormedWrongSignature(ctx, t, h)
	_ = h.dispatch(ctx, t, &VoteMessage{Vote: forged}, "peer")
	require.Zero(t, app.calls(),
		"the application was asked about the extensions of a precommit whose block signature is forged")

	broken := signPrecommitWithExtensions(ctx, t, h.vss[3], &stateData, extensions)
	broken.VoteExtensions[0].SetSignature(make([]byte, types.SignatureSize))
	_ = h.dispatch(ctx, t, &VoteMessage{Vote: broken}, "peer")
	require.Zero(t, app.calls(),
		"the application was asked about a vote extension that does not verify")

	valid := signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, extensions)
	_ = h.dispatch(ctx, t, &VoteMessage{Vote: valid}, "peer")
	require.Equal(t, 1, app.calls(),
		"the application must still be asked about a precommit that verified")
}

// What one accepted precommit costs this node is the figure the verification
// budget is spent against, and it is the whole reason the vote path hands its
// verification result forward instead of letting the vote set repeat it. The
// staged shape matters as much as the total: the block signature is drawn for
// first and alone, so a precommit that cannot produce one is rejected for a
// single pairing however many extensions it declares.
func TestPeerVoteVerificationDraws(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const extensions = 4

	testCases := []struct {
		name  string
		build func(h *floodHarness, stateData *StateData) *types.Vote
		draws []int
		why   string
	}{
		{
			name: "precommit for a block",
			build: func(h *floodHarness, sd *StateData) *types.Vote {
				return signPrecommitWithExtensions(ctx, t, h.vss[1], sd, extensions)
			},
			draws: []int{1, extensions},
			why:   "a precommit's signatures are verified once: block signature, then extensions",
		},
		{
			name: "precommit with a forged block signature",
			build: func(h *floodHarness, sd *StateData) *types.Vote {
				vote := signPrecommitWithExtensions(ctx, t, h.vss[1], sd, extensions)
				vote.BlockSignature = wellFormedWrongSignature(ctx, t, h)
				return vote
			},
			draws: []int{1},
			why:   "a forged block signature must cost one verification, whatever it declares",
		},
		{
			name: "prevote",
			build: func(h *floodHarness, sd *StateData) *types.Vote {
				return honestVote(ctx, t, h.vss[1], sd, -1)
			},
			draws: []int{1},
			why:   "a prevote carries one signature and no extensions",
		},
		{
			name: "precommit for nil",
			build: func(h *floodHarness, sd *StateData) *types.Vote {
				return nilPrecommit(ctx, t, h.vss[1], sd)
			},
			draws: []int{1},
			why:   "a nil precommit carries no extensions and skips the application callback",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
			stateData := h.stateData()
			vote := tc.build(h, &stateData)

			_ = h.dispatch(ctx, t, &VoteMessage{Vote: vote}, "peer")

			require.Equal(t, tc.draws, h.budget.charges(), tc.why)

			gate, err := budgetedMessageCost(&VoteMessage{Vote: vote})
			require.NoError(t, err)
			require.GreaterOrEqual(t, gate, sumInts(h.budget.charges()),
				"the message was admitted for less work than it went on to draw")
		})
	}
}

// The application is handed a vote's own bytes on the way to being asked about
// its extensions — kvstore, e2e and every other in-process proxy app get the
// request struct itself, with no serialisation in between — and it is handed
// them after the vote's signatures have been checked but before the vote is
// stored. A vote that comes back from that round trip must either still be the
// vote whose signatures were checked, or not be stored at all. Anything else
// puts a vote in the set that does not verify, where it occupies its
// validator's slot and turns that validator's genuine precommit into a conflict.
func TestApplicationCannotRewriteAVoteThroughItsRequest(t *testing.T) {
	const extensions = 4

	testCases := []struct {
		name    string
		rewrite func(req *abci.RequestVerifyVoteExtension)
	}{
		{
			// Only the block hash, so that nothing else about the vote can account
			// for the outcome: the vote still names its own validator and still
			// carries the signatures it was sent with, and the only thing that has
			// changed is a byte the block signature covers.
			name: "the block hash",
			rewrite: func(req *abci.RequestVerifyVoteExtension) {
				flipFirstByte(req.Hash)
			},
		},
		{
			name: "every byte it is handed",
			rewrite: func(req *abci.RequestVerifyVoteExtension) {
				flipFirstByte(req.Hash)
				flipFirstByte(req.ValidatorProTxHash)
				for _, ext := range req.VoteExtensions {
					flipFirstByte(ext.Extension)
					flipFirstByte(ext.GetSignRequestId())
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// The context is the harness's lifetime, and the harness logs through
			// this subtest. Scoping it here keeps the node's services from
			// outliving the test whose logger they hold.
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			inner, err := kvstore.NewMemoryApp()
			require.NoError(t, err)
			h := newFloodHarness(ctx, t, floodHarnessArgs{
				validators:  4,
				application: &voteExtensionMutatingApp{Application: inner, rewrite: tc.rewrite},
			})
			stateData := h.stateData()

			vote := signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, extensions)
			_ = h.dispatch(ctx, t, &VoteMessage{Vote: vote}, "peer")

			stateData = h.stateData()
			require.NotNil(t, storedVote(&stateData, vote),
				"an honest precommit was lost because the application scribbled on the request it was given")
			require.Equal(t, 1, reverifyEveryStoredVote(t, &stateData, vote.Round),
				"the sweep must reach the precommit the application was asked about")
		})
	}
}

// A vote this node produces, one replayed from the write-ahead log, and one a
// peer sends back to us claiming to be ours are all verified where they always
// were — in the vote set. Only a precommit that reaches the application
// callback carries a verification result forward, so none of these paths may
// have quietly lost a check.
func TestLocalAndReplayedVotesStillVerifiedInTheVoteSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const extensions = 4

	t.Run("replayed precommit", func(t *testing.T) {
		h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
		stateData := h.stateData()

		forged := signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, extensions)
		forged.BlockSignature = wellFormedWrongSignature(ctx, t, h)
		_ = h.dispatchReplayed(ctx, t, &VoteMessage{Vote: forged}, "peer")
		replayed := h.stateData()
		require.Nil(t, storedVote(&replayed, forged),
			"a replayed precommit with a forged block signature reached the vote set")

		valid := signPrecommitWithExtensions(ctx, t, h.vss[1], &stateData, extensions)
		_ = h.dispatchReplayed(ctx, t, &VoteMessage{Vote: valid}, "peer")
		replayed = h.stateData()
		require.NotNil(t, storedVote(&replayed, valid),
			"a replayed precommit that verifies must still be accepted")
		require.Empty(t, h.budget.charges(), "a replayed message is not charged to the budget")
	})

	// A peer echoing back a vote attributed to this node skips the application
	// callback, so nothing verifies it before the vote set does.
	t.Run("precommit attributed to this node", func(t *testing.T) {
		h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
		stateData := h.stateData()

		forged := signPrecommitWithExtensions(ctx, t, h.vss[0], &stateData, extensions)
		forged.BlockSignature = wellFormedWrongSignature(ctx, t, h)
		require.Equal(t, h.cs.privValidator.ProTxHash, forged.ValidatorProTxHash,
			"the vote must be attributed to this node for the local short-circuit to apply")

		_ = h.dispatch(ctx, t, &VoteMessage{Vote: forged}, "peer")
		stateData = h.stateData()
		require.Nil(t, storedVote(&stateData, forged),
			"a forged precommit attributed to this node reached the vote set unverified")
	})
}

// reverifyEveryStoredVote checks every vote held by every vote set up to
// maxRound from scratch, and returns how many it checked.
//
// maxRound is passed rather than read off the height's tracked round: a vote
// set opened for a peer catching up on a later round does not advance that
// round, so sweeping up to it would silently skip exactly the votes that
// arrived by the least usual route.
func reverifyEveryStoredVote(t *testing.T, stateData *StateData, maxRound int32) int {
	t.Helper()
	valSet := stateData.Validators
	checked := 0
	for round := int32(0); round <= maxRound; round++ {
		for _, voteType := range []tmproto.SignedMsgType{tmproto.PrevoteType, tmproto.PrecommitType} {
			voteSet := stateData.Votes.GetVoteSet(round, voteType)
			if voteSet == nil {
				continue
			}
			for _, vote := range voteSet.List() {
				val := valSet.GetByIndex(vote.ValidatorIndex)
				require.NotNil(t, val, "a vote from an unknown validator is in the vote set")
				require.NoError(t,
					vote.Verify(stateData.state.ChainID, valSet.QuorumType, valSet.QuorumHash,
						val.PubKey, val.ProTxHash),
					"a vote whose signatures do not verify is in the %s set of round %d",
					voteType, round)
				checked++
			}
		}
	}
	return checked
}

// storedVote returns the vote the vote set holds for the given vote's validator,
// if it holds one for the same block.
func storedVote(stateData *StateData, vote *types.Vote) *types.Vote {
	stored := stateData.Votes.GetVoteSet(vote.Round, vote.Type).GetByIndex(vote.ValidatorIndex)
	if stored == nil || !stored.BlockID.Equals(vote.BlockID) {
		return nil
	}
	return stored
}

// nilPrecommit is a genuinely signed precommit for no block: it carries no vote
// extensions and never reaches the application callback.
func nilPrecommit(ctx context.Context, t *testing.T, vs *validatorStub, stateData *StateData) *types.Vote {
	t.Helper()
	vote, err := vs.signVote(ctx, tmproto.PrecommitType, stateData.state.ChainID, types.BlockID{},
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, nil)
	require.NoError(t, err)
	return vote
}

// voteExtensionCountingApp counts how often the application is asked to pass
// judgement on a vote extension.
type voteExtensionCountingApp struct {
	abci.Application

	mtx sync.Mutex
	n   int
}

func (a *voteExtensionCountingApp) VerifyVoteExtension(
	ctx context.Context,
	req *abci.RequestVerifyVoteExtension,
) (*abci.ResponseVerifyVoteExtension, error) {
	a.mtx.Lock()
	a.n++
	a.mtx.Unlock()
	return a.Application.VerifyVoteExtension(ctx, req)
}

func (a *voteExtensionCountingApp) calls() int {
	a.mtx.Lock()
	defer a.mtx.Unlock()
	return a.n
}

// voteExtensionMutatingApp accepts every vote extension and, on its way there,
// writes through the request it was handed. An application is free to do this —
// the request is its own — which is exactly why the node must not be sharing the
// vote's memory with it.
type voteExtensionMutatingApp struct {
	abci.Application

	rewrite func(req *abci.RequestVerifyVoteExtension)
}

func (a *voteExtensionMutatingApp) VerifyVoteExtension(
	_ context.Context,
	req *abci.RequestVerifyVoteExtension,
) (*abci.ResponseVerifyVoteExtension, error) {
	a.rewrite(req)
	return &abci.ResponseVerifyVoteExtension{
		Status: abci.ResponseVerifyVoteExtension_ACCEPT,
	}, nil
}

func flipFirstByte(b []byte) {
	if len(b) > 0 {
		b[0] ^= 0x01
	}
}
