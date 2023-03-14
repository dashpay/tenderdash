package client

import (
	"context"
	"sync"

	"github.com/tendermint/tendermint/internal/p2p"
)

type chanStore struct {
	mtx         sync.Mutex
	descriptors map[p2p.ChannelID]*p2p.ChannelDescriptor
	creator     p2p.ChannelCreator
	chans       map[p2p.ChannelID]p2p.Channel
}

func newChanStore(descriptors map[p2p.ChannelID]*p2p.ChannelDescriptor, creator p2p.ChannelCreator) *chanStore {
	return &chanStore{
		creator:     creator,
		chans:       make(map[p2p.ChannelID]p2p.Channel),
		descriptors: descriptors,
	}
}

func (c *chanStore) iter(ctx context.Context, chanIDs ...p2p.ChannelID) (*p2p.ChannelIterator, error) {
	chans := make([]p2p.Channel, 0, len(chanIDs))
	for _, chanID := range chanIDs {
		ch, err := c.get(ctx, chanID)
		if err != nil {
			return nil, err
		}
		chans = append(chans, ch)
	}
	if len(chans) == 1 {
		return chans[0].Receive(ctx), nil
	}
	return p2p.MergedChannelIterator(ctx, chans...), nil
}

func (c *chanStore) get(ctx context.Context, chanID p2p.ChannelID) (p2p.Channel, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	ch, ok := c.chans[chanID]
	if ok {
		return ch, nil
	}
	ch, err := c.creator(ctx, c.descriptors[chanID])
	if err != nil {
		return nil, err
	}
	c.chans[chanID] = ch
	return ch, nil
}
