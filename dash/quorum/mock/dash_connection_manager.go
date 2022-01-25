package mock

import (
	"encoding/binary"
	"encoding/hex"

	"github.com/tendermint/tendermint/internal/libs/sync"
	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/types"
)

const (
	OpDial = "dialOne"
	OpStop = "stopOne"
)

// HistoryEvent is a log of dial and stop operations executed by the DashConnectionManager
type HistoryEvent struct {
	Operation string // OpDialMany, OpStopOne
	Params    []string
}

// DashConnectionManager implements `dash.DashConnectionManager`.
// It sends event about DialPeersAsync() and StopPeerGracefully() calls
// to HistoryChan and stores them in History
type DashConnectionManager struct {
	mux            sync.Mutex
	ConnectedPeers map[types.NodeID]bool
	HistoryChan    chan HistoryEvent
}

// NewDashConnectionManager creates a new mock dash.DashConnectionManager that sends
// notifications on all events to HistoryChan channel.
func NewDashConnectionManager() *DashConnectionManager {
	isw := &DashConnectionManager{
		ConnectedPeers: map[types.NodeID]bool{},
		HistoryChan:    make(chan HistoryEvent, 1000),
	}
	return isw
}

// DialPeersAsync implements dash.DashConnectionManager.
// It emulates connecting to provided addresses
// and adds them as peers and emits history event OpDialMany.
func (sw *DashConnectionManager) ConnectAsync(addr p2p.NodeAddress) error {
	id := addr.NodeID
	sw.mux.Lock()
	sw.ConnectedPeers[id] = true
	sw.mux.Unlock()

	sw.history(OpDial, string(id))
	return nil
}

// IsDialingOrExistingAddress implements dash.DashConnectionManager. It checks if provided peer has been dialed
// before.
func (sw *DashConnectionManager) IsDialingOrConnected(id types.NodeID) bool {
	sw.mux.Lock()
	defer sw.mux.Unlock()
	return sw.ConnectedPeers[id]
}

// StopPeerGracefully implements dash.DashConnectionManager. It removes the peer from Peers() and emits history
// event OpStopOne.
func (sw *DashConnectionManager) DisconnectAsync(id types.NodeID) error {
	sw.mux.Lock()
	sw.ConnectedPeers[id] = false
	sw.mux.Unlock()
	sw.history(OpStop, string(id))

	return nil
}
func (sw *DashConnectionManager) Resolve(val types.ValidatorAddress) (p2p.NodeAddress, error) {
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
func (sw *DashConnectionManager) history(op string, args ...string) {
	event := HistoryEvent{
		Operation: op,
		Params:    args,
	}
	sw.HistoryChan <- event
}
