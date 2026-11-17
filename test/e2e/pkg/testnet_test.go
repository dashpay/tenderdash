package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateFinalizeHold(t *testing.T) {
	held := func(edit func(*Testnet, *Node)) error {
		testnet := Testnet{ABCIProtocol: ProtocolBuiltin}
		node := Node{
			Mode:         ModeFull,
			StartAt:      1010,
			StateSync:    StateSyncDisabled,
			QueueType:    "priority",
			FinalizeHold: 65 * time.Second,
		}
		edit(&testnet, &node)
		return node.validateFinalizeHold(testnet)
	}
	require.NoError(t, held(func(*Testnet, *Node) {}))
	require.NoError(t, held(func(_ *Testnet, n *Node) { n.FinalizeHold, n.Mode = 0, ModeSeed }))
	for name, edit := range map[string]func(*Testnet, *Node){
		"negative":     func(_ *Testnet, n *Node) { n.FinalizeHold = -time.Second },
		"socket app":   func(tn *Testnet, _ *Node) { tn.ABCIProtocol = ProtocolUNIX },
		"validator":    func(_ *Testnet, n *Node) { n.Mode = ModeValidator },
		"initial node": func(_ *Testnet, n *Node) { n.StartAt = 0 },
		"state sync":   func(_ *Testnet, n *Node) { n.StateSync = StateSyncP2P },
		"fifo queue":   func(_ *Testnet, n *Node) { n.QueueType = "fifo" },
		"perturbed":    func(_ *Testnet, n *Node) { n.Perturbations = []Perturbation{PerturbationRestart} },
	} {
		require.Error(t, held(edit), name)
	}
}
