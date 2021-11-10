package e2e

import (
	"fmt"
	"strconv"

	"github.com/tendermint/tendermint/crypto"
)

const nodeKeyType = "ed25519"

func newNode(name string, testnet *Testnet, manifest Manifest, opts ...func(node *Node) error) (*Node, error) {
	nodeManifest := manifest.Nodes[name]
	mode := Mode(nodeManifest.Mode)
	if nodeManifest.Mode == "" {
		mode = ModeValidator
	}
	node := &Node{
		Name:                 name,
		Testnet:              testnet,
		ProTxHash:            nil,
		Mode:                 mode,
		Database:             "goleveldb",
		ABCIProtocol:         ProtocolBuiltin,
		PersistInterval:      1,
		PrivvalProtocol:      ProtocolFile,
		Perturbations:        []Perturbation{},
		Misbehaviors:         make(map[int64]string),
		PrivvalKeys:          make(map[string]crypto.QuorumKeys),
		PrivvalUpdateHeights: make(map[string]crypto.QuorumHash),
	}
	if node.StartAt == testnet.InitialHeight {
		node.StartAt = 0 // normalize to 0 for initial nodes, since code expects this
	}
	for _, opt := range opts {
		err := opt(node)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

func generateNodeKey(keyGen *keyGenerator) func(node *Node) error {
	return func(node *Node) error {
		node.NodeKey = keyGen.Generate(nodeKeyType)
		return nil
	}
}

func generateIP(ipGen *ipGenerator) func(node *Node) error {
	return func(node *Node) error {
		node.IP = ipGen.Next()
		return nil
	}
}

func generateProxyPort(proxyPortGen *portGenerator) func(node *Node) error {
	return func(node *Node) error {
		node.ProxyPort = proxyPortGen.Next()
		return nil
	}
}

func initPrivvalKeys(
	quorumHash crypto.QuorumHash,
	privKey crypto.PrivKey,
	thresholdPubKey crypto.PubKey,
) func(node *Node) error {
	return func(node *Node) error {
		if node.Mode != ModeValidator {
			// Set up genesis validators. If not specified explicitly, use all validator nodes.
			node.PrivvalKeys[quorumHash.String()] = crypto.QuorumKeys{
				ThresholdPublicKey: thresholdPubKey,
			}
			return nil
		}
		node.PrivvalKeys[quorumHash.String()] = crypto.QuorumKeys{
			PrivKey:            privKey,
			ThresholdPublicKey: thresholdPubKey,
		}
		return nil
	}
}

func nodeManifest(manifest Manifest) func(node *Node) error {
	return func(node *Node) error {
		nodeManifest := manifest.Nodes[node.Name]
		node.StartAt = nodeManifest.StartAt
		node.FastSync = nodeManifest.FastSync
		node.StateSync = nodeManifest.StateSync
		node.SnapshotInterval = nodeManifest.SnapshotInterval
		node.RetainBlocks = nodeManifest.RetainBlocks
		if nodeManifest.Database != "" {
			node.Database = nodeManifest.Database
		}
		if nodeManifest.ABCIProtocol != "" {
			node.ABCIProtocol = Protocol(nodeManifest.ABCIProtocol)
		}
		if nodeManifest.PrivvalProtocol != "" {
			node.PrivvalProtocol = Protocol(nodeManifest.PrivvalProtocol)
		}
		if nodeManifest.PersistInterval != nil {
			node.PersistInterval = *nodeManifest.PersistInterval
		}
		for _, p := range nodeManifest.Perturb {
			node.Perturbations = append(node.Perturbations, Perturbation(p))
		}
		for heightString, misbehavior := range nodeManifest.Misbehaviors {
			height, err := strconv.ParseInt(heightString, 10, 64)
			if err != nil {
				return fmt.Errorf("unable to parse height %s to int64: %w", heightString, err)
			}
			node.Misbehaviors[height] = misbehavior
		}
		return nil
	}
}

func bindSeedsAndPeers(testnet *Testnet, manifest Manifest) func(node *Node) error {
	return func(node *Node) error {
		// We do a second pass to set up seeds and persistent peers, which allows graph cycles.
		nodeManifest, ok := manifest.Nodes[node.Name]
		if !ok {
			return fmt.Errorf("failed to look up manifest for node %q", node.Name)
		}
		var err error
		node.Seeds, err = testnet.LookupNodes(nodeManifest.Seeds)
		if err != nil {
			return fmt.Errorf("unable to find the list of nodes %v for node %q: %w",
				nodeManifest.Seeds, node.Name, err)
		}
		node.PersistentPeers, err = testnet.LookupNodes(nodeManifest.PersistentPeers)
		if err != nil {
			return fmt.Errorf("unable to find a persistent peer set %q for node %q",
				nodeManifest.PersistentPeers, node.Name)
		}
		// If there are no seeds or persistent peers specified, default to persistent
		// connections to all other nodes.
		if len(node.PersistentPeers) == 0 && len(node.Seeds) == 0 {
			for _, peer := range testnet.Nodes {
				if peer.Name == node.Name {
					continue
				}
				node.PersistentPeers = append(node.PersistentPeers, peer)
			}
		}
		return nil
	}
}

func createNodes(names []string, testnet *Testnet, manifest Manifest, opts ...func(node *Node) error) ([]*Node, error) {
	var nodes []*Node
	for _, name := range names {
		fmt.Printf("Creating node: %s\n", name)
		node, err := newNode(name, testnet, manifest, opts...)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func updateNodes(nodes []*Node, opts ...func(node *Node) error) error {
	for _, node := range nodes {
		for _, opt := range opts {
			err := opt(node)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
