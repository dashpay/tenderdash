package consensus

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	tmtypes "github.com/tendermint/tendermint/types"
)

func TestWalIter(t *testing.T) {
	testCases := []struct {
		input   []Message
		want    []Message
		wantLen int
	}{
		{
			wantLen: 3,
			input: []Message{
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1000, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1001, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1002, Round: 0}},
			},
			want: []Message{
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1000, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1001, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1002, Round: 0}},
			},
		},
		{
			wantLen: 9,
			want: []Message{
				&VoteMessage{Vote: &tmtypes.Vote{Height: 0, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1000, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1000, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1001, Round: 3}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 1}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 2}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1002, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1002, Round: 0}},
			},
			input: []Message{
				&VoteMessage{Vote: &tmtypes.Vote{Height: 0, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1000, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1000, Round: 0}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1001, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1000, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1000, Round: 1}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1001, Round: 1}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 1}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1001, Round: 2}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 1}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1001, Round: 3}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 1}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1001, Round: 2}},
				&ProposalMessage{Proposal: &tmtypes.Proposal{Height: 1002, Round: 0}},
				&VoteMessage{Vote: &tmtypes.Vote{Height: 1002, Round: 0}},
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			it := newWalIter(&walMessages{msgs: tc.input})
			cnt := 0
			for it.Next() {
				msg := it.Value()
				mi := msg.Msg.(msgInfo)
				require.Equal(t, tc.want[cnt], mi.Msg.(Message))
				cnt++
			}
			require.NoError(t, it.Err())
			require.Equal(t, tc.wantLen, cnt)
		})
	}
}

type walMessages struct {
	msgs []Message
	pos  int
}

func (w *walMessages) Decode() (*TimedWALMessage, error) {
	l := len(w.msgs)
	if l == 0 || w.pos >= l {
		return nil, io.EOF
	}
	msg := w.msgs[w.pos]
	w.pos++
	twm := TimedWALMessage{
		Msg: msgInfo{Msg: msg},
	}
	return &twm, nil
}
