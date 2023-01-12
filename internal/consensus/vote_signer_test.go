package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/tendermint/tendermint/crypto"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/state/mocks"
	"github.com/tendermint/tendermint/libs/log"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

func TestVoteSigner_signAddVote(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const (
		H100 = int64(100)
	)
	emptyPeerID := types.NodeID("")
	mockCancelCtx := mock.AnythingOfType("*context.cancelCtx")
	valSet, priVals := mockValidatorSet()
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
			{
				Extension: tmrand.Bytes(32),
			},
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
	signer := VoteSigner{
		privValidator: privVal,
		logger:        logger,
		queueSender:   mockQueue,
		wal:           mWAL,
		voteExtender:  mockBlockExecutor,
	}
	blockID := types.BlockID{
		Hash: tmrand.Bytes(crypto.HashSize),
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
			wantBlockSign: "93A9D90CEC5F1B0FB3FB7535D58EAF7215C1E11EF1DA413C195BE371EA9D510CBEC703F20B059C40293DDD3D7A8F6A1E0434FD579A5489C6324FA2CF5EB97F702AAB4743940CBB22CB4D132C3B5283AFD5E9CA1D486B0996DACDE7F3E05E1D70",
		},
		{
			msgType:       tmproto.PrecommitType,
			wantBlockSign: "07418F19FD99C712C1131C0BFFB2B4DBA4AD48172981FAF5EF4239E710FFEE412C8665A0E19C4665C67CDD881D3D56240F6000C54D14618997EE7871E45AD0474335CF325489D3F9FD77B246C0C962984D075005C62AA61593C01C0E6425B3E0",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: nil,
			mockFn:         mockFn,
			wantBlockSign:  "8031636E190F1C91967645B209C3FF9BA2579BDA3DA72592314ADB91D17190BDC69D18BC1B3D28FED5B9169FC20DCD59040610ECC76EA25EA14CF6732BBE2D096503AE1F8756EBC1035EC72A77674B5967C8CE33EE6B0ABC85B3F8575B27C2DC",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: voteExtensions,
			mockFn:         mockFn,
			wantBlockSign:  "8031636E190F1C91967645B209C3FF9BA2579BDA3DA72592314ADB91D17190BDC69D18BC1B3D28FED5B9169FC20DCD59040610ECC76EA25EA14CF6732BBE2D096503AE1F8756EBC1035EC72A77674B5967C8CE33EE6B0ABC85B3F8575B27C2DC",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
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
					BlockSignature:     mustHexToBytes(tc.wantBlockSign),
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
		})
	}
}
