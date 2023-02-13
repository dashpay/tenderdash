package consensus

import (
	"fmt"
	"testing"
	"time"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	"github.com/tendermint/tendermint/types"
)

func TestIsValidForPrevote(t *testing.T) {
	valSet, _ := mockValidatorSet()
	now := time.Now()
	defState := sm.State{
		Validators: valSet,
	}
	testCases := []struct {
		state   sm.State
		rs      cstypes.RoundState
		wantErr string
	}{
		{
			// invalid proposal-block
			state: defState,
			rs: cstypes.RoundState{
				Validators: valSet,
			},
			wantErr: "proposal-block is nil",
		},
		{
			// invalid proposal
			state: defState,
			rs: cstypes.RoundState{
				ProposalBlock: &types.Block{},
				Validators:    valSet,
			},
			wantErr: "proposal is nil",
		},
		{
			// timestamps is not equal
			state: defState,
			rs: cstypes.RoundState{
				ProposalBlock: &types.Block{
					Header: types.Header{Time: now},
				},
				Proposal:   &types.Proposal{Timestamp: now.Add(time.Second)},
				Validators: valSet,
			},
			wantErr: "proposal timestamp not equal",
		},
		{
			// proposal is not timely
			state: sm.State{
				InitialHeight: 1000,
				LastBlockTime: now.Add(time.Second),
			},
			rs: cstypes.RoundState{
				Height: 1000,
				ProposalBlock: &types.Block{
					Header: types.Header{Time: now},
				},
				LockedRound: -1,
				Proposal: &types.Proposal{
					Timestamp: now,
					POLRound:  -1,
				},
				Validators: valSet,
			},
			wantErr: "proposal is not timely",
		},
		{
			// valid
			state: sm.State{
				InitialHeight: 1000,
				LastBlockTime: now,
			},
			rs: cstypes.RoundState{
				Height: 1000,
				ProposalBlock: &types.Block{
					Header: types.Header{Time: now},
				},
				LockedRound: -1,
				Proposal: &types.Proposal{
					Timestamp: now,
					POLRound:  -1,
				},
				Validators: valSet,
			},
			wantErr: "",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			stateData := StateData{
				state:      tc.state,
				RoundState: tc.rs,
			}
			tmrequire.Error(t, tc.wantErr, stateData.isValidForPrevote())
		})
	}
}
