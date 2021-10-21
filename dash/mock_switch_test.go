package dash_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/tendermint/tendermint/dash"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/p2p/mocks"
)

// MOCK SWITCH //

type mockSwitchHistoryEvent struct {
	Timestamp time.Time
	Operation string // "dialMany", "stopOne"
	Params    []string
	Comment   string
}
type MockSwitch struct {
	PeerSet         *p2p.PeerSet
	PersistentPeers map[string]bool
	History         []mockSwitchHistoryEvent
	// Connections     map[string]bool
	HistoryChan chan mockSwitchHistoryEvent
}

func NewMockSwitch() *MockSwitch {

	isw := &MockSwitch{
		PeerSet:         p2p.NewPeerSet(),
		PersistentPeers: map[string]bool{},
		History:         []mockSwitchHistoryEvent{},
		// Connections:     map[string]bool{},
		HistoryChan: make(chan mockSwitchHistoryEvent, 1000),
	}

	_ = dash.ISwitch(isw) // ensure we implement the interface
	return isw
}

func (sw *MockSwitch) Peers() p2p.IPeerSet {
	return sw.PeerSet
}

func (sw *MockSwitch) AddPersistentPeers(addrs []string) error {
	for _, addr := range addrs {
		addr = strings.TrimPrefix(addr, "tcp://")
		sw.PersistentPeers[addr] = true
	}
	return nil
}
func (sw *MockSwitch) RemovePersistentPeer(addr string) error {
	addr = strings.TrimPrefix(addr, "tcp://")
	if sw.PersistentPeers[addr] != true {
		return fmt.Errorf("peer is not persisitent, addr=%s", addr)
	}

	delete(sw.PersistentPeers, addr)
	return nil
}

func (sw *MockSwitch) DialPeersAsync(addrs []string) error {

	for _, addr := range addrs {
		peer := &mocks.Peer{}
		parsed, _ := p2p.ParseNodeAddress(addr)

		peer.On("ID").Return(parsed.NodeID)
		peer.On("String").Return(addr)
		if err := sw.PeerSet.Add(peer); err != nil {
			return err
		}
	}
	sw.history("dialMany", addrs...)
	return nil
}

func (sw *MockSwitch) IsDialingOrExistingAddress(addr *p2p.NetAddress) bool {
	return sw.PeerSet.Has(addr.ID)
}

func (sw *MockSwitch) StopPeerGracefully(peer p2p.Peer) {
	sw.PeerSet.Remove(peer)
	sw.history("stopOne", peer.String())
}

func (sw *MockSwitch) history(op string, args ...string) {
	event := mockSwitchHistoryEvent{
		Timestamp: time.Now(),
		Operation: op,
		Params:    args,
	}
	sw.History = append(sw.History, event)

	sw.HistoryChan <- event
}
