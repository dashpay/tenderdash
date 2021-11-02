package mock

import (
	"fmt"
	"strings"
	"time"

	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/p2p/mocks"
)

// MOCK SWITCH //
const (
	OpDialMany = "dialMany"
	OpStopOne  = "stopOne"
)

// SwitchHistoryEvent is a log of dial and stop operations executed by the MockSwitch
type SwitchHistoryEvent struct {
	Timestamp time.Time
	Operation string // OpDialMany, OpStopOne
	Params    []string
	Comment   string
}

// Switch implements dash.ISwitch. It sends event about DialPeersAsync() and StopPeerGracefully() calls
// to HistoryChan and stores them in History
type Switch struct {
	PeerSet         *p2p.PeerSet
	PersistentPeers map[string]bool
	History         []SwitchHistoryEvent
	HistoryChan     chan SwitchHistoryEvent
}

// NewMockSwitch creates a new mock Switch
func NewMockSwitch() *Switch {

	isw := &Switch{
		PeerSet:         p2p.NewPeerSet(),
		PersistentPeers: map[string]bool{},
		History:         []SwitchHistoryEvent{},
		HistoryChan:     make(chan SwitchHistoryEvent, 1000),
	}
	return isw
}

// Peers implements ISwitch by returning sw.PeerSet
func (sw *Switch) Peers() p2p.IPeerSet {
	return sw.PeerSet
}

// AddPersistentPeers implements ISwitch by marking provided addresses as persistent
func (sw *Switch) AddPersistentPeers(addrs []string) error {
	for _, addr := range addrs {
		addr = strings.TrimPrefix(addr, "tcp://")
		sw.PersistentPeers[addr] = true
	}
	return nil
}

// RemovePersistentPeer implements ISwitch. It checks if the addr is persistent, and
// marks it as non-persistent if needed.
func (sw *Switch) RemovePersistentPeer(addr string) error {
	addr = strings.TrimPrefix(addr, "tcp://")
	if !sw.PersistentPeers[addr] {
		return fmt.Errorf("peer is not persisitent, addr=%s", addr)
	}

	delete(sw.PersistentPeers, addr)
	return nil
}

// DialPeersAsync implements ISwitch. It emulates connecting to provided addresses
// and adds them as peers and emits history event OpDialMany.
func (sw *Switch) DialPeersAsync(addrs []string) error {
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
func (sw *Switch) IsDialingOrExistingAddress(addr *p2p.NetAddress) bool {
	return sw.PeerSet.Has(addr.ID)
}

// StopPeerGracefully implements iSwitch. It removes the peer from Peers() and emits history
// event OpStopOne.
func (sw *Switch) StopPeerGracefully(peer p2p.Peer) {
	sw.PeerSet.Remove(peer)
	sw.history(OpStopOne, peer.String())
}

// history adds info about an operation to sw.History and sends it to sw.HistoryChan
func (sw *Switch) history(op string, args ...string) {
	event := SwitchHistoryEvent{
		Timestamp: time.Now(),
		Operation: op,
		Params:    args,
	}
	sw.History = append(sw.History, event)

	sw.HistoryChan <- event
}
