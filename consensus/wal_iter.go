package consensus

import (
	"fmt"
	"io"

	"github.com/tendermint/tendermint/types"
)

type walReader interface {
	Decode() (*TimedWALMessage, error)
}

type walIter struct {
	queue     []*TimedWALMessage
	curHeight int64
	curRound  int32
	cache     []*TimedWALMessage

	r   walReader
	msg *TimedWALMessage
	err error
}

func newWalIter(reader walReader) *walIter {
	return &walIter{
		r: reader,
	}
}

// Value takes a top element from a queue, otherwise returns nil if the queue is empty
func (i *walIter) Value() *TimedWALMessage {
	if len(i.queue) == 0 {
		return nil
	}
	msg := i.queue[0]
	i.queue = i.queue[1:]
	return msg
}

// Next reads a next message from WAL, every message holds until reach a next height
// if the read message is Propose with the "round" greater than previous, then held messages are flush
func (i *walIter) Next() bool {
	if len(i.queue) > 0 {
		return true
	}
	if i.err != nil {
		return false
	}
	for len(i.queue) == 0 && i.readMsg() {
		if !i.processMsg(i.msg) {
			return false
		}
		i.cache = append(i.cache, i.msg)
	}
	if len(i.queue) == 0 {
		i.queue = i.cache
	}
	return len(i.queue) > 0
}

// Err returns an error if got the error is not io.EOF otherwise returns nil
func (i *walIter) Err() error {
	if i.err == io.EOF {
		return nil
	}
	return i.err
}

func (i *walIter) readMsg() bool {
	if i.err == io.EOF {
		return false
	}
	i.msg, i.err = i.r.Decode()
	if i.err == io.EOF {
		return false
	}
	return true
}

func (i *walIter) processMsg(msg *TimedWALMessage) bool {
	switch m := msg.Msg.(type) {
	case msgInfo:
		switch mi := m.Msg.(type) {
		case *ProposalMessage:
			i.processProposal(mi.Proposal)
		case *BlockPartMessage:
		case *VoteMessage:
		default:
			i.queue = nil
			i.cache = nil
			i.err = fmt.Errorf("unsupported type %T", i.err)
			return false
		}
	}
	return true
}

func (i *walIter) processProposal(p *types.Proposal) {
	if p.Height == i.curHeight && i.curRound < p.Round {
		i.cache = nil
		i.curRound = p.Round
	}
	if p.Height > i.curHeight {
		i.curHeight = p.Height
		i.curRound = p.Round
		i.queue = i.cache
		i.cache = nil
	}
}
