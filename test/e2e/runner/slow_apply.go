package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
)

// This command is deliberately separate from RunE and the default test binary.
func newSlowApplyCommand(cli *CLI, logger log.Logger) *cobra.Command {
	var image string
	cmd := &cobra.Command{Use: "slow-apply", Short: "Run the manual block-sync local-application backpressure scenario", RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
		defer cancel()
		return runSlowApply(ctx, cli, logger, image)
	}}
	cmd.Flags().StringVar(&image, "image", "tenderdash/e2e-node", "E2E node image built from this checkout")
	return cmd
}

func runSlowApply(ctx context.Context, cli *CLI, logger log.Logger, image string) error {
	net := cli.testnet
	if len(net.Nodes) != 5 || net.Name == "" || net.ABCIProtocol != e2e.ProtocolBuiltin {
		return errors.New("use the five-node manual/slow-apply.toml manifest")
	}
	var lag, validator *e2e.Node
	var followers []*e2e.Node
	for _, n := range net.Nodes {
		switch {
		case n.Name == "slowapply-full":
			lag = n
		case n.Name == "slowapply-v1" && n.Mode == e2e.ModeValidator:
			validator = n
		case n.Mode == e2e.ModeFull && n.StartAt == 0 && n.StateSync == e2e.StateSyncDisabled && n.PrivvalProtocol == e2e.ProtocolDashCore:
			followers = append(followers, n)
		}
		if err := exec.CommandContext(ctx, "docker", "inspect", "--", n.Name).Run(); err == nil { //nolint:gosec // G204: no shell; container name follows --.
			return fmt.Errorf("container %s already exists; refusing to replace it", n.Name)
		}
	}
	if validator == nil || lag == nil || len(followers) != 3 || lag.Mode != e2e.ModeFull || lag.StateSync != e2e.StateSyncDisabled || lag.StartAt < 100 || lag.PrivvalProtocol != e2e.ProtocolDashCore {
		return errors.New("need one genesis validator, three full sources, and slowapply-full with state sync disabled at height >=100")
	}
	sources := append([]*e2e.Node{validator}, followers...)
	if _, err := os.Stat(net.Dir); !os.IsNotExist(err) {
		return fmt.Errorf("generated directory %s already exists; use a fresh manifest path", net.Dir)
	}
	if err := Setup(ctx, logger, net, cli.infra); err != nil {
		return err
	}
	// Bound block production, not response delivery, to bound workload growth during catchup.
	validatorConfig, err := MakeConfig(validator)
	if err != nil {
		return err
	}
	validatorConfig.Consensus.CreateEmptyBlocksInterval = time.Second
	if err := config.WriteConfigFile(filepath.Join(net.Dir, validator.Name), validatorConfig); err != nil {
		return err
	}
	compose := filepath.Join(net.Dir, "docker-compose.yml")
	data, err := os.ReadFile(compose)
	if err != nil {
		return err
	}
	data = []byte(strings.ReplaceAll(string(data), "image: tenderdash/e2e-node", "image: "+strconv.Quote(image)))
	// The manifest selects this task-owned output directory, just as Setup does.
	if err := os.WriteFile(compose, data, 0600); err != nil { //nolint:gosec // G703: explicit local output path.
		return err
	}
	// Never use the existing global e2e-label cleanup: other test networks may be running.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		logs := exec.CommandContext(cleanup, "docker", "compose", "-f", compose, "logs", "--no-color")
		if out, err := logs.CombinedOutput(); err == nil {
			_ = os.WriteFile(filepath.Join(net.Dir, "nodes.log"), out, 0600)
		}
		down := exec.CommandContext(cleanup, "docker", "compose", "-f", compose, "down", "--remove-orphans")
		if out, err := down.CombinedOutput(); err != nil {
			logger.Error("manual network cleanup failed", "error", err, "output", string(out))
		}
	}()
	gate := filepath.Join(net.Dir, lag.Name, "slow-apply.gate")
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		return err
	}
	cfg, err := os.OpenFile(filepath.Join(net.Dir, lag.Name, "config", "app.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cfg, "\nfinalize_block_gate = \"/tenderdash/slow-apply.gate\"")
	closeErr := cfg.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// A sole genesis validator goes directly to consensus. Start each serving full
	// node only after its source is ready, before connecting the gated target.
	for _, n := range sources {
		if err := cli.infra.StartNode(ctx, n); err != nil {
			return err
		}
		if err := slowWaitSource(ctx, n, lag.StartAt); err != nil {
			return err
		}
	}
	source, err := validator.Client()
	if err != nil {
		return err
	}
	// Small blocks keep average transfer volume low without throttling healthy sources.
	for h := int64(2); h <= lag.StartAt; h++ {
		b, e := source.Block(ctx, &h)
		if e != nil {
			return e
		}
		if b.Block.Size() > 2048 {
			return fmt.Errorf("block %d is not small: %d bytes", h, b.Block.Size())
		}
	}
	if err := cli.infra.StartNode(ctx, lag); err != nil {
		return err
	}
	gateCtx, cancelGate := context.WithTimeout(ctx, 45*time.Second)
	defer cancelGate()
	if err := slowPoll(gateCtx, 100*time.Millisecond, func() (bool, error) { _, e := os.Stat(gate + ".entered"); return e == nil, nil }); err != nil {
		return fmt.Errorf("FinalizeBlock gate was not reached: %w", err)
	}
	logger.Info("manual FinalizeBlock gate reached; network sources remain running")
	// Verify the application is waiting inside the injected gate, not on the network.
	portOut, err := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "port", lag.Name, "6060").Output()
	if err != nil {
		return err
	}
	port := strings.TrimSpace(string(portOut))
	port = port[strings.LastIndex(port, ":")+1:]
	profile, err := slowHTTP(ctx, "http://127.0.0.1:"+port+"/debug/pprof/goroutine?debug=2")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(net.Dir, "blocked-goroutines.txt"), []byte(profile), 0600); err != nil {
		return err
	}
	if !strings.Contains(profile, "waitForFinalizeGate") {
		return errors.New("goroutine profile does not show the application gate")
	}
	// Received responses accumulate behind the held application. This establishes
	// local buffering, without claiming that the 600-slot result queue is full.
	metricsURL := fmt.Sprintf("http://127.0.0.1:%d/metrics", lag.ProxyPort+1000)
	var held slowMetrics
	backlogCtx, cancelBacklog := context.WithTimeout(ctx, 30*time.Second)
	defer cancelBacklog()
	if err := slowPoll(backlogCtx, 200*time.Millisecond, func() (bool, error) {
		m, e := readSlowMetrics(ctx, metricsURL)
		held = m
		return e == nil && m.responses > 16*1024, nil
	}); err != nil {
		return fmt.Errorf("no measurable received block-response backlog: %w", err)
	}
	client, err := lag.Client()
	if err != nil {
		return err
	}
	stored, err := client.BlockchainInfo(ctx, 0, 0)
	if err != nil {
		return err
	}
	if stored.LastHeight != 1 {
		return fmt.Errorf("expected gate on first block, stored height %d", stored.LastHeight)
	}
	held.requests, err = slowRequestBytes(ctx, sources, lag)
	if err != nil {
		return err
	}
	logger.Info("received block responses with FinalizeBlock blocked", "bytes", held.responses, "requests", held.requests)
	until := time.Now().Add(70 * time.Second)
	for time.Now().Before(until) {
		m, e := readSlowMetrics(ctx, metricsURL)
		if e != nil {
			return e
		}
		if m.syncing != 1 || m.peers < 2 || m.evicted != 0 {
			return fmt.Errorf("local application hold caused handoff/eviction: %+v", m)
		}
		stored, e := client.BlockchainInfo(ctx, 0, 0)
		if e != nil {
			return e
		}
		if stored.LastHeight != 1 {
			return fmt.Errorf("block store advanced while application held: %d", stored.LastHeight)
		}
		m.requests, e = slowRequestBytes(ctx, sources, lag)
		if e != nil {
			return e
		}
		held = m
		if err := slowPause(ctx, time.Second); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(net.Dir, "held-metrics.txt"), []byte(held.raw), 0600); err != nil {
		return err
	}
	if err := os.Remove(gate); err != nil {
		return err
	}
	logger.Info("released local application; require requests beyond the initial window")
	recoveryCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := slowPoll(recoveryCtx, time.Second, func() (bool, error) {
		s, e := client.Status(recoveryCtx)
		if e != nil {
			return false, nil
		}
		tip, e := source.Status(ctx)
		if e != nil {
			return false, nil
		}
		m, e := readSlowMetrics(ctx, metricsURL)
		if e != nil {
			return false, e
		}
		m.requests, e = slowRequestBytes(recoveryCtx, sources, lag)
		if e != nil {
			return false, e
		}
		if m.evicted != 0 {
			return false, errors.New("healthy peer evicted during recovery")
		}
		return s.SyncInfo.LatestBlockHeight >= lag.StartAt && tip.SyncInfo.LatestBlockHeight-s.SyncInfo.LatestBlockHeight <= 2 && !s.SyncInfo.CatchingUp && m.requests > held.requests && m.responses > held.responses, nil
	}); err != nil {
		return fmt.Errorf("application release did not restore source eligibility and catchup: %w", err)
	}
	final, err := readSlowMetrics(ctx, metricsURL)
	if err != nil {
		return err
	}
	final.requests, err = slowRequestBytes(ctx, sources, lag)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(net.Dir, "recovered-metrics.txt"), []byte(final.raw), 0600); err != nil {
		return err
	}
	logger.Info("manual slow-apply assertions passed", "held_response_bytes", held.responses, "recovered_response_bytes", final.responses, "held_request_bytes", held.requests, "recovered_request_bytes", final.requests)
	return nil
}

type slowMetrics struct {
	responses, requests, syncing, evicted, peers float64
	raw                                          string
}

func readSlowMetrics(ctx context.Context, url string) (slowMetrics, error) {
	raw, err := slowHTTP(ctx, url)
	if err != nil {
		return slowMetrics{}, err
	}
	m := slowMetrics{raw: raw, syncing: -1, peers: -1}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.HasPrefix(line, "#") {
			continue
		}
		v, e := strconv.ParseFloat(f[1], 64)
		if e != nil {
			return m, e
		}
		switch {
		case strings.Contains(f[0], "p2p_peer_receive_bytes_total{") && strings.Contains(f[0], `message_type="blocksync_BlockResponse"`):
			m.responses += v
		case strings.Contains(f[0], "p2p_peer_receive_bytes_total{") && strings.Contains(f[0], `message_type="blocksync_BlockRequest"`):
			m.requests += v
		case strings.Contains(f[0], "consensus_block_syncing"):
			m.syncing = v
		case strings.Contains(f[0], "p2p_peers_connected{"):
			m.peers = v
		case strings.Contains(f[0], "p2p_peers_evicted"):
			m.evicted += v
		}
	}
	if m.syncing < 0 || m.peers < 0 {
		return m, errors.New("missing block-sync or connected-peer metrics")
	}
	return m, nil
}
func slowHTTP(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return string(data), err
}
func slowPoll(ctx context.Context, interval time.Duration, fn func() (bool, error)) error {
	for {
		ok, err := fn()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if err := slowPause(ctx, interval); err != nil {
			return err
		}
	}
}
func slowPause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Observe requests at the sources because the router does not export sent bytes.
func slowRequestBytes(ctx context.Context, sources []*e2e.Node, lag *e2e.Node) (float64, error) {
	var total float64
	peerLabel := fmt.Sprintf(`peer_id="%x"`, lag.NodeKey.PubKey().Address().Bytes())
	for _, n := range sources {
		raw, err := slowHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/metrics", n.ProxyPort+1000))
		if err != nil {
			return 0, err
		}
		for _, line := range strings.Split(raw, "\n") {
			if !strings.Contains(line, "p2p_peer_receive_bytes_total{") || !strings.Contains(line, `message_type="blocksync_BlockRequest"`) || !strings.Contains(line, peerLabel) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				return 0, errors.New("malformed source request metric")
			}
			value, err := strconv.ParseFloat(fields[1], 64)
			if err != nil {
				return 0, err
			}
			total += value
		}
	}
	return total, nil
}

func slowWaitSource(ctx context.Context, n *e2e.Node, height int64) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	client, err := n.Client()
	if err != nil {
		return err
	}
	if err := slowPoll(ctx, time.Second, func() (bool, error) {
		status, err := client.Status(ctx)
		return err == nil && status.SyncInfo.LatestBlockHeight >= height && !status.SyncInfo.CatchingUp, nil
	}); err != nil {
		return fmt.Errorf("source %s did not reach height %d: %w", n.Name, height, err)
	}
	return nil
}
