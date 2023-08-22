package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/dashpay/tenderdash/internal/p2p"
)

type (
	chanStore struct {
		mtx       sync.Mutex
		creator   p2p.ChannelCreator
		chanItems map[p2p.ChannelID]*chanItem
	}
	chanItem struct {
		descriptor *p2p.ChannelDescriptor
		channel    p2p.Channel
	}
)

func newChanStore(descriptors map[p2p.ChannelID]*p2p.ChannelDescriptor, creator p2p.ChannelCreator) *chanStore {
	store := &chanStore{
		creator:   creator,
		chanItems: make(map[p2p.ChannelID]*chanItem),
	}
	for _, descr := range descriptors {
		store.chanItems[descr.ID] = &chanItem{descriptor: descr}
	}
	return store
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
	item, ok := c.chanItems[chanID]
	if !ok {
		return nil, fmt.Errorf("channelID %v is unsupported", chanID)
	}
	if item.channel != nil {
		return item.channel, nil
	}
	var err error
	item.channel, err = c.creator(ctx, item.descriptor)
	if err != nil {
		return nil, err
	}
	c.chanItems[chanID] = item
	return item.channel, nil
}
