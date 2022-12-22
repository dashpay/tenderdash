package consensus

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/internal/consensus/types"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	tmtypes "github.com/tendermint/tendermint/types"
)

func TestWalIter_Next(t *testing.T) {
	testCases := []struct {
		input []any
		want  []any
	}{
		{
			input: []any{
				newEventDataRoundState(1000, 0, types.RoundStepNewHeight),
				newProposalMessage(1000, 0),
				EndHeightMessage{Height: 1000},
				newEventDataRoundState(1001, 0, types.RoundStepNewHeight),
				newProposalMessage(1001, 0),
				EndHeightMessage{Height: 1001},
				newEventDataRoundState(1002, 0, types.RoundStepNewHeight),
				newProposalMessage(1002, 0),
				EndHeightMessage{Height: 1002},
			},
			want: []any{
				newEventDataRoundState(1000, 0, types.RoundStepNewHeight),
				newProposalMessage(1000, 0),
				EndHeightMessage{Height: 1000},
				newEventDataRoundState(1001, 0, types.RoundStepNewHeight),
				newProposalMessage(1001, 0),
				EndHeightMessage{Height: 1001},
				newEventDataRoundState(1002, 0, types.RoundStepNewHeight),
				newProposalMessage(1002, 0),
				EndHeightMessage{Height: 1002},
			},
		},
		{
			input: []any{
				newEventDataRoundState(0, 0, types.RoundStepNewHeight),
				newVoteMessage(0, 0),
				EndHeightMessage{Height: 0},
				newEventDataRoundState(1000, 0, types.RoundStepNewHeight),
				newProposalMessage(1000, 0),
				newBlockPartMessage(1000, 0),
				newVoteMessage(1000, 0),
				newVoteMessage(1000, 0),
				EndHeightMessage{Height: 1000},
			},
			want: []any{
				newEventDataRoundState(0, 0, types.RoundStepNewHeight),
				newVoteMessage(0, 0),
				EndHeightMessage{Height: 0},
				newEventDataRoundState(1000, 0, types.RoundStepNewHeight),
				newProposalMessage(1000, 0),
				newBlockPartMessage(1000, 0),
				newVoteMessage(1000, 0),
				newVoteMessage(1000, 0),
				EndHeightMessage{Height: 1000},
			},
		},
		{
			input: []any{
				newEventDataRoundState(0, 0, types.RoundStepNewHeight),
				newVoteMessage(0, 0),
				EndHeightMessage{Height: 0},

				newEventDataRoundState(1000, 0, types.RoundStepNewHeight),
				newProposalMessage(1000, 0),
				newVoteMessage(1000, 0),
				EndHeightMessage{Height: 1000},

				newEventDataRoundState(1001, 0, types.RoundStepNewHeight),
				newProposalMessage(1001, 0),
				newVoteMessage(1001, 0),
				// new round 1
				newEventDataRoundState(1001, 1, types.RoundStepPropose),
				newProposalMessage(1001, 1),
				newVoteMessage(1001, 1),
				newVoteMessage(1001, 0),
				newVoteMessage(1001, 1),
				// new round 2
				newEventDataRoundState(1001, 2, types.RoundStepPropose),
				newProposalMessage(1001, 2),
				newVoteMessage(1001, 0),
				newVoteMessage(1001, 1),
				// new round 3
				newEventDataRoundState(1001, 3, types.RoundStepPropose),
				newProposalMessage(1001, 3),
				newVoteMessage(1001, 0),
				newVoteMessage(1001, 1),
				newVoteMessage(1001, 2),
				EndHeightMessage{Height: 1001},

				newEventDataRoundState(1002, 0, types.RoundStepNewHeight),
				newProposalMessage(1002, 0),
				newVoteMessage(1002, 0),
				EndHeightMessage{Height: 1002},
			},
			want: []any{
				newEventDataRoundState(0, 0, types.RoundStepNewHeight),
				newVoteMessage(0, 0),
				EndHeightMessage{Height: 0},

				newEventDataRoundState(1000, 0, types.RoundStepNewHeight),
				newProposalMessage(1000, 0),
				newVoteMessage(1000, 0),
				EndHeightMessage{Height: 1000},

				newEventDataRoundState(1001, 3, types.RoundStepPropose),
				newProposalMessage(1001, 3),
				EndHeightMessage{Height: 1001},

				newEventDataRoundState(1002, 0, types.RoundStepNewHeight),
				newProposalMessage(1002, 0),
				newVoteMessage(1002, 0),
				EndHeightMessage{Height: 1002},
			},
		},
	}
	testFunc := func(t *testing.T, it walIter, want []any) {
		cnt := 0
		for it.Next() {
			msg := it.Value()
			require.Equal(t, want[cnt], msg.Msg)
			cnt++
		}
		require.NoError(t, it.Err())
		require.Equal(t, len(want), cnt)
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			it := newWalIter(&mockWALReader{ret: tc.input}, true)
			testFunc(t, it, tc.want)
			it = newWalIter(&mockWALReader{ret: tc.input}, false)
			testFunc(t, it, tc.input)
		})
	}
}

func TestWalIter_Err(t *testing.T) {
	testCases := []struct {
		err  error
		want string
	}{
		{
			err: io.EOF,
		},
		{
			err:  errors.New("reader error"),
			want: "reader error",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			it := newWalIter(&mockWALReader{ret: []any{tc.err}}, true)
			require.False(t, it.Next())
			requireError(t, tc.want, it.Err())
		})
	}
}

func requireError(t *testing.T, want string, err error) {
	if err != nil {
		require.True(t, strings.Contains(err.Error(), want))
		return
	}
	require.Equal(t, "", want, "error must not be nil")
}

type mockWALReader struct {
	ret []any
	pos int
}

func (w *mockWALReader) Decode() (*TimedWALMessage, error) {
	l := len(w.ret)
	if l == 0 || w.pos >= l {
		return nil, io.EOF
	}
	r := w.ret[w.pos]
	w.pos++
	if err, ok := r.(error); ok {
		return nil, err
	}
	return &TimedWALMessage{Msg: r}, nil
}

func newEventDataRoundState(height int64, round int32, step types.RoundStepType) tmtypes.EventDataRoundState {
	return tmtypes.EventDataRoundState{
		Height: height,
		Round:  round,
		Step:   step.String(),
	}
}

func newProposalMessage(height int64, round int32) msgInfo {
	return msgInfo{
		Msg: &ProposalMessage{
			Proposal: &tmtypes.Proposal{
				Height: height,
				Round:  round,
			},
		},
	}
}

func newBlockPartMessage(height int64, round int32) msgInfo {
	return msgInfo{
		Msg: &BlockPartMessage{
			Height: height,
			Round:  round,
			Part:   nil,
		},
	}
}

func newVoteMessage(height int64, round int32) msgInfo {
	return msgInfo{
		Msg: &VoteMessage{Vote: &tmtypes.Vote{
			Type:   tmproto.PrecommitType,
			Height: height,
			Round:  round,
		}},
	}
}
