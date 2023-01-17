package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/state/mocks"
	"github.com/tendermint/tendermint/libs/log"
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
				Extension: mustHexToBytes("524F1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83B"),
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
		Hash: mustHexToBytes("1D03D1D81E94A099042736D40BD9681B867321443FF58A4568E274DBD83BFFEB"),
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
			wantBlockSign: "09B1205CE83BB2B6C447EAE29AB13DBC5FEF5F4B6BCAF9CDE26AB4E0F024577A25669A2ECF7EB00AC9CB952F040E63730D8AB757D8EA7DC904E3EA8EDC625E77319C6692A1B99CF8D5D1B9B1253B5D2E944EA644259506B8DC24B3DAA2E17256",
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
			wantBlockSign:  "0512BBFA226A90816DD7E84D2DA34BD86E18BF119740FE69EA10043417E409FE70D6C38033981FB01FF13C893F47D3BB146BBE05DBA21DF0707C19098BBC207C328272BF2A36549DFA31A655896ABD7C81B8561BA4568516AFDA3D8693967793",
		},
		{
			msgType:        tmproto.PrecommitType,
			blockID:        blockID,
			voteExtensions: voteExtensions,
			mockFn:         mockFn,
			wantBlockSign:  "0512BBFA226A90816DD7E84D2DA34BD86E18BF119740FE69EA10043417E409FE70D6C38033981FB01FF13C893F47D3BB146BBE05DBA21DF0707C19098BBC207C328272BF2A36549DFA31A655896ABD7C81B8561BA4568516AFDA3D8693967793",
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
