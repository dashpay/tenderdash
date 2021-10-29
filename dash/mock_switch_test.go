package dash

import (
	"fmt"
	"strings"
	"time"

	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/p2p/mocks"
)

// MOCK SWITCH //
const (
	OpDialMany historyOperation = "dialMany"
	OpStopOne  historyOperation = "stopOne"
)

type historyOperation string

// mockSwitchHistoryEvent is a log of dial and stop operations executed by the MockSwitch
type mockSwitchHistoryEvent struct {
	Timestamp time.Time
	Operation historyOperation // OpDialMany, OpStopOne
	Params    []string
	Comment   string
}

// MockSwitch implements dash.ISwitch. It sends event about DialPeersAsync() and StopPeerGracefully() calls
// to HistoryChan and stores them in History
type MockSwitch struct {
	PeerSet         *p2p.PeerSet
	PersistentPeers map[string]bool
	History         []mockSwitchHistoryEvent
	HistoryChan     chan mockSwitchHistoryEvent
}

// NewMockSwitch creates a new mock Switch
func NewMockSwitch() *MockSwitch {

	isw := &MockSwitch{
		PeerSet:         p2p.NewPeerSet(),
		PersistentPeers: map[string]bool{},
		History:         []mockSwitchHistoryEvent{},
		HistoryChan:     make(chan mockSwitchHistoryEvent, 1000),
	}
	return isw
}

// Peers implements ISwitch by returning sw.PeerSet
func (sw *MockSwitch) Peers() p2p.IPeerSet {
	return sw.PeerSet
}

// AddPersistentPeers implements ISwitch by marking provided addresses as persistent
func (sw *MockSwitch) AddPersistentPeers(addrs []string) error {
	for _, addr := range addrs {
		addr = strings.TrimPrefix(addr, "tcp://")
		sw.PersistentPeers[addr] = true
	}
	return nil
}

// RemovePersistentPeer implements ISwitch. It checks if the addr is persistent, and
// marks it as non-persistent if needed.
func (sw *MockSwitch) RemovePersistentPeer(addr string) error {
	addr = strings.TrimPrefix(addr, "tcp://")
	if sw.PersistentPeers[addr] != true {
		return fmt.Errorf("peer is not persisitent, addr=%s", addr)
	}

	delete(sw.PersistentPeers, addr)
	return nil
}

// DialPeersAsync implements ISwitch. It emulates connecting to provided addresses
// and adds them as peers and emits history event OpDialMany.
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
	sw.history(OpDialMany, addrs...)
	return nil
}

// IsDialingOrExistingAddress implements ISwitch. It checks if provided peer has been dialed
// before.
func (sw *MockSwitch) IsDialingOrExistingAddress(addr *p2p.NetAddress) bool {
	return sw.PeerSet.Has(addr.ID)
}

// StopPeerGracefully implements iSwitch. It removes the peer from Peers() and emits history
// event OpStopOne.
func (sw *MockSwitch) StopPeerGracefully(peer p2p.Peer) {
	sw.PeerSet.Remove(peer)
	sw.history(OpStopOne, peer.String())
}

// history adds info about an operation to sw.History and sends it to sw.HistoryChan
func (sw *MockSwitch) history(op historyOperation, args ...string) {
	event := mockSwitchHistoryEvent{
		Timestamp: time.Now(),
		Operation: op,
		Params:    args,
	}
	sw.History = append(sw.History, event)

	sw.HistoryChan <- event
}
