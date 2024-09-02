package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/libs/sync"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

type versionCmd struct {
	nodeKey   string
	peers     []string
	logger    log.Logger
	outFormat string
}

func newVersionCmd(logger log.Logger) Cmd {
	return &versionCmd{
		logger: log.NewNopLogger(),
	}
}

/// curl -s https://mnowatch.org/json/?method=emn_details | jq -r '.[] | "tcp://\(.platformNodeID)@\(.ip):\(.platformP2PPort)#\(.protx_hash)"'

func (c *versionCmd) Command() *cobra.Command {
	versionCmd := &cobra.Command{
		Use:   "version node_1 ... node_n",
		Short: "Connect to another host and display version",
		Long:  "Connect to another host and display version. Peers should be listed as arguments or provided from standard input.",
		Example: `curl -s https://mnowatch.org/json/?method=emn_details | ` +
			`jq -r '.[] | "tcp://\(.platformNodeID)@\(.ip):\(.platformP2PPort)#\(.protx_hash)"' | ` +
			`go run ./cmd/p2p_cli version -k ~/.tenderdash/config/node_key.json`,
		RunE:    c.versionRunE,
		PreRunE: c.versionPreRunE,
	}

	versionCmd.Flags().StringVarP(&c.nodeKey, "key", "k", "", "File with node key to use when running the test")
	versionCmd.Flags().StringVarP(&c.outFormat, "format", "f", "text", "Output format (text, json)")

	return versionCmd
}

// versionPreRunE is a pre-run command that checks if the required flags are set
func (c *versionCmd) versionPreRunE(_cmd *cobra.Command, args []string) error {
	if c.nodeKey == "" {
		return errors.New("node key is required")
	}

	// ensure node key file is readable
	if _, err := os.Stat(c.nodeKey); err != nil {
		return fmt.Errorf("node key file %s is not readable: %w", c.nodeKey, err)
	}

	if len(args) == 0 {
		// load peers from standard input
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				c.peers = append(c.peers, line)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("error reading from stdin: %w", err)
		}
		if len(c.peers) == 0 {
			return errors.New("no peers provided")
		}
	}

	return nil
}

// versionRun implements the version command
func (c *versionCmd) versionRunE(_cmd *cobra.Command, _args []string) error {
	logger := c.logger
	nodeKey, err := types.LoadNodeKey(c.nodeKey)
	if err != nil {
		return fmt.Errorf("failed to load node key: %w", err)
	}

	// parse peers
	addresses, err := parsePeers(c.peers, logger)
	if err != nil {
		return fmt.Errorf("failed to parse peers: %w", err)
	}

	// execute the query
	nodeInfos, errs := QueryNodeInfos(logger, nodeKey, addresses)

	// generate the report
	items := make([]reportItem, 0, len(nodeInfos))
	for label, info := range nodeInfos {
		item := reportItem{
			label:   label,
			address: info.ListenAddr,
			version: info.Version,
			comment: "",
		}

		items = append(items, item)
	}
	for label, e := range errs {
		items = append(items, reportItem{
			label:   label,
			comment: e.Error(),
		})
	}

	return c.output(items)
}

func (c *versionCmd) output(items []reportItem) error {
	switch c.outFormat {
	case "json":
		return c.outputJSON(items)
	default:
		return c.outputText(items)
	}
}

func (c *versionCmd) outputText(items []reportItem) error {
	versions := map[string]int{}
	for _, item := range items {
		if item.comment != "" {
			fmt.Printf("%s: %s\n", item.label, item.comment)
		} else {
			fmt.Printf("%s: %s %s\n", item.label, item.version, item.address)
		}

		if item.version != "" {
			versions[item.version]++
		} else {
			versions["unknown"]++
		}
	}

	fmt.Println("\nVersions statistics:")
	for version, count := range versions {
		fmt.Printf("Version %s: %d\n", version, count)
	}

	return nil
}

func (c *versionCmd) outputJSON(items []reportItem) error {
	d, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}
	fmt.Println(string(d))

	return nil
}

func parsePeers(peers []string, logger log.Logger) (map[string]*types.NetAddress, error) {
	addresses := map[string]*types.NetAddress{}
	for _, addr := range peers {
		// Parse validator address, in form: `tcp://nodeID@host:port`
		addrs := strings.Split(addr, "#")
		addr = addrs[0]

		validatorAddress, err := types.ParseValidatorAddress(addr)
		if err != nil {
			logger.Error("cannot parse validator address", "address", addr, "err", err)
			// report(Stat{label, validatorAddress, "", err.Error()}, format)
			continue
		}

		label := string(validatorAddress.NodeID)
		if len(addrs) > 1 {
			label = addrs[1]
		}

		addr, err := validatorAddress.NetAddress()
		if err != nil {
			return nil, fmt.Errorf("cannot parse address: %w", err)
		}

		if _, ok := addresses[label]; ok {
			return nil, fmt.Errorf("duplicate label: %s", label)
		}

		addresses[label] = addr
	}
	return addresses, nil
}

// QueryNodeInfos queries the node infos of the given peers
// and returns a map of label to node infos.
//
// ## Arguments
//
// - `nodeKey` - the node key to use for the query
// - `peers` - a map of label to net addresses
//
// ## Returns
//
// - a map of label to node infos
// - a map of label to errors, or nil if no errors occurred
//
// It is not guaranteed that all peers will be in the returned maps.
func QueryNodeInfos(logger log.Logger, nodeKey types.NodeKey, peers map[string]*types.NetAddress) (map[string]types.NodeInfo, map[string]error) {
	transport := setupTransport(logger)
	defer transport.Close()

	errs := map[string]error{}
	infos := map[string]types.NodeInfo{}

	for label, addr := range peers {
		peerInfo, err := queryNodeInfo(transport, *addr, nodeKey.PrivKey)
		if err != nil {
			errs[label] = err
		} else {
			infos[label] = peerInfo
		}
	}

	if len(errs) == 0 {
		errs = nil
	}

	return infos, errs
}

func setupTransport(logger log.Logger) *p2p.MConnTransport {
	cfg := conn.DefaultMConnConfig()
	chDesc := []*conn.ChannelDescriptor{}
	opts := p2p.MConnTransportOptions{}
	return p2p.NewMConnTransport(logger, cfg, chDesc, opts)
}

func queryNodeInfo(transport *p2p.MConnTransport, addr types.NetAddress, ourKey crypto.PrivKey) (types.NodeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	endpoint := p2p.Endpoint{
		Protocol: "tcp",
		IP:       addr.IP,
		Port:     addr.Port,
	}
	conn, err := transport.Dial(ctx, &endpoint)
	if err != nil {
		return types.NodeInfo{}, fmt.Errorf("cannot dial: %w", err)
	}

	ourInfo := types.NodeInfo{
		NodeID: types.NodeIDFromPubKey(ourKey.PubKey()),
		ProtocolVersion: types.ProtocolVersion{
			P2P:   version.P2PProtocol,
			Block: version.BlockProtocol,
			App:   1,
		},

		// ListenAddr: "127.0.0.1:26656",
		// Network:    "dash-1",
		Version:  version.TMCoreSemVer,
		Channels: sync.NewConcurrentSlice[uint16](0xf0, 0x0f),
		Moniker:  "version-checker",
	}

	peerInfo, _, err := conn.Handshake(ctx, 5*time.Second, ourInfo, ourKey)
	if err != nil {
		return peerInfo, fmt.Errorf("handshake failed: %w", err)
	}
	conn.Close()

	return peerInfo, nil
}

type reportItem struct {
	label   string
	address string
	version string
	comment string
}
