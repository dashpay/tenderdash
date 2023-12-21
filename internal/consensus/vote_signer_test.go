package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	bls "github.com/dashpay/bls-signatures/go-bindings"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestVoteSigner_signAddVote(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const (
		H100 = int64(100)
	)
	emptyPeerID := types.NodeID("")
	mockCancelCtx := mock.AnythingOfType("*context.cancelCtx")
	valSet, priVals := factory.MockValidatorSet()
	proTxHash, err := priVals[0].GetProTxHash(ctx)
	assert.NoError(t, err)
	logger := log.NewTestingLogger(t)
	mWAL := newMockWAL(t)
	mockQueue := newMockQueueSender(t)
	mockBlockExecutor := mocks.NewExecutor(t)
	privVal := privValidator{
		PrivValidator: priVals[0],
		ProTxHash:     proTxHash,
	}
	voteExtensions := tmproto.VoteExtensions{{
		Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
		Extension: tmbytes.MustHexDecode("524F1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83B"),
	}}

	conf := configSetup(t)
	stateData := StateData{
		config: conf.Consensus,
		RoundState: cstypes.RoundState{
			Height:     H100,
			Round:      0,
			Validators: valSet,
		},
		state: sm.State{
			ChainID:    "test-chain",
			Validators: valSet,
		},
	}
	signer := voteSigner{
		privValidator: privVal,
		logger:        logger,
		queueSender:   mockQueue,
		wal:           mWAL,
		voteExtender:  mockBlockExecutor,
	}
	blockID := types.BlockID{
		Hash: tmbytes.MustHexDecode("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB"),
	}
	mockFn := func(voteExtensions types.VoteExtensions) {
		mockBlockExecutor.
			On("ExtendVote", ctx, mock.MatchedBy(func(vote *types.Vote) bool {
				vote.VoteExtensions = voteExtensions
				return true
			})).
			Return(nil).
			Once()
	}
	type testCase struct {
		msgType        tmproto.SignedMsgType
		blockID        types.BlockID
		voteExtensions types.VoteExtensions
		wantBlockSign  string
		mockFn         func(voteExtensions types.VoteExtensions)
	}
	testCases := []testCase{
		{
			msgType:       tmproto.PrevoteType,
			blockID:       blockID,
			wantBlockSign: "8400E1599EB8E954C7DBE069B14941B71E010D92F4999B4A64752AF1969EE9AF140F953A40F212F0E63C4EF92C5F9BDA1930D753B1522183C7EA46EC847C9053FE4AFAEA17B60C263F015380497B64F4CE2480D01CB4DE9A64F9C8E048472CF2",
		},
		{
			msgType:       tmproto.PrecommitType,
			wantBlockSign: "88AB6D08FC3E9D258A1CB78288EF00E49F245A4E9AF5CAFF0DBEBDB31CB0098D7B4A93424681F1747686163938CE6647023424C47E7ED5656F33693D112853D6483CE795F788ED3A657F74B58C2215CA056324EC33DF6C44608DA65B13563224",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: nil,
			mockFn:         mockFn,
			wantBlockSign:  "B89D8AB4B59B4285A05C59C7C2641EF64DBB1A68C99F55FB2716EE25CDA264A0F87D02767EEBA7124E926325CE36D96D157674633C229D8BFCD8CB039889700D87A2041CF9D44A3D0BC2F231E64EB3815199DCB70184BCDC8CAC593AF3C3FE5F",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: types.VoteExtensionsFromProto(voteExtensions...),
			mockFn:         mockFn,
			wantBlockSign:  "B89D8AB4B59B4285A05C59C7C2641EF64DBB1A68C99F55FB2716EE25CDA264A0F87D02767EEBA7124E926325CE36D96D157674633C229D8BFCD8CB039889700D87A2041CF9D44A3D0BC2F231E64EB3815199DCB70184BCDC8CAC593AF3C3FE5F",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			if tc.mockFn != nil {
				tc.mockFn(tc.voteExtensions)
			}
			wantVoteMsg := &VoteMessage{
				Vote: &types.Vote{
					Type:               tc.msgType,
					Height:             stateData.Height,
					Round:              stateData.Round,
					BlockID:            tc.blockID,
					ValidatorProTxHash: proTxHash,
					ValidatorIndex:     0,
					BlockSignature:     tmbytes.MustHexDecode(tc.wantBlockSign),
					VoteExtensions:     tc.voteExtensions,
				},
			}
			mockQueue.
				On("send", mockCancelCtx, wantVoteMsg, emptyPeerID).
				Once().
				Return(nil)
			mWAL.
				On("FlushAndSync").
				Once().
				Return(nil)
			vote := signer.signAddVote(ctx, &stateData, tc.msgType, tc.blockID)
			assert.NotNil(t, vote)
			key, err := privVal.GetPubKey(ctx, valSet.QuorumHash)
			assert.NoError(t, err)

			for _, ext := range vote.VoteExtensions {
				assert.NotEmpty(t, ext.GetSignature())
			}

			key1, err := bls.G1ElementFromBytes(key.Bytes())
			assert.NoError(t, err)

			t.Logf("key: %x", key1.Serialize())
		})
	}
}
