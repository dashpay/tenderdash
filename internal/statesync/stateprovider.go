package statesync

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/light"
	lightprovider "github.com/dashpay/tenderdash/light/provider"
	lighthttp "github.com/dashpay/tenderdash/light/provider/http"
	lightrpc "github.com/dashpay/tenderdash/light/rpc"
	lightdb "github.com/dashpay/tenderdash/light/store/db"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	rpchttp "github.com/dashpay/tenderdash/rpc/client/http"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

//go:generate ../../scripts/mockery_generate.sh StateProvider

// StateProvider is a provider of trusted state data for bootstrapping a node. This refers
// to the state.State object, not the state machine. There are two implementations. One
// uses the P2P layer and the other uses the RPC layer. Both use light client verification.
type StateProvider interface {
	// AppHash returns the app hash after the given height has been committed.
	AppHash(ctx context.Context, height uint64) (tmbytes.HexBytes, error)
	// Commit returns the commit at the given height.
	Commit(ctx context.Context, height uint64) (*types.Commit, error)
	// State returns a state object at the given height.
	State(ctx context.Context, height uint64) (sm.State, error)
}

// stateProviderRPC is a state provider using RPC to communicate with light clients.
// Deprecated, will be removed in future.
type stateProviderRPC struct {
	sync.Mutex                  // light.Client is not concurrency-safe
	lc                          *light.Client
	initialHeight               int64
	providers                   map[lightprovider.Provider]string
	logger                      log.Logger
	dashCoreClient              dashcore.Client
	trustedVotingPowerThreshold *uint64
}

// NewRPCStateProvider creates a new StateProvider using a light client and RPC clients.
// Deprecated, will be removed in future.
func NewRPCStateProvider(
	ctx context.Context,
	chainID string,
	initialHeight int64,
	servers []string,
	logger log.Logger,
	dashCoreClient dashcore.Client,
) (StateProvider, error) {
	return newRPCStateProvider(ctx, chainID, initialHeight, servers, logger, dashCoreClient, nil)
}

func newRPCStateProvider(
	ctx context.Context,
	chainID string,
	initialHeight int64,
	servers []string,
	logger log.Logger,
	dashCoreClient dashcore.Client,
	trustedVotingPowerThreshold *uint64,
) (StateProvider, error) {
	if len(servers) < 2 {
		return nil, fmt.Errorf("at least 2 RPC servers are required, got %d", len(servers))
	}

	providers := make([]lightprovider.Provider, 0, len(servers))
	providerRemotes := make(map[lightprovider.Provider]string)
	for _, server := range servers {
		client, err := rpcClient(server)
		if err != nil {
			return nil, fmt.Errorf("failed to set up RPC client: %w", err)
		}
		provider := lighthttp.NewWithClient(chainID, client)
		providers = append(providers, provider)
		// We store the RPC addresses keyed by provider, so we can find the address of the primary
		// provider used by the light client and use it to fetch consensus parameters.
		providerRemotes[provider] = server
	}
	lc, err := light.NewClient(ctx, chainID, providers[0], providers[1:],
		lightdb.New(dbm.NewMemDB()), dashCoreClient, light.Logger(logger))
	if err != nil {
		return nil, err
	}
	return &stateProviderRPC{
		logger:                      logger,
		lc:                          lc,
		initialHeight:               initialHeight,
		providers:                   providerRemotes,
		dashCoreClient:              dashCoreClient,
		trustedVotingPowerThreshold: cloneUint64Ptr(trustedVotingPowerThreshold),
	}, nil
}

func (s *stateProviderRPC) verifyLightBlockAtHeight(ctx context.Context, height uint64, ts time.Time) (*types.LightBlock, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return s.lc.VerifyLightBlockAtHeight(ctx, int64(height), ts)
}

// AppHash implements part of StateProvider. It calls the application to verify the
// light blocks at heights h+1 and h+2 and, if verification succeeds, reports the app
// hash for the block at height h+1 which correlates to the state at height h.
func (s *stateProviderRPC) AppHash(ctx context.Context, height uint64) (tmbytes.HexBytes, error) {
	s.Lock()
	defer s.Unlock()

	header, err := s.verifyLightBlockAtHeight(ctx, height, time.Now())
	if err != nil {
		return nil, err
	}

	return header.AppHash, nil
}

// Commit implements StateProvider.
func (s *stateProviderRPC) Commit(ctx context.Context, height uint64) (*types.Commit, error) {
	s.Lock()
	defer s.Unlock()
	header, err := s.verifyLightBlockAtHeight(ctx, height, time.Now())
	if err != nil {
		return nil, err
	}
	return header.Commit, nil
}

// State implements StateProvider.
func (s *stateProviderRPC) State(ctx context.Context, height uint64) (sm.State, error) {
	s.Lock()
	defer s.Unlock()

	state := sm.State{
		ChainID:       s.lc.ChainID(),
		InitialHeight: s.initialHeight,
	}
	if state.InitialHeight == 0 {
		state.InitialHeight = 1
	}

	// The snapshot height maps onto the state heights as follows:
	//
	// height: last block, i.e. the snapshotted height
	// height+1: current block, i.e. the first block we'll process after the snapshot
	// height+2: next block, i.e. the second block after the snapshot
	lastLightBlock, err := s.verifyLightBlockAtHeight(ctx, height, time.Now())
	if err != nil {
		return sm.State{}, err
	}
	currentLightBlock, err := s.verifyLightBlockAtHeight(ctx, height+1, time.Now())
	if err != nil {
		return sm.State{}, err
	}

	state.Version = sm.Version{
		Consensus: currentLightBlock.Version,
		Software:  version.TMCoreSemVer,
	}
	state.LastBlockHeight = lastLightBlock.Height
	state.LastBlockRound = lastLightBlock.Commit.Round
	state.LastBlockTime = lastLightBlock.Time
	state.LastBlockID = lastLightBlock.Commit.BlockID
	state.LastCoreChainLockedBlockHeight = lastLightBlock.Header.CoreChainLockedHeight
	state.LastAppHash = currentLightBlock.AppHash
	state.LastResultsHash = currentLightBlock.ResultsHash
	state.LastValidators, err = authenticateStateSyncValidatorSet(lastLightBlock, s.dashCoreClient)
	if err != nil {
		return sm.State{}, fmt.Errorf("authenticating last validator set: %w", err)
	}
	state.Validators, err = authenticateStateSyncValidatorSet(currentLightBlock, s.dashCoreClient)
	if err != nil {
		return sm.State{}, fmt.Errorf("authenticating current validator set: %w", err)
	}
	state.LastHeightValidatorsChanged = currentLightBlock.Height

	// We'll also need to fetch consensus params via RPC, using light client verification.
	primaryURL, ok := s.providers[s.lc.Primary()]
	if !ok || primaryURL == "" {
		return sm.State{}, fmt.Errorf("could not find address for primary light client provider")
	}
	primaryRPC, err := rpcClient(primaryURL)
	if err != nil {
		return sm.State{}, fmt.Errorf("unable to create RPC client: %w", err)
	}
	rpcclient := lightrpc.NewClient(s.logger, primaryRPC, s.lc)
	result, err := rpcclient.ConsensusParams(ctx, &currentLightBlock.Height)
	if err != nil {
		return sm.State{}, fmt.Errorf("unable to fetch consensus parameters for height %v: %w",
			currentLightBlock.Height, err)
	}
	if err := verifyConsensusParams(
		result.ConsensusParams,
		currentLightBlock.ConsensusHash,
		currentLightBlock.Height,
		s.trustedVotingPowerThreshold,
	); err != nil {
		return sm.State{}, err
	}
	state.ConsensusParams = result.ConsensusParams
	applyVotingPowerThreshold(&state)
	state.LastHeightConsensusParamsChanged = currentLightBlock.Height

	return state, nil
}

// rpcClient sets up a new RPC client
func rpcClient(server string) (*rpchttp.HTTP, error) {
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	return rpchttp.New(server)
}

type stateProviderP2P struct {
	sync.Mutex                  // light.Client is not concurrency-safe
	lc                          *light.Client
	initialHeight               int64
	paramsSendCh                p2p.Channel
	paramsRecvCh                chan types.ConsensusParams
	dashCoreClient              dashcore.Client
	trustedVotingPowerThreshold *uint64
}

// NewP2PStateProvider creates a light client state
// provider but uses a dispatcher connected to the P2P layer
func NewP2PStateProvider(
	ctx context.Context,
	chainID string,
	initialHeight int64,
	providers []lightprovider.Provider,
	paramsSendCh p2p.Channel,
	logger log.Logger,
	dashCoreClient dashcore.Client,
) (StateProvider, error) {
	return newP2PStateProvider(ctx, chainID, initialHeight, providers, paramsSendCh, logger, dashCoreClient, nil)
}

func newP2PStateProvider(
	ctx context.Context,
	chainID string,
	initialHeight int64,
	providers []lightprovider.Provider,
	paramsSendCh p2p.Channel,
	logger log.Logger,
	dashCoreClient dashcore.Client,
	trustedVotingPowerThreshold *uint64,
) (StateProvider, error) {
	if len(providers) < minPeers {
		return nil, fmt.Errorf("at least %d peers are required, got %d", minPeers, len(providers))
	}

	lc, err := light.NewClient(ctx, chainID, providers[0], providers[1:],
		lightdb.New(dbm.NewMemDB()), dashCoreClient, light.Logger(logger))
	if err != nil {
		return nil, err
	}

	return &stateProviderP2P{
		lc:                          lc,
		initialHeight:               initialHeight,
		paramsSendCh:                paramsSendCh,
		paramsRecvCh:                make(chan types.ConsensusParams),
		dashCoreClient:              dashCoreClient,
		trustedVotingPowerThreshold: cloneUint64Ptr(trustedVotingPowerThreshold),
	}, nil
}

func (s *stateProviderP2P) verifyLightBlockAtHeight(ctx context.Context, height uint64, ts time.Time) (*types.LightBlock, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return s.lc.VerifyLightBlockAtHeight(ctx, int64(height), ts)
}

// AppHash implements StateProvider.
func (s *stateProviderP2P) AppHash(ctx context.Context, height uint64) (tmbytes.HexBytes, error) {
	s.Lock()
	defer s.Unlock()

	header, err := s.verifyLightBlockAtHeight(ctx, height, time.Now())
	if err != nil {
		return nil, err
	}

	return header.AppHash, nil
}

// Commit implements StateProvider.
func (s *stateProviderP2P) Commit(ctx context.Context, height uint64) (*types.Commit, error) {
	s.Lock()
	defer s.Unlock()
	header, err := s.verifyLightBlockAtHeight(ctx, height, time.Now())
	if err != nil {
		return nil, err
	}
	return header.Commit, nil
}

// State implements StateProvider.
func (s *stateProviderP2P) State(ctx context.Context, height uint64) (sm.State, error) {
	s.Lock()
	defer s.Unlock()

	state := sm.State{
		ChainID:       s.lc.ChainID(),
		InitialHeight: s.initialHeight,
	}
	if state.InitialHeight == 0 {
		state.InitialHeight = 1
	}

	// The snapshot height maps onto the state heights as follows:
	//
	// height: last block, i.e. the snapshotted height
	// height+1: current block, i.e. the first block we'll process after the snapshot
	// height+2: next block, i.e. the second block after the snapshot
	lastLightBlock, err := s.verifyLightBlockAtHeight(ctx, height, time.Now())
	if err != nil {
		return sm.State{}, err
	}
	currentLightBlock, err := s.verifyLightBlockAtHeight(ctx, height+1, time.Now())
	if err != nil {
		return sm.State{}, err
	}

	state.Version = sm.Version{
		Consensus: currentLightBlock.Version,
		Software:  version.TMCoreSemVer,
	}
	state.LastBlockHeight = lastLightBlock.Height
	state.LastBlockRound = lastLightBlock.Commit.Round
	state.LastBlockTime = lastLightBlock.Time
	state.LastBlockID = lastLightBlock.Commit.BlockID
	state.LastCoreChainLockedBlockHeight = lastLightBlock.Header.CoreChainLockedHeight
	state.LastAppHash = currentLightBlock.AppHash
	state.LastResultsHash = currentLightBlock.ResultsHash
	state.LastValidators, err = authenticateStateSyncValidatorSet(lastLightBlock, s.dashCoreClient)
	if err != nil {
		return sm.State{}, fmt.Errorf("authenticating last validator set: %w", err)
	}
	state.Validators, err = authenticateStateSyncValidatorSet(currentLightBlock, s.dashCoreClient)
	if err != nil {
		return sm.State{}, fmt.Errorf("authenticating current validator set: %w", err)
	}
	state.LastHeightValidatorsChanged = currentLightBlock.Height

	// We'll also need to fetch consensus params via P2P.
	state.ConsensusParams, err = s.consensusParams(ctx, currentLightBlock.Height)
	if err != nil {
		return sm.State{}, fmt.Errorf("fetching consensus params: %w", err)
	}
	if err := verifyConsensusParams(
		state.ConsensusParams,
		currentLightBlock.ConsensusHash,
		currentLightBlock.Height,
		s.trustedVotingPowerThreshold,
	); err != nil {
		return sm.State{}, err
	}
	applyVotingPowerThreshold(&state)
	// set the last height changed to the current height
	state.LastHeightConsensusParamsChanged = currentLightBlock.Height

	return state, nil
}

// authenticateStateSyncValidatorSet replaces validator membership and live
// consensus controls supplied by a light-block peer with values from local Dash
// Core and the signed header. The legacy ValidatorsHash authenticates only the
// threshold key and quorum hash, so copying those peer fields into state is unsafe.
func authenticateStateSyncValidatorSet(
	lightBlock *types.LightBlock,
	dashCoreClient dashcore.Client,
) (*types.ValidatorSet, error) {
	if lightBlock == nil || lightBlock.ValidatorSet == nil || lightBlock.Header == nil {
		return nil, errors.New("light block is missing header or validator set")
	}
	if dashCoreClient == nil {
		return nil, errors.New("cannot authenticate validator membership without a Dash Core client")
	}

	wireSet := lightBlock.ValidatorSet
	info, err := dashCoreClient.QuorumInfo(wireSet.QuorumType, wireSet.QuorumHash)
	if err != nil {
		return nil, fmt.Errorf("querying quorum info: %w", err)
	}
	if info == nil {
		return nil, errors.New("received nil quorum info from Dash Core")
	}
	return authenticateStateSyncValidatorSetWithQuorumInfo(lightBlock, info)
}

func authenticateStateSyncValidatorSetWithQuorumInfo(
	lightBlock *types.LightBlock,
	info *btcjson.QuorumInfoResult,
) (*types.ValidatorSet, error) {
	if lightBlock == nil || lightBlock.ValidatorSet == nil || lightBlock.Header == nil || info == nil {
		return nil, errors.New("validator-set authentication input is incomplete")
	}
	wireSet := lightBlock.ValidatorSet
	quorumType := btcjson.GetLLMQType(info.Type)
	if quorumType.Validate() != nil || quorumType != wireSet.QuorumType {
		return nil, fmt.Errorf("quorum type %q returned by Dash Core does not match light block %d", info.Type, wireSet.QuorumType)
	}

	quorumHash, err := hex.DecodeString(info.QuorumHash)
	if err != nil || !bytes.Equal(quorumHash, wireSet.QuorumHash) {
		return nil, fmt.Errorf("quorum hash %q returned by Dash Core does not match light block %X", info.QuorumHash, wireSet.QuorumHash)
	}
	thresholdKeyBytes, err := hex.DecodeString(info.QuorumPublicKey)
	if err != nil || len(thresholdKeyBytes) != bls12381.PubKeySize {
		return nil, fmt.Errorf("invalid Dash Core threshold public key %q", info.QuorumPublicKey)
	}
	thresholdKey := bls12381.PubKey(thresholdKeyBytes)
	if !thresholdKey.Equals(wireSet.ThresholdPublicKey) {
		return nil, errors.New("threshold public key returned by Dash Core does not match light block")
	}

	validators := make([]*types.Validator, 0, len(info.Members))
	hasPublicKeys := true
	seen := make(map[string]struct{}, len(info.Members))
	for i, member := range info.Members {
		if !member.Valid {
			continue
		}
		proTxHash, err := hex.DecodeString(member.ProTxHash)
		if err != nil || len(proTxHash) != crypto.ProTxHashSize {
			return nil, fmt.Errorf("invalid quorum member proTxHash at index %d", i)
		}
		key := string(proTxHash)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate quorum member proTxHash at index %d", i)
		}
		seen[key] = struct{}{}

		var pubKey bls12381.PubKey
		if member.PubKeyShare == "" {
			hasPublicKeys = false
		} else {
			pubKeyBytes, err := hex.DecodeString(member.PubKeyShare)
			if err != nil || len(pubKeyBytes) != bls12381.PubKeySize {
				return nil, fmt.Errorf("invalid quorum member public-key share at index %d", i)
			}
			pubKey = bls12381.PubKey(pubKeyBytes)
		}
		validator := &types.Validator{
			ProTxHash:   types.ProTxHash(proTxHash),
			PubKey:      pubKey,
			VotingPower: types.DefaultDashVotingPower,
		}
		validators = append(validators, validator)
	}
	if len(validators) == 0 {
		return nil, errors.New("quorum returned by Dash Core has no valid members")
	}
	if !hasPublicKeys {
		for _, validator := range validators {
			validator.PubKey = nil
		}
	}

	authenticated := types.NewValidatorSet(
		validators,
		thresholdKey,
		quorumType,
		wireSet.QuorumHash.Copy(),
		hasPublicKeys,
		nil,
	)
	if err := authenticated.SetProposer(lightBlock.ProposerProTxHash); err != nil {
		return nil, fmt.Errorf("signed proposer is not a valid quorum member: %w", err)
	}
	if err := authenticated.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("authenticated validator set is invalid: %w", err)
	}
	return authenticated, nil
}

// verifyConsensusParams checks peer-supplied consensus params before Bootstrap
// persists them. The legacy consensus hash covers only block and version fields,
// so the validator threshold must match the value stored from local genesis.
func verifyConsensusParams(
	params types.ConsensusParams,
	expectedHash tmbytes.HexBytes,
	height int64,
	trustedVotingPowerThreshold *uint64,
) error {
	if err := params.ValidateConsensusParams(); err != nil {
		return fmt.Errorf("invalid consensus params at height %d: %w", height, err)
	}
	if !bytes.Equal(expectedHash, params.HashConsensusParams()) {
		return fmt.Errorf("consensus params hash mismatch at height %d. Expected %v, got %v",
			height, expectedHash, params.HashConsensusParams())
	}
	if !equalUint64Ptr(params.Validator.VotingPowerThreshold, trustedVotingPowerThreshold) {
		return fmt.Errorf("consensus params voting power threshold at height %d does not match trusted local state", height)
	}
	return nil
}

func equalUint64Ptr(a, b *uint64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func cloneUint64Ptr(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func applyVotingPowerThreshold(state *sm.State) {
	threshold := state.ConsensusParams.Validator.VotingPowerThreshold
	if threshold == nil {
		return
	}
	if state.LastValidators != nil {
		state.LastValidators.VotingPowerThreshold = *threshold
	}
	if state.Validators != nil {
		state.Validators.VotingPowerThreshold = *threshold
	}
}

// addProvider dynamically adds a peer as a new witness. A limit of 6 providers is kept as a
// heuristic. Too many overburdens the network and too little compromises the second layer of security.
func (s *stateProviderP2P) addProvider(p lightprovider.Provider) {
	if len(s.lc.Witnesses()) < 6 {
		s.lc.AddProvider(p)
	}
}

// consensusParams sends out a request for consensus params blocking
// until one is returned.
//
// It attempts to send requests to all witnesses in parallel, but if
// none responds it will retry them all sometime later until it
// receives some response. This operation will block until it receives
// a response or the context is canceled.
func (s *stateProviderP2P) consensusParams(ctx context.Context, height int64) (types.ConsensusParams, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out := make(chan types.ConsensusParams)

	retryAll := func() (<-chan struct{}, error) {
		wg := &sync.WaitGroup{}

		for _, provider := range s.lc.Witnesses() {
			p, ok := provider.(*BlockProvider)
			if !ok {
				return nil, fmt.Errorf("witness is not BlockProvider [%T]", provider)
			}

			peer, err := types.NewNodeID(p.String())
			if err != nil {
				return nil, fmt.Errorf("invalid provider (%s) node id: %w", p.String(), err)
			}

			wg.Add(1)
			go func(peer types.NodeID) {
				defer wg.Done()

				timer := time.NewTimer(0)
				defer timer.Stop()
				var iterCount int64

				for {
					iterCount++
					if err := s.paramsSendCh.Send(ctx, p2p.Envelope{
						To: peer,
						Message: &ssproto.ParamsRequest{
							Height: uint64(height),
						},
					}); err != nil {
						// this only errors if
						// the context is
						// canceled which we
						// don't need to
						// propagate here
						return
					}

					// jitter+backoff the retry loop
					timer.Reset(time.Duration(iterCount)*consensusParamsResponseTimeout +
						time.Duration(100*rand.Int63n(iterCount))*time.Millisecond) //nolint:gosec

					select {
					case <-timer.C:
						continue
					case <-ctx.Done():
						return
					case params, ok := <-s.paramsRecvCh:
						if !ok {
							return
						}
						select {
						case <-ctx.Done():
							return
						case out <- params:
							return
						}
					}
				}

			}(peer)
		}
		sig := make(chan struct{})
		go func() { wg.Wait(); close(sig) }()
		return sig, nil
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	var iterCount int64
	for {
		iterCount++
		sig, err := retryAll()
		if err != nil {
			return types.ConsensusParams{}, err
		}
		select {
		case <-sig:
			// jitter+backoff the retry loop
			timer.Reset(time.Duration(iterCount)*consensusParamsResponseTimeout +
				time.Duration(100*rand.Int63n(iterCount))*time.Millisecond) //nolint:gosec
			select {
			case param := <-out:
				return param, nil
			case <-ctx.Done():
				return types.ConsensusParams{}, ctx.Err()
			case <-timer.C:
			}
		case <-ctx.Done():
			return types.ConsensusParams{}, ctx.Err()
		case param := <-out:
			return param, nil
		}
	}

}
