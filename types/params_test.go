package types

import (
	"bytes"
	"crypto/sha256"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

var (
	valBLS12381 = []string{ABCIPubKeyTypeBLS12381}
)

func TestConsensusParamsValidation(t *testing.T) {
	testCases := []struct {
		name   string
		params ConsensusParams
		valid  bool
	}{
		// test block params
		{
			name: "block params valid",
			params: makeParams(makeParamsArgs{
				blockBytes:   1,
				evidenceAge:  2,
				precision:    1,
				messageDelay: 1}),
			valid: true,
		},
		{
			name: "block params invalid MaxBytes",
			params: makeParams(makeParamsArgs{
				blockBytes:   0,
				evidenceAge:  2,
				precision:    1,
				messageDelay: 1}),
			valid: false,
		},
		{
			name: "block params large MaxBytes",
			params: makeParams(makeParamsArgs{
				blockBytes:   47 * 1024 * 1024,
				evidenceAge:  2,
				precision:    1,
				messageDelay: 1}),
			valid: true,
		},
		{
			name: "block params small MaxBytes",
			params: makeParams(makeParamsArgs{
				blockBytes:   10,
				evidenceAge:  2,
				precision:    1,
				messageDelay: 1}),
			valid: true,
		},
		{
			name: "block params 100MB MaxBytes",
			params: makeParams(makeParamsArgs{
				blockBytes:   100 * 1024 * 1024,
				evidenceAge:  2,
				precision:    1,
				messageDelay: 1}),
			valid: true,
		},
		{
			name: "block params MaxBytes too large",
			params: makeParams(makeParamsArgs{
				blockBytes:   101 * 1024 * 1024,
				evidenceAge:  2,
				precision:    1,
				messageDelay: 1}),
			valid: false,
		},
		{
			name: "block params 1GB MaxBytes",
			params: makeParams(makeParamsArgs{
				blockBytes:   1024 * 1024 * 1024,
				evidenceAge:  2,
				precision:    1,
				messageDelay: 1}),
			valid: false,
		},
		// test evidence params
		{
			name: "evidence MaxAge and MaxBytes 0",
			params: makeParams(makeParamsArgs{
				blockBytes:       1,
				evidenceAge:      0,
				maxEvidenceBytes: 0,
				precision:        1,
				messageDelay:     1}),
			valid: false,
		},
		{
			name: "evidence MaxBytes greater than Block.MaxBytes",
			params: makeParams(makeParamsArgs{
				blockBytes:       1,
				evidenceAge:      2,
				maxEvidenceBytes: 2,
				precision:        1,
				messageDelay:     1}),
			valid: false,
		},
		{
			name: "evidence size below Block.MaxBytes",
			params: makeParams(makeParamsArgs{
				blockBytes:       1000,
				evidenceAge:      2,
				maxEvidenceBytes: 1,
				precision:        1,
				messageDelay:     1}),
			valid: true,
		},
		{
			name: "evidence MaxAgeDuration < 0",
			params: makeParams(makeParamsArgs{
				blockBytes:       1,
				evidenceAge:      -1,
				maxEvidenceBytes: 0,
				precision:        1,
				messageDelay:     1}),
			valid: false,
		},
		{
			name: "no pubkey types",
			params: makeParams(makeParamsArgs{
				evidenceAge:  2,
				pubkeyTypes:  []string{},
				precision:    1,
				messageDelay: 1}),
			valid: false,
		},
		{
			name: "invalid pubkey types",
			params: makeParams(makeParamsArgs{
				evidenceAge:  2,
				pubkeyTypes:  []string{"potatoes make good pubkeys"},
				precision:    1,
				messageDelay: 1}),
			valid: false,
		},
		{
			name: "negative MessageDelay",
			params: makeParams(makeParamsArgs{
				evidenceAge:  2,
				precision:    1,
				messageDelay: -1}),
			valid: false,
		},
		{
			name: "negative Precision",
			params: makeParams(makeParamsArgs{
				evidenceAge:  2,
				precision:    -1,
				messageDelay: 1}),
			valid: false,
		},
	}
	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.valid {
				assert.NoErrorf(t, tc.params.ValidateConsensusParams(), "expected no error for valid params (#%d)", i)
			} else {
				assert.Errorf(t, tc.params.ValidateConsensusParams(), "expected error for non valid params (#%d)", i)
			}
		})
	}
}

type makeParamsArgs struct {
	blockBytes       int64
	blockGas         int64
	recheck          bool
	evidenceAge      int64
	maxEvidenceBytes int64
	pubkeyTypes      []string
	precision        time.Duration
	messageDelay     time.Duration

	propose      *time.Duration
	proposeDelta *time.Duration
	vote         *time.Duration
	voteDelta    *time.Duration

	consensusVersion int32
}

func makeParams(args makeParamsArgs) ConsensusParams {
	if args.pubkeyTypes == nil {
		args.pubkeyTypes = valBLS12381
	}
	if args.propose == nil {
		args.propose = durationPtr(1)
	}
	if args.proposeDelta == nil {
		args.proposeDelta = durationPtr(1)
	}
	if args.vote == nil {
		args.vote = durationPtr(1)
	}
	if args.voteDelta == nil {
		args.voteDelta = durationPtr(1)
	}

	cp := ConsensusParams{
		Block: BlockParams{
			MaxBytes: args.blockBytes,
			MaxGas:   args.blockGas,
		},
		Evidence: EvidenceParams{
			MaxAgeNumBlocks: args.evidenceAge,
			MaxAgeDuration:  time.Duration(args.evidenceAge),
			MaxBytes:        args.maxEvidenceBytes,
		},
		Validator: ValidatorParams{
			PubKeyTypes:          args.pubkeyTypes,
			VotingPowerThreshold: nil,
		},
		Synchrony: SynchronyParams{
			Precision:    args.precision,
			MessageDelay: args.messageDelay,
		},
		Timeout: TimeoutParams{
			Propose:      *args.propose,
			ProposeDelta: *args.proposeDelta,
			Vote:         *args.vote,
			VoteDelta:    *args.voteDelta,
		},
		ABCI: ABCIParams{
			RecheckTx: args.recheck,
		},
	}

	if args.consensusVersion != 0 {
		cp.Version = VersionParams{
			AppVersion:       1,
			ConsensusVersion: args.consensusVersion,
		}
	}

	return cp
}

func TestConsensusParamsHash(t *testing.T) {
	params := []ConsensusParams{
		makeParams(makeParamsArgs{blockBytes: 4, blockGas: 2, evidenceAge: 3, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 1, blockGas: 4, evidenceAge: 3, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 1, blockGas: 2, evidenceAge: 4, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 2, blockGas: 5, evidenceAge: 7, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 1, blockGas: 7, evidenceAge: 6, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 9, blockGas: 5, evidenceAge: 4, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 7, blockGas: 8, evidenceAge: 9, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 4, blockGas: 6, evidenceAge: 5, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 4, blockGas: 6, evidenceAge: 5, maxEvidenceBytes: 1, consensusVersion: 1}),
	}

	hashes := make([][]byte, len(params))
	for i := range params {
		hashes[i] = params[i].HashConsensusParams()
	}

	// make sure there are no duplicates...
	// sort, then check in order for matches
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i], hashes[j]) < 0
	})
	for i := 0; i < len(hashes)-1; i++ {
		assert.NotEqual(t, hashes[i], hashes[i+1])
	}
}

func TestConsensusParamsUpdate(t *testing.T) {
	testCases := []struct {
		initialParams ConsensusParams
		updates       *tmproto.ConsensusParams
		updatedParams ConsensusParams
	}{
		// empty updates
		{
			initialParams: makeParams(makeParamsArgs{blockBytes: 1, blockGas: 2, evidenceAge: 3}),
			updates:       &tmproto.ConsensusParams{},
			updatedParams: makeParams(makeParamsArgs{blockBytes: 1, blockGas: 2, evidenceAge: 3}),
		},
		{
			// update synchrony params
			initialParams: makeParams(makeParamsArgs{evidenceAge: 3, precision: time.Second, messageDelay: 3 * time.Second}),
			updates: &tmproto.ConsensusParams{
				Synchrony: &tmproto.SynchronyParams{
					Precision:    durationPtr(time.Second * 2),
					MessageDelay: durationPtr(time.Second * 4),
				},
			},
			updatedParams: makeParams(makeParamsArgs{evidenceAge: 3, precision: 2 * time.Second, messageDelay: 4 * time.Second}),
		},
		{
			// update timeout params
			initialParams: makeParams(makeParamsArgs{
				propose:      durationPtr(3 * time.Second),
				proposeDelta: durationPtr(500 * time.Millisecond),
				vote:         durationPtr(time.Second),
				voteDelta:    durationPtr(500 * time.Millisecond),
			}),
			updates: &tmproto.ConsensusParams{
				Timeout: &tmproto.TimeoutParams{
					Propose:      durationPtr(2 * time.Second),
					ProposeDelta: durationPtr(400 * time.Millisecond),
					Vote:         durationPtr(5 * time.Second),
					VoteDelta:    durationPtr(400 * time.Millisecond),
				},
			},
			updatedParams: makeParams(makeParamsArgs{
				propose:      durationPtr(2 * time.Second),
				proposeDelta: durationPtr(400 * time.Millisecond),
				vote:         durationPtr(5 * time.Second),
				voteDelta:    durationPtr(400 * time.Millisecond),
			}),
		},
		// fine updates
		{
			initialParams: makeParams(makeParamsArgs{blockBytes: 1, blockGas: 2, evidenceAge: 3}),
			updates: &tmproto.ConsensusParams{
				Block: &tmproto.BlockParams{
					MaxBytes: 100,
					MaxGas:   200,
				},
				Evidence: &tmproto.EvidenceParams{
					MaxAgeNumBlocks: 300,
					MaxAgeDuration:  time.Duration(300),
					MaxBytes:        50,
				},
				Validator: &tmproto.ValidatorParams{
					PubKeyTypes: valBLS12381,
				},
			},
			updatedParams: makeParams(makeParamsArgs{
				blockBytes: 100, blockGas: 200,
				evidenceAge:      300,
				maxEvidenceBytes: 50,
			}),
		},
		{
			initialParams: makeParams(makeParamsArgs{blockBytes: 1, blockGas: 2, evidenceAge: 3}),
			updates: &tmproto.ConsensusParams{
				Block: &tmproto.BlockParams{
					MaxBytes: 100,
					MaxGas:   200,
				},
				Evidence: &tmproto.EvidenceParams{
					MaxAgeNumBlocks: 300,
					MaxAgeDuration:  time.Duration(300),
					MaxBytes:        50,
				},
				Validator: &tmproto.ValidatorParams{
					PubKeyTypes: valBLS12381,
				},
			},
			updatedParams: makeParams(makeParamsArgs{
				blockBytes:       100,
				blockGas:         200,
				evidenceAge:      300,
				maxEvidenceBytes: 50,
			}),
		}, {
			initialParams: makeParams(makeParamsArgs{blockBytes: 100, blockGas: 200, evidenceAge: 300}),
			updates: &tmproto.ConsensusParams{
				Version: &tmproto.VersionParams{
					AppVersion:       1,
					ConsensusVersion: 1,
				},
			},
			updatedParams: makeParams(makeParamsArgs{
				blockBytes:       100,
				blockGas:         200,
				evidenceAge:      300,
				consensusVersion: 1,
			}),
		},
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.updatedParams, tc.initialParams.UpdateConsensusParams(tc.updates))
	}
}

func TestConsensusParamsUpdate_Version(t *testing.T) {
	params := makeParams(makeParamsArgs{blockBytes: 1, blockGas: 2, evidenceAge: 3})

	assert.EqualValues(t, 0, params.Version.AppVersion)

	updated := params.UpdateConsensusParams(
		&tmproto.ConsensusParams{Version: &tmproto.VersionParams{AppVersion: 1, ConsensusVersion: 1}})

	assert.EqualValues(t, 1, updated.Version.AppVersion)
	assert.EqualValues(t, 1, updated.Version.ConsensusVersion)
}

func TestProto(t *testing.T) {
	params := []ConsensusParams{
		makeParams(makeParamsArgs{blockBytes: 4, blockGas: 2, evidenceAge: 3, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 1, blockGas: 4, evidenceAge: 3, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 1, blockGas: 2, evidenceAge: 4, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 2, blockGas: 5, evidenceAge: 7, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 1, blockGas: 7, evidenceAge: 6, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 9, blockGas: 5, evidenceAge: 4, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 7, blockGas: 8, evidenceAge: 9, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{blockBytes: 4, blockGas: 6, evidenceAge: 5, maxEvidenceBytes: 1}),
		makeParams(makeParamsArgs{precision: time.Second, messageDelay: time.Minute}),
		makeParams(makeParamsArgs{precision: time.Nanosecond, messageDelay: time.Millisecond}),
		makeParams(makeParamsArgs{
			propose:      durationPtr(2 * time.Second),
			proposeDelta: durationPtr(400 * time.Millisecond),
			vote:         durationPtr(5 * time.Second),
			voteDelta:    durationPtr(400 * time.Millisecond),
		}),
	}

	for i := range params {
		pbParams := params[i].ToProto()

		oriParams := ConsensusParamsFromProto(pbParams)

		assert.Equal(t, params[i], oriParams)

	}
}

func TestFromProto(t *testing.T) {
	params := []tmproto.ConsensusParams{
		{},
	}

	for i := range params {
		pbParams := params[i]
		assert.NotPanics(t, func() { ConsensusParamsFromProto(pbParams) })
	}
}

func durationPtr(t time.Duration) *time.Duration {
	return &t
}

func TestProposerLivenessTimeouts(t *testing.T) {
	t.Run("should derive whichever of the pair a chain leaves unset", func(t *testing.T) {
		testCases := map[string]struct {
			heartbeat, silence         time.Duration
			wantHeartbeat, wantSilence time.Duration
		}{
			"neither set":            {0, 0, time.Second, 5 * time.Second},
			"only a long heartbeat":  {10 * time.Second, 0, 10 * time.Second, 50 * time.Second},
			"only a short heartbeat": {100 * time.Millisecond, 0, 100 * time.Millisecond, 5 * time.Second},
			"only a short silence":   {0, 2 * time.Second, 400 * time.Millisecond, 2 * time.Second},
			"only a long silence":    {0, 30 * time.Second, time.Second, 30 * time.Second},
			"both set":               {2 * time.Second, 3 * time.Second, 2 * time.Second, 3 * time.Second},
		}
		for name, tc := range testCases {
			t.Run(name, func(t *testing.T) {
				tp := TimeoutParams{ProposerHeartbeat: tc.heartbeat, ProposerSilence: tc.silence}.TimeoutParamsOrDefaults()
				assert.Equal(t, tc.wantHeartbeat, tp.ProposerHeartbeat)
				assert.Equal(t, tc.wantSilence, tp.ProposerSilence)
			})
		}
	})

	t.Run("should keep a valid pair when an update names only one of them", func(t *testing.T) {
		base := *DefaultConsensusParams()
		require.NoError(t, base.ValidateConsensusParams())

		heartbeat := 10 * time.Second
		updated := base.UpdateConsensusParams(&tmproto.ConsensusParams{
			Timeout: &tmproto.TimeoutParams{ProposerHeartbeat: &heartbeat},
		})
		require.NoError(t, updated.ValidateConsensusParams())
		assert.Equal(t, heartbeat, updated.Timeout.ProposerHeartbeat)
		assert.Zero(t, updated.Timeout.ProposerSilence, "an update leaves the other field as it was")
		assert.Equal(t, 50*time.Second, updated.Timeout.TimeoutParamsOrDefaults().ProposerSilence)

		silence := 2 * time.Second
		updated = base.UpdateConsensusParams(&tmproto.ConsensusParams{
			Timeout: &tmproto.TimeoutParams{ProposerSilence: &silence},
		})
		require.NoError(t, updated.ValidateConsensusParams())
		assert.Equal(t, silence, updated.Timeout.ProposerSilence)
	})

	t.Run("should refuse a pair that cannot work", func(t *testing.T) {
		testCases := map[string]TimeoutParams{
			"negative heartbeat":              {ProposerHeartbeat: -time.Second},
			"negative silence":                {ProposerSilence: -time.Second},
			"silence no longer than the beat": {ProposerHeartbeat: 2 * time.Second, ProposerSilence: 2 * time.Second},
			"silence too short to beat in":    {ProposerSilence: 3 * time.Nanosecond},
		}
		for name, timeouts := range testCases {
			t.Run(name, func(t *testing.T) {
				params := *DefaultConsensusParams()
				params.Timeout.ProposerHeartbeat = timeouts.ProposerHeartbeat
				params.Timeout.ProposerSilence = timeouts.ProposerSilence
				assert.Error(t, params.ValidateConsensusParams())
			})
		}
	})

	t.Run("should leave both out of the encoding while unset", func(t *testing.T) {
		params := *DefaultConsensusParams()
		pb := params.ToProto()
		assert.Nil(t, pb.Timeout.ProposerHeartbeat)
		assert.Nil(t, pb.Timeout.ProposerSilence)
		assert.Equal(t, params, ConsensusParamsFromProto(pb))
	})

	t.Run("should survive a proto round trip once set", func(t *testing.T) {
		params := *DefaultConsensusParams()
		params.Timeout.ProposerHeartbeat = 2 * time.Second
		params.Timeout.ProposerSilence = 9 * time.Second
		decoded := ConsensusParamsFromProto(params.ToProto())
		assert.Equal(t, 2*time.Second, decoded.Timeout.ProposerHeartbeat)
		assert.Equal(t, 9*time.Second, decoded.Timeout.ProposerSilence)
	})

	t.Run("should grow the silence by one heartbeat a round", func(t *testing.T) {
		tp := TimeoutParams{ProposerHeartbeat: time.Second, ProposerSilence: 5 * time.Second}
		assert.Equal(t, 5*time.Second, tp.ProposerSilenceTimeout(0))
		assert.Equal(t, 8*time.Second, tp.ProposerSilenceTimeout(3))
	})

	t.Run("should derive the longest silence rather than wrap from a very long heartbeat", func(t *testing.T) {
		params := *DefaultConsensusParams()
		params.Timeout.ProposerHeartbeat = 5_000_000_000_000_000_000
		require.NoError(t, params.ValidateConsensusParams())

		tp := params.Timeout.TimeoutParamsOrDefaults()
		assert.Equal(t, time.Duration(math.MaxInt64), tp.ProposerSilence)
		assert.Equal(t, time.Duration(math.MaxInt64), tp.ProposerSilenceTimeout(3))
	})

	t.Run("should stop growing the silence at the longest duration rather than wrap", func(t *testing.T) {
		params := *DefaultConsensusParams()
		params.Timeout.ProposerHeartbeat = 5_000_000_000_000_000_000
		params.Timeout.ProposerSilence = 6_000_000_000_000_000_000
		require.NoError(t, params.ValidateConsensusParams(), "validation bounds neither from above")

		tp := params.Timeout.TimeoutParamsOrDefaults()
		assert.Equal(t, params.Timeout.ProposerSilence, tp.ProposerSilenceTimeout(0))
		for _, round := range []int32{1, 2, math.MaxInt32} {
			assert.Equal(t, time.Duration(math.MaxInt64), tp.ProposerSilenceTimeout(round), "round %d", round)
		}
	})
}

func TestConsensusParamsHashProposerLivenessTimeouts(t *testing.T) {
	withVersion := func(version tmproto.VersionParams_ConsensusVersion, heartbeat, silence time.Duration) ConsensusParams {
		params := *DefaultConsensusParams()
		params.Version.ConsensusVersion = int32(version)
		params.Timeout.ProposerHeartbeat = heartbeat
		params.Timeout.ProposerSilence = silence
		return params
	}

	t.Run("should hash params before CONSENSUS_VERSION_2 exactly as before", func(t *testing.T) {
		for _, version := range []tmproto.VersionParams_ConsensusVersion{
			tmproto.VersionParams_CONSENSUS_VERSION_0, tmproto.VersionParams_CONSENSUS_VERSION_1,
		} {
			params := withVersion(version, 3*time.Second, 20*time.Second)
			legacy, err := (&tmproto.HashedParams{
				BlockMaxBytes:    params.Block.MaxBytes,
				BlockMaxGas:      params.Block.MaxGas,
				ConsensusVersion: params.Version.ConsensusVersion,
			}).Marshal()
			require.NoError(t, err)
			sum := sha256.Sum256(legacy)
			assert.Equal(t, sum[:], []byte(params.HashConsensusParams()), "version %v", version)
		}
	})

	t.Run("should cover the timeouts from CONSENSUS_VERSION_2", func(t *testing.T) {
		v2 := tmproto.VersionParams_CONSENSUS_VERSION_2
		base := withVersion(v2, 2*time.Second, 10*time.Second).HashConsensusParams()
		assert.NotEqual(t, base, withVersion(v2, 3*time.Second, 10*time.Second).HashConsensusParams())
		assert.NotEqual(t, base, withVersion(v2, 2*time.Second, 11*time.Second).HashConsensusParams())
	})

	t.Run("should hash unset timeouts as the defaults they stand for", func(t *testing.T) {
		v2 := tmproto.VersionParams_CONSENSUS_VERSION_2
		assert.Equal(t,
			withVersion(v2, 0, 0).HashConsensusParams(),
			withVersion(v2, time.Second, 5*time.Second).HashConsensusParams())
		assert.Equal(t,
			withVersion(v2, 10*time.Second, 0).HashConsensusParams(),
			withVersion(v2, 10*time.Second, 50*time.Second).HashConsensusParams())
	})
}
