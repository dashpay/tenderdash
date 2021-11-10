//nolint:gosec
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dashevo/dashd-go/btcjson"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/crypto/secp256k1"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	mcs "github.com/tendermint/tendermint/test/maverick/consensus"
)

const (
	randomSeed     int64  = 2308084734268
	proxyPortFirst uint32 = 5701
	networkIPv4           = "10.186.73.0/24"
	networkIPv6           = "fd80:b10c::/48"
)

type Mode string
type Protocol string
type Perturbation string

const (
	ModeValidator Mode = "validator"
	ModeFull      Mode = "full"
	ModeLight     Mode = "light"
	ModeSeed      Mode = "seed"

	ProtocolBuiltin  Protocol = "builtin"
	ProtocolFile     Protocol = "file"
	ProtocolGRPC     Protocol = "grpc"
	ProtocolTCP      Protocol = "tcp"
	ProtocolUNIX     Protocol = "unix"
	ProtocolDashCore Protocol = "dashcore"

	PerturbationDisconnect Perturbation = "disconnect"
	PerturbationKill       Perturbation = "kill"
	PerturbationPause      Perturbation = "pause"
	PerturbationRestart    Perturbation = "restart"
)

// Testnet represents a single testnet.

type ValidatorsMap map[*Node]abci.ValidatorUpdate
type Testnet struct {
	Name                      string
	File                      string
	Dir                       string
	IP                        *net.IPNet
	InitialHeight             int64
	InitialCoreHeight         uint32
	InitialState              map[string]string
	Validators                map[*Node]crypto.PubKey
	ValidatorUpdates          map[int64]map[*Node]crypto.PubKey
	ChainLockUpdates          map[int64]int64
	Nodes                     []*Node
	KeyType                   string
	ThresholdPublicKey        crypto.PubKey
	ThresholdPublicKeyUpdates map[int64]crypto.PubKey
	QuorumType                btcjson.LLMQType
	QuorumHash                crypto.QuorumHash
	QuorumHashUpdates         map[int64]crypto.QuorumHash
}

// Node represents a Tenderdash node in a testnet.
type Node struct {
	Name                 string
	Testnet              *Testnet
	Mode                 Mode
	PrivvalKeys          map[string]crypto.QuorumKeys
	PrivvalUpdateHeights map[string]crypto.QuorumHash
	NodeKey              crypto.PrivKey
	ProTxHash            crypto.ProTxHash
	IP                   net.IP
	ProxyPort            uint32
	StartAt              int64
	FastSync             string
	StateSync            bool
	Database             string
	ABCIProtocol         Protocol
	PrivvalProtocol      Protocol
	PersistInterval      uint64
	SnapshotInterval     uint64
	RetainBlocks         uint64
	Seeds                []*Node
	PersistentPeers      []*Node
	Perturbations        []Perturbation
	Misbehaviors         map[int64]string
}

// LoadTestnet loads a testnet from a manifest file, using the filename to
// determine the testnet name and directory (from the basename of the file).
// The testnet generation must be deterministic, since it is generated
// separately by the runner and the test cases. For this reason, testnets use a
// random seed to generate e.g. keys.
func LoadTestnet(file string) (*Testnet, error) {
	fmt.Printf("loading manifest\n")
	manifest, err := LoadManifest(file)
	fmt.Printf("loaded manifest\n")
	if err != nil {
		return nil, err
	}
	dir := strings.TrimSuffix(file, filepath.Ext(file))

	// Set up resource generators. These must be deterministic.
	netAddress := networkIPv4
	if manifest.IPv6 {
		netAddress = networkIPv6
	}
	_, ipNet, err := net.ParseCIDR(netAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid IP network address %q: %w", netAddress, err)
	}

	ipGen := newIPGenerator(ipNet)
	keyGen := newKeyGenerator(randomSeed)
	proTxHashGen := newProTxHashGenerator(randomSeed + 1)
	quorumHashGen := newQuorumHashGenerator(randomSeed + 2)
	proxyPortGen := newPortGenerator(proxyPortFirst)

	// Set up nodes, in alphabetical order (IPs and ports get same order).
	nodeNames := manifest.NodeNames()
	validatorCount := manifest.ValidatorCount()
	fmt.Printf("validator count is %v\n", validatorCount)

	quorumType := manifest.QuorumType
	if quorumType == 0 {
		quorumType = 100
	}

	proTxHashes, privateKeys, thresholdPublicKey := generateValidatorKeys(proTxHashGen, validatorCount)

	quorumHash := quorumHashGen.Generate()

	testnet := &Testnet{
		Name:                      filepath.Base(dir),
		File:                      file,
		Dir:                       dir,
		IP:                        ipGen.Network(),
		InitialHeight:             1,
		InitialCoreHeight:         1,
		InitialState:              manifest.InitialState,
		Validators:                map[*Node]crypto.PubKey{},
		ValidatorUpdates:          map[int64]map[*Node]crypto.PubKey{},
		ChainLockUpdates:          map[int64]int64{},
		Nodes:                     []*Node{},
		ThresholdPublicKey:        thresholdPublicKey,
		ThresholdPublicKeyUpdates: map[int64]crypto.PubKey{},
		QuorumType:                btcjson.LLMQType(quorumType),
		QuorumHash:                quorumHash,
		QuorumHashUpdates:         map[int64]crypto.QuorumHash{},
	}
	if manifest.InitialHeight > 0 {
		testnet.InitialHeight = manifest.InitialHeight
	}
	if manifest.InitialCoreChainLockedHeight > 0 {
		testnet.InitialCoreHeight = manifest.InitialCoreChainLockedHeight
	}
	testnet.Nodes, err = createNodes(
		nodeNames,
		testnet,
		manifest,
		generateNodeKey(keyGen),
		generateIP(ipGen),
		generateProxyPort(proxyPortGen),
		initPrivvalKeys(quorumHash, keyGen.Generate(manifest.KeyType), thresholdPublicKey),
		nodeManifest(manifest),
	)
	if err != nil {
		return nil, err
	}
	err = updateNodes(testnet.Nodes, bindSeedsAndPeers(testnet, manifest))
	if err != nil {
		return nil, err
	}

	validators, err := getValidators(manifest, testnet)
	if err != nil {
		return nil, err
	}

	for i, validator := range validators {
		pubKey := privateKeys[i].PubKey()
		vu, err := validator.ValidatorUpdate(pubKey.Bytes(), types.DefaultDashVotingPower)
		if err != nil {
			return nil, err
		}
		testnet.Validators[validator] = vu
		validator.ProTxHash = proTxHashes[i]
		quorumKeys := crypto.QuorumKeys{
			PrivKey:            privateKeys[i],
			PubKey:             pubKey,
			ThresholdPublicKey: thresholdPublicKey,
		}
		validator.PrivvalKeys[quorumHash.String()] = quorumKeys
	}

	heights, err := prepareValidatorUpdateHeights(manifest.ValidatorUpdates)
	if err != nil {
		return nil, err
	}

	prepProTxHashes := prepareProTxHashesFunc(proTxHashGen)

	// Set up validator updates.
	for _, height := range heights {
		heightStr := strconv.FormatInt(int64(height), 10)
		validators := manifest.ValidatorUpdates[heightStr]
		valUpdate := ValidatorsMap{}

		nodes, err := testnet.LookupNodes(mapGetStrKeys(validators))
		if err != nil {
			return nil, fmt.Errorf("unable to find the validators at height %v: %w", height, err)
		}

		proTxHashes, privateKeys, thresholdPublicKey :=
			bls12381.CreatePrivLLMQDataOnProTxHashesDefaultThresholdUsingSeedSource(
				prepProTxHashes(nodes),
				randomSeed+int64(height),
			)

		quorumHash := quorumHashGen.Generate()

		for i, proTxHash := range proTxHashes {
			privKey := privateKeys[i]
			pubKey := privKey.PubKey()
			node := testnet.LookupNodeByProTxHash(proTxHash)
			valUpdate[node] = privateKeys[i].PubKey()
			if node == nil {
				return nil, fmt.Errorf("unknown validator with protxHash %X for update at height %v", proTxHash, height)
			}

			//power := manifest.ValidatorUpdates[heightStr][node.Name]
			vu, err := node.ValidatorUpdate(pubKey.Bytes(), types.DefaultDashVotingPower)
			if err != nil {
				return nil, err
			}
			valUpdate[node] = vu // privateKeys[i].PubKey()}
			quorumKeys := crypto.QuorumKeys{
				PrivKey:            privateKeys[i],
				PubKey:             pubKey,
				ThresholdPublicKey: thresholdPublicKey,
			}
			step := 0
			if height == 0 {
				fmt.Printf("Set validator %s/%X (at genesis) pubkey to %X\n", node.Name,
					node.ProTxHash, pubKey.Bytes())
			} else {
				step = 2
				fmt.Printf("Set validator %s/%X (at height %d (+ 2)) pubkey to %X\n", node.Name,
					node.ProTxHash, height, privateKeys[i].PubKey().Bytes())
			}
			node.PrivvalKeys[quorumHash.String()] = quorumKeys
			node.PrivvalUpdateHeights[strconv.Itoa(height+step)] = quorumHash
		}
		updateOutOfQuorumNodes(testnet.Nodes, height, proTxHashes, thresholdPublicKey, quorumHash)
		if height == heights[0] {
			testnet.QuorumHash = quorumHash
			testnet.ThresholdPublicKey = thresholdPublicKey
			testnet.Validators = valUpdate
		}
		testnet.ValidatorUpdates[int64(height)] = valUpdate
		testnet.ThresholdPublicKeyUpdates[int64(height)] = thresholdPublicKey
		testnet.QuorumHashUpdates[int64(height)] = quorumHash
	}

	//
	if _, ok := testnet.ValidatorUpdates[0]; !ok {
		testnet.ValidatorUpdates[0] = testnet.ValidatorUpdates[int64(heights[0])]
	}
	//

	// We need to do validator updates in order, as we use the previous validator set as the basis of current proTxHashes
	for height, lockHeight := range manifest.ChainLockUpdates {
		testnet.ChainLockUpdates[int64(mustAtoi(height))] = lockHeight
		fmt.Printf("Set chainlock at height %q / core height is %d\n", height, lockHeight)
	}
	return testnet, testnet.Validate()
}

// Validate validates a testnet.
func (t Testnet) Validate() error {
	if t.Name == "" {
		return errors.New("network has no name")
	}
	if t.IP == nil {
		return errors.New("network has no IP")
	}
	if len(t.Nodes) == 0 {
		return errors.New("network has no nodes")
	}
	for _, node := range t.Nodes {
		if err := node.Validate(t); err != nil {
			return fmt.Errorf("invalid node %q: %w", node.Name, err)
		}
	}
	return nil
}

// Validate validates a node.
func (n Node) Validate(testnet Testnet) error {
	if n.Name == "" {
		return errors.New("node has no name")
	}
	if n.IP == nil {
		return errors.New("node has no IP address")
	}
	if !testnet.IP.Contains(n.IP) {
		return fmt.Errorf("node IP %v is not in testnet network %v", n.IP, testnet.IP)
	}
	if n.ProxyPort > 0 {
		if n.ProxyPort <= 1024 {
			return fmt.Errorf("local port %v must be >1024", n.ProxyPort)
		}
		for _, peer := range testnet.Nodes {
			if peer.Name != n.Name && peer.ProxyPort == n.ProxyPort {
				return fmt.Errorf("peer %q also has local port %v", peer.Name, n.ProxyPort)
			}
		}
	}
	if n.Mode == "validator" {
		if n.ProTxHash == nil {
			return fmt.Errorf("validator %s must have a proTxHash set", n.Name)
		}
		if len(n.ProTxHash) != crypto.ProTxHashSize {
			return fmt.Errorf("validator %s must have a proTxHash of size 32 (%d)", n.Name, len(n.ProTxHash))
		}
	}
	switch n.FastSync {
	case "", "v0", "v1", "v2":
	default:
		return fmt.Errorf("invalid fast sync setting %q", n.FastSync)
	}
	switch n.Database {
	case "goleveldb", "cleveldb", "boltdb", "rocksdb", "badgerdb":
	default:
		return fmt.Errorf("invalid database setting %q", n.Database)
	}
	switch n.ABCIProtocol {
	case ProtocolBuiltin, ProtocolUNIX, ProtocolTCP, ProtocolGRPC:
	default:
		return fmt.Errorf("invalid ABCI protocol setting %q", n.ABCIProtocol)
	}
	if n.Mode == ModeLight && n.ABCIProtocol != ProtocolBuiltin {
		return errors.New("light client must use builtin protocol")
	}
	switch n.PrivvalProtocol {
	case ProtocolFile, ProtocolUNIX, ProtocolTCP, ProtocolDashCore:
	default:
		return fmt.Errorf("invalid privval protocol setting %q", n.PrivvalProtocol)
	}

	if n.StartAt > 0 && n.StartAt < n.Testnet.InitialHeight {
		return fmt.Errorf("cannot start at height %v lower than initial height %v",
			n.StartAt, n.Testnet.InitialHeight)
	}
	if n.StateSync && n.StartAt == 0 {
		return errors.New("state synced nodes cannot start at the initial height")
	}
	if n.PersistInterval == 0 && n.RetainBlocks > 0 {
		return errors.New("persist_interval=0 requires retain_blocks=0")
	}
	if n.PersistInterval > 1 && n.RetainBlocks > 0 && n.RetainBlocks < n.PersistInterval {
		return errors.New("persist_interval must be less than or equal to retain_blocks")
	}
	if n.SnapshotInterval > 0 && n.RetainBlocks > 0 && n.RetainBlocks < n.SnapshotInterval {
		return errors.New("snapshot_interval must be less than er equal to retain_blocks")
	}

	for _, perturbation := range n.Perturbations {
		switch perturbation {
		case PerturbationDisconnect, PerturbationKill, PerturbationPause, PerturbationRestart:
		default:
			return fmt.Errorf("invalid perturbation %q", perturbation)
		}
	}

	if (n.PrivvalProtocol != "file" || n.Mode != "validator") && len(n.Misbehaviors) != 0 {
		return errors.New("must be using \"file\" privval protocol to implement misbehaviors")
	}

	for height, misbehavior := range n.Misbehaviors {
		if height < n.StartAt {
			return fmt.Errorf("misbehavior height %d is below node start height %d",
				height, n.StartAt)
		}
		if height < testnet.InitialHeight {
			return fmt.Errorf("misbehavior height %d is below network initial height %d",
				height, testnet.InitialHeight)
		}
		exists := false
		for possibleBehaviors := range mcs.MisbehaviorList {
			if possibleBehaviors == misbehavior {
				exists = true
			}
		}
		if !exists {
			return fmt.Errorf("misbehavior %s does not exist", misbehavior)
		}
	}

	return nil
}

// LookupNode looks up a node by name. For now, simply do a linear search.
func (t Testnet) LookupNode(name string) *Node {
	for _, node := range t.Nodes {
		if node.Name == name {
			return node
		}
	}
	return nil
}

// LookupNodes returns slice of nodes corresponding to passed node names
func (t Testnet) LookupNodes(names []string) ([]*Node, error) {
	nodes := make([]*Node, 0, len(names))
	for _, name := range names {
		node := t.LookupNode(name)
		if node == nil {
			return nil, fmt.Errorf("node %q not found", name)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// LookupNodeByProTxHash looks up a node by name. For now, simply do a linear search.
func (t Testnet) LookupNodeByProTxHash(proTxHash crypto.ProTxHash) *Node {
	for _, node := range t.Nodes {
		if bytes.Equal(node.ProTxHash, proTxHash) {
			return node
		}
	}
	return nil
}

// ArchiveNodes returns a list of archive nodes that start at the initial height
// and contain the entire blockchain history. They are used e.g. as light client
// RPC servers.
func (t Testnet) ArchiveNodes() []*Node {
	var nodes []*Node
	for _, node := range t.Nodes {
		if !node.Stateless() && node.StartAt == 0 && node.RetainBlocks == 0 {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// RandomNode returns a random non-seed node.
func (t Testnet) RandomNode() *Node {
	for {
		node := t.Nodes[rand.Intn(len(t.Nodes))]
		if node.Mode != ModeSeed {
			return node
		}
	}
}

// IPv6 returns true if the testnet is an IPv6 network.
func (t Testnet) IPv6() bool {
	return t.IP.IP.To4() == nil
}

// HasPerturbations returns whether the network has any perturbations.
func (t Testnet) HasPerturbations() bool {
	for _, node := range t.Nodes {
		if len(node.Perturbations) > 0 {
			return true
		}
	}
	return false
}

// LastMisbehaviorHeight returns the height of the last misbehavior.
func (t Testnet) LastMisbehaviorHeight() int64 {
	lastHeight := int64(0)
	for _, node := range t.Nodes {
		for height := range node.Misbehaviors {
			if height > lastHeight {
				lastHeight = height
			}
		}
	}
	return lastHeight
}

// ValidatorUpdate creates an abci.ValidatorUpdate struct from the current node
func (n *Node) ValidatorUpdate(publicKey []byte, power int64) (abci.ValidatorUpdate, error) {
	proTxHash := n.ProTxHash.Bytes()
	address := n.AddressP2P(true)
	validatorUpdate := abci.UpdateValidator(proTxHash, publicKey, power, address)
	return validatorUpdate, nil
}

// AddressP2P returns a P2P endpoint address for the node.
func (n Node) AddressP2P(withID bool) string {
	ip := n.IP.String()
	if n.IP.To4() == nil {
		// IPv6 addresses must be wrapped in [] to avoid conflict with : port separator
		ip = fmt.Sprintf("[%v]", ip)
	}
	addr := fmt.Sprintf("%v:26656", ip)
	if withID {
		addr = fmt.Sprintf("%x@%v", n.NodeKey.PubKey().Address().Bytes(), addr)
	}
	return addr
}

// AddressRPC returns an RPC endpoint address for the node.
func (n Node) AddressRPC() string {
	ip := n.IP.String()
	if n.IP.To4() == nil {
		// IPv6 addresses must be wrapped in [] to avoid conflict with : port separator
		ip = fmt.Sprintf("[%v]", ip)
	}
	return fmt.Sprintf("%v:26657", ip)
}

// Client returns an RPC client for a node.
func (n Node) Client() (*rpchttp.HTTP, error) {
	return rpchttp.New(fmt.Sprintf("http://127.0.0.1:%v", n.ProxyPort), "/websocket")
}

// Stateless returns true if the node is either a seed node or a light node
func (n Node) Stateless() bool {
	return n.Mode == ModeLight || n.Mode == ModeSeed
}

// keyGenerator generates pseudorandom keys based on a seed.
type keyGenerator struct {
	random *rand.Rand
}

func newKeyGenerator(seed int64) *keyGenerator {
	return &keyGenerator{
		random: rand.New(rand.NewSource(seed)),
	}
}

func (g *keyGenerator) Generate(keyType string) crypto.PrivKey {
	seed := make([]byte, bls12381.SeedSize)

	_, err := io.ReadFull(g.random, seed)
	if err != nil {
		panic(err) // this shouldn't happen
	}
	switch keyType {
	case "secp256k1":
		return secp256k1.GenPrivKeySecp256k1(seed)
	case "", "bls12381":
		return bls12381.GenPrivKeyFromSecret(seed)
	case "ed25519":
		return ed25519.GenPrivKeyFromSecret(seed)
	default:
		panic("KeyType not supported") // should not make it this far
	}
}

// portGenerator generates local Docker proxy ports for each node.
type portGenerator struct {
	nextPort uint32
}

func newPortGenerator(firstPort uint32) *portGenerator {
	return &portGenerator{nextPort: firstPort}
}

func (g *portGenerator) Next() uint32 {
	port := g.nextPort
	g.nextPort++
	if g.nextPort == 0 {
		panic("port overflow")
	}
	return port
}

// ipGenerator generates sequential IP addresses for each node, using a random
// network address.
type ipGenerator struct {
	network *net.IPNet
	nextIP  net.IP
}

func newIPGenerator(network *net.IPNet) *ipGenerator {
	nextIP := make([]byte, len(network.IP))
	copy(nextIP, network.IP)
	gen := &ipGenerator{network: network, nextIP: nextIP}
	// Skip network and gateway addresses
	gen.Next()
	gen.Next()
	return gen
}

func (g *ipGenerator) Network() *net.IPNet {
	n := &net.IPNet{
		IP:   make([]byte, len(g.network.IP)),
		Mask: make([]byte, len(g.network.Mask)),
	}
	copy(n.IP, g.network.IP)
	copy(n.Mask, g.network.Mask)
	return n
}

func (g *ipGenerator) Next() net.IP {
	ip := make([]byte, len(g.nextIP))
	copy(ip, g.nextIP)
	for i := len(g.nextIP) - 1; i >= 0; i-- {
		g.nextIP[i]++
		if g.nextIP[i] != 0 {
			break
		}
	}
	return ip
}

// proTxHashGenerator generates pseudorandom proTxHash based on a seed.
type proTxHashGenerator struct {
	random *rand.Rand
}

func newProTxHashGenerator(seed int64) *proTxHashGenerator {
	return &proTxHashGenerator{
		random: rand.New(rand.NewSource(seed)),
	}
}

func (g *proTxHashGenerator) Generate() crypto.ProTxHash {
	seed := make([]byte, crypto.DefaultHashSize)

	_, err := io.ReadFull(g.random, seed)
	if err != nil {
		panic(err) // this shouldn't happen
	}
	return crypto.ProTxHash(seed)
}

// quorumHashGenerator generates pseudorandom quorumHash based on a seed.
type quorumHashGenerator struct {
	random *rand.Rand
}

func newQuorumHashGenerator(seed int64) *quorumHashGenerator {
	return &quorumHashGenerator{
		random: rand.New(rand.NewSource(seed)),
	}
}

func (g *quorumHashGenerator) Generate() crypto.QuorumHash {
	seed := make([]byte, crypto.DefaultHashSize)

	_, err := io.ReadFull(g.random, seed)
	if err != nil {
		panic(err) // this shouldn't happen
	}
	return crypto.QuorumHash(seed)
}
