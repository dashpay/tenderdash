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
			wantBlockSign: "80B628F02FE2047C7B98175CD8CF609775B95393C63D8EBC2F630D95121C28826C942F6511405D33484639C2906879110FDC293418C95862A60329FDDF0B210654559839B5ABFC11E50AFC4E498B0C9041118394DB04E52D0B28A92FC91DEABC",
		},
		{
			msgType:       tmproto.PrecommitType,
			wantBlockSign: "8BEEE4EDA67394060C1A1D41797E998120B7BC217E7D36526DA76AE57616475FB1C4DCF08C522E76C75220104611F56800F1CF299ECD98FDB1C598471DC0D4048F8F5381B034270EB0B66E987D61B7DF555DFA92C6B5C9E6FAD608676130A726",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: nil,
			mockFn:         mockFn,
			wantBlockSign:  "B2C484BB07094AAB8EFAB187982F6A8E8172FBEBAE5B6EB6304E527ABAAB7D059D9A43DDDBA82A6D296AF30E67C28D250449A586E9A69C2577057E01FA290BB03C186982D4D45E6016935AD4B9A84EB0911B62A83E457E25CE44DC28516D0E0A",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: types.VoteExtensionsFromProto(voteExtensions...),
			mockFn:         mockFn,
			wantBlockSign:  "B2C484BB07094AAB8EFAB187982F6A8E8172FBEBAE5B6EB6304E527ABAAB7D059D9A43DDDBA82A6D296AF30E67C28D250449A586E9A69C2577057E01FA290BB03C186982D4D45E6016935AD4B9A84EB0911B62A83E457E25CE44DC28516D0E0A",
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
		})
	}
}
