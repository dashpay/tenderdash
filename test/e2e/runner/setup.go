package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gogo/protobuf/proto"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	cryptoenc "github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/privval"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
	"github.com/dashpay/tenderdash/test/e2e/pkg/infra"
	"github.com/dashpay/tenderdash/types"
)

const (
	AppAddressTCP  = "tcp://127.0.0.1:30000"
	AppAddressUNIX = "unix:///var/run/app.sock"

	PrivvalAddressTCP      = "tcp://0.0.0.0:27559"
	PrivvalAddressGRPC     = "grpc://0.0.0.0:27559"
	PrivvalAddressUNIX     = "unix:///var/run/privval.sock"
	PrivvalAddressDashCore = "127.0.0.1:19998"
	PrivvalKeyFile         = "config/priv_validator_key.json"
	PrivvalStateFile       = "data/priv_validator_state.json"
	PrivvalDummyKeyFile    = "config/dummy_validator_key.json"
	PrivvalDummyStateFile  = "data/dummy_validator_state.json"

	ABCIGRPC = "grpc"
)

// Setup sets up the testnet configuration.
func Setup(ctx context.Context, logger log.Logger, testnet *e2e.Testnet, ti infra.TestnetInfra) error {
	logger.Info(fmt.Sprintf("Generating testnet files in %q", testnet.Dir))

	err := os.MkdirAll(testnet.Dir, os.ModePerm)
	if err != nil {
		return err
	}

	genesisNodes, err := initGenesisForEveryNode(testnet)
	if err != nil {
		return err
	}
	withModificators(genesisNodes, pubkeyResettingModificator(shouldResetPubkeys()))

	for _, node := range testnet.Nodes {
		genesis, ok := genesisNodes[node.Mode]
		if !ok {
			return fmt.Errorf("node has unsupported node type: %s", node.Mode)
		}

		nodeDir := filepath.Join(testnet.Dir, node.Name)

		dirs := []string{
			filepath.Join(nodeDir, "config"),
			filepath.Join(nodeDir, "data"),
			filepath.Join(nodeDir, "data", "app"),
		}
		for _, dir := range dirs {
			// light clients don't need an app directory
			if node.Mode == e2e.ModeLight && strings.Contains(dir, "app") {
				continue
			}
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				return err
			}
		}

		cfg, err := MakeConfig(node)
		if err != nil {
			return err
		}
		if err := config.WriteConfigFile(nodeDir, cfg); err != nil {
			return err
		}

		appCfg, err := MakeAppConfig(node)
		if err != nil {
			return err
		}
		//nolint:gosec
		// G306: Expect WriteFile permissions to be 0600 or less
		err = os.WriteFile(filepath.Join(nodeDir, "config", "app.toml"), appCfg, 0644)
		if err != nil {
			return err
		}

		if node.Mode == e2e.ModeLight {
			// stop early if a light client
			// Set up a dummy validator for light client verification.
			pv, err := newDefaultFilePV(node, nodeDir)
			if err != nil {
				return err
			}
			err = pv.Save()
			if err != nil {
				return err
			}
			continue
		}

		err = genesis.SaveAs(filepath.Join(nodeDir, "config", "genesis.json"))
		if err != nil {
			return err
		}

		err = (&types.NodeKey{PrivKey: node.NodeKey}).SaveAs(filepath.Join(nodeDir, "config", "node_key.json"))
		if err != nil {
			return err
		}

		if node.Mode == e2e.ModeValidator {
			pv, err := newFilePVFromNode(node, nodeDir)
			if err != nil {
				return err
			}
			err = pv.Save()
			if err != nil {
				return err
			}
		}
		// Set up a dummy validator. Tenderdash requires a file PV even when not used, so we
		// give it a dummy such that it will fail if it actually tries to use it.
		pv, err := newDefaultFilePV(node, nodeDir)
		if err != nil {
			return err
		}
		err = pv.Save()
		if err != nil {
			return err
		}
	}

	if err := ti.Setup(ctx); err != nil {
		return err
	}

	return nil
}

// MakeGenesis generates a genesis document.
func MakeGenesis(testnet *e2e.Testnet, genesisTime time.Time) (types.GenesisDoc, error) {
	genesis := types.GenesisDoc{
		GenesisTime:                  genesisTime,
		ChainID:                      testnet.Name,
		ConsensusParams:              types.DefaultConsensusParams(),
		InitialHeight:                testnet.InitialHeight,
		InitialCoreChainLockedHeight: testnet.GenesisCoreHeight,
		ThresholdPublicKey:           testnet.ThresholdPublicKey,
		QuorumType:                   testnet.QuorumType,
		QuorumHash:                   testnet.QuorumHash,
	}
	genesis.ConsensusParams.Validator.PubKeyTypes =
		append(genesis.ConsensusParams.Validator.PubKeyTypes, types.ABCIPubKeyTypeBLS12381)
	genesis.ConsensusParams.Evidence.MaxAgeNumBlocks = e2e.EvidenceAgeHeight
	genesis.ConsensusParams.Evidence.MaxAgeDuration = e2e.EvidenceAgeTime
	if testnet.MaxEvidenceSize > 0 {
		genesis.ConsensusParams.Evidence.MaxBytes = testnet.MaxEvidenceSize
	}
	if testnet.MaxBlockSize > 0 {
		genesis.ConsensusParams.Block.MaxBytes = testnet.MaxBlockSize
	}

	for validator, validatorUpdate := range testnet.Validators {
		if validatorUpdate.PubKey == nil {
			return genesis, fmt.Errorf("public key for validator %s is nil", validator.Name)
		}
		pubkey, err := cryptoenc.PubKeyFromProto(*validatorUpdate.PubKey)
		if err != nil {
			return genesis, err
		}

		genesis.Validators = append(genesis.Validators, types.GenesisValidator{
			Name:      validator.Name,
			PubKey:    pubkey,
			ProTxHash: validator.ProTxHash,
			Power:     types.DefaultDashVotingPower,
		})
	}
	// The validator set will be sorted internally by Tenderdash ranked by power,
	// but we sort it here as well so that all genesis files are identical.
	sort.Slice(genesis.Validators, func(i, j int) bool {
		return strings.Compare(genesis.Validators[i].Name, genesis.Validators[j].Name) == -1
	})

	genesis.AppState = []byte(testnet.InitialState)

	return genesis, genesis.ValidateAndComplete()
}

// MakeConfig generates a Tenderdash config for a node.
func MakeConfig(node *e2e.Node) (*config.Config, error) {
	cfg := config.DefaultConfig()
	cfg.Moniker = node.Name
	cfg.Abci.Address = AppAddressTCP
	cfg.TxIndex = config.TestTxIndexConfig()

	if node.LogLevel != "" {
		cfg.LogLevel = node.LogLevel
	}

	cfg.Mempool.TxEnqueueTimeout = 10 * time.Millisecond

	cfg.RPC.ListenAddress = "tcp://0.0.0.0:26657"
	cfg.RPC.PprofListenAddress = ":6060"
	cfg.P2P.ExternalAddress = fmt.Sprintf("tcp://%v", node.AddressP2P(false))
	cfg.P2P.QueueType = node.QueueType
	cfg.DBBackend = node.Database
	cfg.StateSync.DiscoveryTime = 5 * time.Second
	if node.Mode != e2e.ModeLight {
		cfg.Mode = string(node.Mode)
	}

	switch node.Testnet.ABCIProtocol {
	case e2e.ProtocolUNIX:
		cfg.Abci.Address = AppAddressUNIX
	case e2e.ProtocolTCP:
		cfg.Abci.Address = AppAddressTCP
	case e2e.ProtocolGRPC:
		cfg.Abci.Address = AppAddressTCP
		cfg.Abci.Transport = ABCIGRPC
	case e2e.ProtocolBuiltin:
		cfg.Abci.Address = ""
		cfg.Abci.Transport = ""
	default:
		return nil, fmt.Errorf("unexpected ABCI protocol setting %q", node.Testnet.ABCIProtocol)
	}

	// Tenderdash errors if it does not have a privval key set up, regardless of whether
	// it's actually needed (e.g. for remote KMS or non-validators). We set up a dummy
	// key here by default, and use the real key for actual validators that should use
	// the file privval.
	cfg.PrivValidator.ListenAddr = ""
	cfg.PrivValidator.Key = PrivvalDummyKeyFile
	cfg.PrivValidator.State = PrivvalDummyStateFile

	if node.PrivvalProtocol == e2e.ProtocolDashCore {
		cfg.PrivValidator.CoreRPCHost = "127.0.0.1:19998"
	}

	switch node.Mode {
	case e2e.ModeValidator:
		switch node.PrivvalProtocol {
		case e2e.ProtocolFile:
			cfg.PrivValidator.Key = PrivvalKeyFile
			cfg.PrivValidator.State = PrivvalStateFile
		case e2e.ProtocolUNIX:
			cfg.PrivValidator.ListenAddr = PrivvalAddressUNIX
		case e2e.ProtocolTCP:
			cfg.PrivValidator.ListenAddr = PrivvalAddressTCP
		case e2e.ProtocolGRPC:
			cfg.PrivValidator.ListenAddr = PrivvalAddressGRPC
		case e2e.ProtocolDashCore:
			cfg.PrivValidator.Key = PrivvalKeyFile
			cfg.PrivValidator.State = PrivvalStateFile
		default:
			return nil, fmt.Errorf("invalid privval protocol setting %q", node.PrivvalProtocol)
		}
	case e2e.ModeSeed, e2e.ModeFull, e2e.ModeLight:
		// Don't need to do anything, since we're using a dummy privval key by default.
	default:
		return nil, fmt.Errorf("unexpected mode %q", node.Mode)
	}

	switch node.StateSync {
	case e2e.StateSyncP2P:
		cfg.StateSync.Enable = true
		cfg.StateSync.UseP2P = true
	case e2e.StateSyncRPC:
		cfg.StateSync.Enable = true
		cfg.StateSync.RPCServers = []string{}
		for _, peer := range node.Testnet.ArchiveNodes() {
			if peer.Name == node.Name {
				continue
			}
			cfg.StateSync.RPCServers = append(cfg.StateSync.RPCServers, peer.AddressRPC())
		}

		if len(cfg.StateSync.RPCServers) < 2 {
			return nil, errors.New("unable to find 2 suitable state sync RPC servers")
		}
	}

	cfg.P2P.PersistentPeers = joinNodeP2PAddresses(node.PersistentPeers, true, ",")
	cfg.P2P.BootstrapPeers = joinNodeP2PAddresses(node.Seeds, true, ",")

	if node.P2PMaxConnections > 0 {
		cfg.P2P.MaxConnections = node.P2PMaxConnections
	}
	if node.P2PMaxOutgoingConnections > 0 {
		cfg.P2P.MaxOutgoingConnections = node.P2PMaxOutgoingConnections
	}
	if node.P2PMaxIncomingConnectionTime > 0 {
		cfg.P2P.MaxIncomingConnectionTime = node.P2PMaxIncomingConnectionTime
	}
	if node.P2PIncomingConnectionWindow > 0 {
		cfg.P2P.IncomingConnectionWindow = node.P2PIncomingConnectionWindow
	}

	cfg.Instrumentation.Prometheus = true

	return cfg, nil
}

func joinNodeP2PAddresses(nodes []*e2e.Node, withID bool, sep string) string {
	addresses := []string{}
	for _, node := range nodes {
		addresses = append(addresses, node.AddressP2P(withID))
	}
	return strings.Join(addresses, sep)
}

// MakeAppConfig generates an ABCI application config for a node.
func MakeAppConfig(node *e2e.Node) ([]byte, error) {
	cfg := map[string]interface{}{
		"chain_id":                  node.Testnet.Name,
		"dir":                       "data/app",
		"listen":                    AppAddressUNIX,
		"mode":                      node.Mode,
		"proxy_port":                node.ProxyPort,
		"protocol":                  "socket",
		"persist_interval":          node.PersistInterval,
		"snapshot_interval":         node.SnapshotInterval,
		"retain_blocks":             node.RetainBlocks,
		"key_type":                  bls12381.KeyType,
		"privval_server_type":       "dashcore",
		"privval_server":            PrivvalAddressDashCore,
		"prepare_proposal_delay_ms": node.Testnet.PrepareProposalDelayMS,
		"process_proposal_delay_ms": node.Testnet.ProcessProposalDelayMS,
		"check_tx_delay_ms":         node.Testnet.CheckTxDelayMS,
		"vote_extension_delay_ms":   node.Testnet.VoteExtensionDelayMS,
		"finalize_block_delay_ms":   node.Testnet.FinalizeBlockDelayMS,
	}

	switch node.Testnet.ABCIProtocol {
	case e2e.ProtocolUNIX:
		cfg["listen"] = AppAddressUNIX
	case e2e.ProtocolTCP:
		cfg["listen"] = AppAddressTCP
	case e2e.ProtocolGRPC:
		cfg["listen"] = AppAddressTCP
		cfg["protocol"] = ABCIGRPC
	case e2e.ProtocolBuiltin:
		delete(cfg, "listen")
		cfg["protocol"] = "builtin"
	default:
		return nil, fmt.Errorf("unexpected ABCI protocol setting %q", node.Testnet.ABCIProtocol)
	}
	if node.Mode == e2e.ModeValidator {
		switch node.PrivvalProtocol {
		case e2e.ProtocolFile:
		case e2e.ProtocolTCP:
			cfg["privval_server_type"] = "tcp"
			cfg["privval_server"] = PrivvalAddressTCP
			cfg["privval_key"] = PrivvalKeyFile
			cfg["privval_state"] = PrivvalStateFile
		case e2e.ProtocolDashCore:
			cfg["privval_server_type"] = "dashcore"
			cfg["privval_server"] = PrivvalAddressDashCore
		case e2e.ProtocolUNIX:
			cfg["privval_server_type"] = "unix"
			cfg["privval_server"] = PrivvalAddressUNIX
			cfg["privval_key"] = PrivvalKeyFile
			cfg["privval_state"] = PrivvalStateFile
		case e2e.ProtocolGRPC:
			cfg["privval_server_type"] = ABCIGRPC
			cfg["privval_server"] = PrivvalAddressGRPC
			cfg["privval_key"] = PrivvalKeyFile
			cfg["privval_state"] = PrivvalStateFile
		default:
			return nil, fmt.Errorf("unexpected privval protocol setting %q", node.PrivvalProtocol)
		}
	} else if node.PrivvalProtocol == e2e.ProtocolDashCore {
		cfg["privval_server_type"] = "dashcore"
		cfg["privval_server"] = PrivvalAddressDashCore
	}

	if len(node.Testnet.ValidatorUpdates) > 0 {
		validatorUpdates := map[string]map[string]string{}
		for height, validators := range node.Testnet.ValidatorUpdates {
			updateVals := map[string]string{}
			for node, validatorUpdate := range validators {
				key := hex.EncodeToString(node.ProTxHash.Bytes())
				update := validatorUpdate // avoid getting address of a range variable to make linter happy
				value, err := proto.Marshal(&update)
				if err != nil {
					return nil, err
				}
				valueBase64 := base64.StdEncoding.EncodeToString(value)
				updateVals[key] = valueBase64
			}
			validatorUpdates[fmt.Sprintf("%v", height)] = updateVals
		}
		cfg["validator_update"] = validatorUpdates

		thresholdPublicKeyUpdates := map[string]string{}
		for height, thresholdPublicKey := range node.Testnet.ThresholdPublicKeyUpdates {

			thresholdPublicKeyUpdates[fmt.Sprintf("%v", height)] = base64.StdEncoding.EncodeToString(thresholdPublicKey.Bytes())
		}
		cfg["threshold_public_key_update"] = thresholdPublicKeyUpdates

		quorumHashUpdates := map[string]string{}
		for height, quorumHash := range node.Testnet.QuorumHashUpdates {

			quorumHashUpdates[fmt.Sprintf("%v", height)] = hex.EncodeToString(quorumHash.Bytes())
		}
		cfg["quorum_hash_update"] = quorumHashUpdates
	}

	if node.Testnet.InitAppCoreHeight > 0 {
		cfg["init_app_core_chain_locked_height"] = node.Testnet.InitAppCoreHeight
	}
	if len(node.Testnet.ChainLockUpdates) > 0 {
		chainLockUpdates := map[string]string{}
		for height, coreHeight := range node.Testnet.ChainLockUpdates {
			chainLockUpdates[fmt.Sprintf("%v", height)] = fmt.Sprintf("%v", coreHeight)
		}
		cfg["chainlock_updates"] = chainLockUpdates
	}

	if len(node.Testnet.ConsensusVersionUpdates) > 0 {
		consensusVersionUpdates := map[string]int32{}
		for height, version := range node.Testnet.ConsensusVersionUpdates {
			consensusVersionUpdates[strconv.Itoa(int(height))] = version //#nosec:G115
		}
		cfg["consensus_version_updates"] = consensusVersionUpdates
	}

	var buf bytes.Buffer
	err := toml.NewEncoder(&buf).Encode(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to generate app config: %w", err)
	}
	return buf.Bytes(), nil
}

// UpdateConfigStateSync updates the state sync config for a node.
func UpdateConfigStateSync(node *e2e.Node, height int64, hash []byte) error {
	cfgPath := filepath.Join(node.Testnet.Dir, node.Name, "config", "config.toml")

	// FIXME Apparently there's no function to simply load a config file without
	// involving the entire Viper apparatus, so we'll just resort to regexps.
	bz, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	bz = regexp.MustCompile(`(?m)^trust-height =.*`).ReplaceAll(bz, []byte(fmt.Sprintf(`trust-height = %v`, height-1)))
	bz = regexp.MustCompile(`(?m)^trust-hash =.*`).ReplaceAll(bz, []byte(fmt.Sprintf(`trust-hash = "%X"`, hash)))
	//nolint: gosec
	// G306: Expect WriteFile permissions to be 0600 or less
	return os.WriteFile(cfgPath, bz, 0644)
}

func newDefaultFilePV(node *e2e.Node, nodeDir string) (*privval.FilePV, error) {
	return privval.NewFilePVWithOptions(
		privval.WithPrivateKeysMap(node.PrivvalKeys),
		privval.WithProTxHash(crypto.RandProTxHash()),
		privval.WithUpdateHeights(node.PrivvalUpdateHeights),
		privval.WithKeyAndStateFilePaths(
			filepath.Join(nodeDir, PrivvalDummyKeyFile),
			filepath.Join(nodeDir, PrivvalDummyStateFile),
		),
	)
}

func newFilePVFromNode(node *e2e.Node, nodeDir string) (*privval.FilePV, error) {
	return privval.NewFilePVWithOptions(
		privval.WithPrivateKeysMap(node.PrivvalKeys),
		privval.WithProTxHash(node.ProTxHash),
		privval.WithUpdateHeights(node.PrivvalUpdateHeights),
		privval.WithKeyAndStateFilePaths(
			filepath.Join(nodeDir, PrivvalKeyFile),
			filepath.Join(nodeDir, PrivvalStateFile),
		),
	)
}

func pubkeyResettingModificator(shouldReset bool) func(genesis map[e2e.Mode]types.GenesisDoc) {
	return func(genesis map[e2e.Mode]types.GenesisDoc) {
		if shouldReset {
			resetPubkey(genesis[e2e.ModeFull].Validators)
		}
	}
}

func resetPubkey(vals []types.GenesisValidator) {
	for i := 0; i < len(vals); i++ {
		vals[i].PubKey = nil
	}
}

func shouldResetPubkeys() bool {
	s := os.Getenv("FULLNODE_PUBKEY_KEEP")
	if s == "" {
		// reset pubkeys - default behavior
		return true
	}
	val, err := strconv.ParseBool(s)
	if err != nil {
		panic(err.Error()) // panic if passed a value that cannot be parsed
	}
	return !val
}

func initGenesisForEveryNode(testnet *e2e.Testnet) (map[e2e.Mode]types.GenesisDoc, error) {
	genesis := make(map[e2e.Mode]types.GenesisDoc)
	genesisTime := tmtime.Now()
	for _, tn := range testnet.Nodes {
		if _, ok := genesis[tn.Mode]; ok {
			continue
		}
		genDoc, err := MakeGenesis(testnet, genesisTime)
		if err != nil {
			return nil, err
		}
		genesis[tn.Mode] = genDoc
	}
	return genesis, nil
}

func withModificators(genesis map[e2e.Mode]types.GenesisDoc, modFuns ...func(map[e2e.Mode]types.GenesisDoc)) {
	for _, fn := range modFuns {
		fn(genesis)
	}
}
