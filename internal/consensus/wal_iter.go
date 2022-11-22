package consensus

import (
	"fmt"
	"io"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/types"
)

type walIter interface {
	Value() *TimedWALMessage
	Next() bool
	Err() error
}

type walReader interface {
	Decode() (*TimedWALMessage, error)
}

type simpleWalIter struct {
	reader walReader
	value  *TimedWALMessage
	err    error
}

func (i *simpleWalIter) Value() *TimedWALMessage {
	return i.value
}

func (i *simpleWalIter) Next() bool {
	i.value, i.err = i.reader.Decode()
	return i.err == nil
}

func (i *simpleWalIter) Err() error {
	return i.err
}

type skipperWalIter struct {
	reader    walReader
	curHeight int64
	curRound  int32
	queue     []*TimedWALMessage
	hold      []*TimedWALMessage
	msg       *TimedWALMessage
	value     *TimedWALMessage
	err       error
}

func newWalIter(reader walReader, shouldSkip bool) walIter {
	if shouldSkip {
		return &skipperWalIter{reader: reader}
	}
	return &simpleWalIter{reader: reader}
}

// Value takes a top element from a queue, otherwise returns nil if the queue is empty
func (i *skipperWalIter) Value() *TimedWALMessage {
	return i.value
}

// Next reads a next message from WAL, every message holds until reach a next height
// if the read message is Propose with the "round" greater than previous, then held messages are flush
func (i *skipperWalIter) Next() bool {
	for len(i.queue) == 0 && i.readMsg() {
		err := i.processTimedWALMessage(i.msg)
		if err != nil {
			i.err = err
			return false
		}
	}
	if len(i.queue) == 0 {
		i.queue = i.hold
		i.hold = i.hold[0:0]
	}
	if len(i.queue) > 0 {
		i.value = i.queue[0]
		i.queue = i.queue[1:]
		return true
	}
	return false
}

// Err returns an error if got the error is not io.EOF otherwise returns nil
func (i *skipperWalIter) Err() error {
	if i.err == io.EOF {
		return nil
	}
	return i.err
}

func (i *skipperWalIter) readMsg() bool {
	if i.err == io.EOF {
		return false
	}
	i.msg, i.err = i.reader.Decode()
	if i.err == io.EOF {
		return false
	}
	if i.err != nil {
		return false
	}
	return true
}

func (i *skipperWalIter) processTimedWALMessage(msg *TimedWALMessage) error {
	ehm, ok := msg.Msg.(EndHeightMessage)
	if ok {
		i.curHeight = ehm.Height + 1
		i.curRound = 0
		i.hold = append(i.hold, msg)
		return nil
	}
	height, round, err := walMsgHeight(msg.Msg)
	if err != nil {
		return err
	}
	if height < i.curHeight {
		i.queue = append(i.queue, msg)
		return nil
	}
	if height == i.curHeight && round < i.curRound {
		return nil
	}
	switch m := msg.Msg.(type) {
	case types.EventDataRoundState:
		switch m.Step {
		case cstypes.RoundStepNewHeight.String():
			i.curHeight = m.Height
			i.curRound = m.Round
			i.queue = i.hold
			i.hold = nil
		case cstypes.RoundStepPropose.String():
			if m.Round == i.curRound+1 {
				i.curRound = m.Round
				i.hold = nil
			}
		}
	}
	i.hold = append(i.hold, i.msg)
	return nil
}

func walMsgHeight(msg WALMessage) (int64, int32, error) {
	switch m := msg.(type) {
	case types.EventDataRoundState:
		return m.Height, m.Round, nil
	case msgInfo:
		switch msg := m.Msg.(type) {
		case *ProposalMessage:
			return msg.Proposal.Height, msg.Proposal.Round, nil
		case *BlockPartMessage:
			return msg.Height, msg.Round, nil
		case *VoteMessage:
			return msg.Vote.Height, msg.Vote.Round, nil
		case *CommitMessage:
			return msg.Commit.Height, msg.Commit.Round, nil
		}
	case timeoutInfo:
		return m.Height, m.Round, nil
	}
	return 0, 0, fmt.Errorf("unknown WALMessage type: %T", msg)
}
