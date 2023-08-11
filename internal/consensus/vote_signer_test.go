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
	voteExtensions := types.VoteExtensions{
		tmproto.VoteExtensionType_THRESHOLD_RECOVER: []types.VoteExtension{
			{Extension: tmbytes.MustHexDecode("524F1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83B")},
		},
	}
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
			wantBlockSign: "8B52677D4D455125808EDEE715D2A999695A6701E477C1F44CEDCCE3FC62FB88698D0B6B3CA0429E17EDA9DBCEA932720C189E21F5A6FB31B2C244152F0CD7988598AD572E5D605164554C80880BDC130E23C9DBEF20CF315D05F8C13B6C92CC",
		},
		{
			msgType:       tmproto.PrecommitType,
			wantBlockSign: "97CCF337D8FCA05E600EAAF769D73BE9A0D1466CAE85374E9E0EF4C3DD1759131E1D2C8B9E8D8D28EBEF27074669D46C0820DF4DA337DFFA6B3EB5BEEA4B78CA8EA131ED584609D227025DB96990C732C2D04A693BC0402B8A19229ED32A51B8",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: nil,
			mockFn:         mockFn,
			wantBlockSign:  "9755FA9803D98C344CB16A43B782D2A93ED9A7E7E1C8437482F42781D5EF802EC82442C14C44429737A7355B1F9D87CB139EB2CF193A1CF7C812E38B99221ADF4DAA60CE16550ED6509A9C467A3D4492D77038505235796968465337A1E14B3E",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: voteExtensions,
			mockFn:         mockFn,
			wantBlockSign:  "9755FA9803D98C344CB16A43B782D2A93ED9A7E7E1C8437482F42781D5EF802EC82442C14C44429737A7355B1F9D87CB139EB2CF193A1CF7C812E38B99221ADF4DAA60CE16550ED6509A9C467A3D4492D77038505235796968465337A1E14B3E",
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

			key1, err := bls.G1ElementFromBytes(key.Bytes())
			assert.NoError(t, err)

			t.Logf("key: %x", key1.Serialize())
			t.Logf("%+v", vote.VoteExtensions[tmproto.VoteExtensionType_THRESHOLD_RECOVER])
		})
	}
}
