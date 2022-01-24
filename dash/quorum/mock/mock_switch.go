package mock

import (
	"encoding/binary"
	"encoding/hex"

	"github.com/tendermint/tendermint/internal/libs/sync"
	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/types"
)

// MOCK SWITCH //
const (
	OpDial = "dialOne"
	OpStop = "stopOne"
)

// SwitchHistoryEvent is a log of dial and stop operations executed by the MockSwitch
type SwitchHistoryEvent struct {
	Operation string // OpDialMany, OpStopOne
	Params    []string
}

// Switch implements `dash.Switch`. It sends event about DialPeersAsync() and StopPeerGracefully() calls
// to HistoryChan and stores them in History
type Switch struct {
	mux            sync.Mutex
	ConnectedPeers map[types.NodeID]bool
	// History        []SwitchHistoryEvent
	HistoryChan chan SwitchHistoryEvent
}

// NewMockSwitch creates a new mock Switch
func NewMockSwitch() *Switch {
	isw := &Switch{
		ConnectedPeers: map[types.NodeID]bool{},
		// History:        []SwitchHistoryEvent{},
		HistoryChan: make(chan SwitchHistoryEvent, 1000),
	}
	return isw
}

// DialPeersAsync implements Switch. It emulates connecting to provided addresses
// and adds them as peers and emits history event OpDialMany.
func (sw *Switch) ConnectAsync(addr p2p.NodeAddress) error {
	id := addr.NodeID
	sw.mux.Lock()
	sw.ConnectedPeers[id] = true
	sw.mux.Unlock()

	sw.history(OpDial, string(id))
	return nil
}

// IsDialingOrExistingAddress implements Switch. It checks if provided peer has been dialed
// before.
func (sw *Switch) IsDialingOrConnected(id types.NodeID) bool {
	sw.mux.Lock()
	defer sw.mux.Unlock()
	return sw.ConnectedPeers[id]
}

// StopPeerGracefully implements Switch. It removes the peer from Peers() and emits history
// event OpStopOne.
func (sw *Switch) DisconnectAsync(id types.NodeID) error {
	sw.mux.Lock()
	sw.ConnectedPeers[id] = false
	sw.mux.Unlock()
	sw.history(OpStop, string(id))

	return nil
}
func (sw *Switch) Resolve(val types.ValidatorAddress) (p2p.NodeAddress, error) {
	// Generate node ID
	nodeID := make([]byte, 20)
	n := val.Port

	binary.LittleEndian.PutUint64(nodeID, uint64(n))

	addr := p2p.NodeAddress{
		NodeID:   types.NodeID(hex.EncodeToString(nodeID)),
		Protocol: p2p.TCPProtocol,
		Hostname: val.Hostname,
		Port:     val.Port,
		Path:     "",
	}

	return addr, nil
}

// history adds info about an operation to sw.History and sends it to sw.HistoryChan
func (sw *Switch) history(op string, args ...string) {
	event := SwitchHistoryEvent{
		Operation: op,
		Params:    args,
	}
	sw.HistoryChan <- event
}

// canonicalAddress converts provided `addr` to a canonical form, to make
// comparisons inside the tests easier
// func canonicalAddress(addr string) string {
// 	va, err := types.ParseValidatorAddress(addr)
// 	if err != nil {
// 		panic(fmt.Sprintf("cannot parse validator address %s: %s", addr, err))
// 	}
// 	return va.String()
// }
