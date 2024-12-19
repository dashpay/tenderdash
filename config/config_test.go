package config

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	// set up some defaults
	cfg := DefaultConfig()
	assert.NotNil(t, cfg.P2P)
	assert.NotNil(t, cfg.Mempool)
	assert.NotNil(t, cfg.Consensus)

	// check the root dir stuff...
	cfg.SetRoot("/foo")
	cfg.Genesis = "bar"
	cfg.DBPath = "/opt/data"

	assert.Equal(t, "/foo/bar", cfg.GenesisFile())
	assert.Equal(t, "/opt/data", cfg.DBDir())

	assert.Equal(t, 60*time.Second, cfg.BaseConfig.SyncTimeout)
}

func TestConfigValidateBasic(t *testing.T) {
	cfg := DefaultConfig()
	assert.NoError(t, cfg.ValidateBasic())

	// tamper with unsafe-propose-timeout-override
	cfg.Consensus.UnsafeProposeTimeoutOverride = -10 * time.Second
	assert.Error(t, cfg.ValidateBasic())
}

func TestAbciConfigValidation(t *testing.T) {
	type testCase struct {
		name string
		*AbciConfig
		expectErr string // empty when no error, or error message to expect
	}
	// negative test cases that should fail on validator, but pass on seeds
	invalidCases := []testCase{
		{
			name:      "no abci config",
			expectErr: "",
		},
		{
			name:       "unexpected data",
			AbciConfig: &AbciConfig{Other: map[string]interface{}{"foo": "bar"}},
			expectErr:  "",
		},
		{
			name: "invalid transport",
			AbciConfig: &AbciConfig{
				Transport: "invalid",
				Address:   "tcp://127.0.0.1:1234",
			},
			expectErr: "",
		},
		{
			name: "missing address",
			AbciConfig: &AbciConfig{
				Transport: "invalid",
				Address:   "",
			},
			expectErr: "",
		},
	}

	for _, tc := range invalidCases {
		tc := tc

		t.Run(tc.name+" on validator", func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeValidator
			cfg.Abci = nil

			err := cfg.ValidateBasic()
			if tc.expectErr != "" {
				assert.ErrorContains(t, err, tc.expectErr)
			}
		})
		t.Run(tc.name+" on seed", func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeSeed
			cfg.Abci = nil

			err := cfg.ValidateBasic()
			assert.NoError(t, err)
		})
	}
}

func TestTLSConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetRoot("/home/user")

	cfg.RPC.TLSCertFile = "file.crt"
	assert.Equal(t, "/home/user/config/file.crt", cfg.RPC.CertFile())
	cfg.RPC.TLSKeyFile = "file.key"
	assert.Equal(t, "/home/user/config/file.key", cfg.RPC.KeyFile())

	cfg.RPC.TLSCertFile = "/abs/path/to/file.crt"
	assert.Equal(t, "/abs/path/to/file.crt", cfg.RPC.CertFile())
	cfg.RPC.TLSKeyFile = "/abs/path/to/file.key"
	assert.Equal(t, "/abs/path/to/file.key", cfg.RPC.KeyFile())
}

func TestBaseConfigValidateBasic(t *testing.T) {
	cfg := TestBaseConfig()
	assert.NoError(t, cfg.ValidateBasic())

	// tamper with log format
	cfg.LogFormat = "invalid"
	assert.Error(t, cfg.ValidateBasic())
}

func TestRPCConfigValidateBasic(t *testing.T) {
	cfg := TestRPCConfig()
	assert.NoError(t, cfg.ValidateBasic())

	fieldsToTest := []string{
		"MaxOpenConnections",
		"MaxSubscriptionClients",
		"MaxSubscriptionsPerClient",
		"TimeoutBroadcastTxCommit",
		"MaxBodyBytes",
		"MaxHeaderBytes",
	}

	for _, fieldName := range fieldsToTest {
		reflect.ValueOf(cfg).Elem().FieldByName(fieldName).SetInt(-1)
		assert.Error(t, cfg.ValidateBasic())
		reflect.ValueOf(cfg).Elem().FieldByName(fieldName).SetInt(0)
	}
}

func TestMempoolConfigValidateBasic(t *testing.T) {
	cfg := TestMempoolConfig()
	assert.NoError(t, cfg.ValidateBasic())

	fieldsToTest := []string{
		"Size",
		"MaxTxsBytes",
		"CacheSize",
		"MaxTxBytes",
	}

	for _, fieldName := range fieldsToTest {
		reflect.ValueOf(cfg).Elem().FieldByName(fieldName).SetInt(-1)
		assert.Error(t, cfg.ValidateBasic())
		reflect.ValueOf(cfg).Elem().FieldByName(fieldName).SetInt(0)
	}
}

func TestStateSyncConfigValidateBasic(t *testing.T) {
	cfg := TestStateSyncConfig()
	require.NoError(t, cfg.ValidateBasic())
}

func TestConsensusConfig_ValidateBasic(t *testing.T) {
	testcases := map[string]struct {
		modify    func(*ConsensusConfig)
		expectErr bool
	}{
		"UnsafeProposeTimeoutOverride":               {func(c *ConsensusConfig) { c.UnsafeProposeTimeoutOverride = time.Second }, false},
		"UnsafeProposeTimeoutOverride negative":      {func(c *ConsensusConfig) { c.UnsafeProposeTimeoutOverride = -1 }, true},
		"UnsafeProposeTimeoutDeltaOverride":          {func(c *ConsensusConfig) { c.UnsafeProposeTimeoutDeltaOverride = time.Second }, false},
		"UnsafeProposeTimeoutDeltaOverride negative": {func(c *ConsensusConfig) { c.UnsafeProposeTimeoutDeltaOverride = -1 }, true},
		"UnsafePrevoteTimeoutOverride":               {func(c *ConsensusConfig) { c.UnsafeVoteTimeoutOverride = time.Second }, false},
		"UnsafePrevoteTimeoutOverride negative":      {func(c *ConsensusConfig) { c.UnsafeVoteTimeoutOverride = -1 }, true},
		"UnsafePrevoteTimeoutDeltaOverride":          {func(c *ConsensusConfig) { c.UnsafeVoteTimeoutDeltaOverride = time.Second }, false},
		"UnsafePrevoteTimeoutDeltaOverride negative": {func(c *ConsensusConfig) { c.UnsafeVoteTimeoutDeltaOverride = -1 }, true},
		"UnsafeCommitTimeoutOverride":                {func(c *ConsensusConfig) { c.UnsafeCommitTimeoutOverride = time.Second }, false},
		"UnsafeCommitTimeoutOverride negative":       {func(c *ConsensusConfig) { c.UnsafeCommitTimeoutOverride = -1 }, true},
		"PeerGossipSleepDuration":                    {func(c *ConsensusConfig) { c.PeerGossipSleepDuration = time.Second }, false},
		"PeerGossipSleepDuration negative":           {func(c *ConsensusConfig) { c.PeerGossipSleepDuration = -1 }, true},
		"PeerQueryMaj23SleepDuration":                {func(c *ConsensusConfig) { c.PeerQueryMaj23SleepDuration = time.Second }, false},
		"PeerQueryMaj23SleepDuration negative":       {func(c *ConsensusConfig) { c.PeerQueryMaj23SleepDuration = -1 }, true},
		"DoubleSignCheckHeight negative":             {func(c *ConsensusConfig) { c.DoubleSignCheckHeight = -1 }, true},
	}
	for desc, tc := range testcases {
		tc := tc // appease linter
		t.Run(desc, func(t *testing.T) {
			cfg := DefaultConsensusConfig()
			tc.modify(cfg)

			err := cfg.ValidateBasic()
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInstrumentationConfigValidateBasic(t *testing.T) {
	cfg := TestInstrumentationConfig()
	assert.NoError(t, cfg.ValidateBasic())

	// tamper with maximum open connections
	cfg.MaxOpenConnections = -1
	assert.Error(t, cfg.ValidateBasic())
}

func TestP2PConfigValidateBasic(t *testing.T) {
	cfg := TestP2PConfig()
	assert.NoError(t, cfg.ValidateBasic())

	fieldsToTest := []string{
		"FlushThrottleTimeout",
		"MaxPacketMsgPayloadSize",
		"SendRate",
		"RecvRate",
	}

	for _, fieldName := range fieldsToTest {
		reflect.ValueOf(cfg).Elem().FieldByName(fieldName).SetInt(-1)
		assert.Error(t, cfg.ValidateBasic())
		reflect.ValueOf(cfg).Elem().FieldByName(fieldName).SetInt(0)
	}
}

// Given some invalid node key file, when I try to load it, I get an error
func TestLoadNodeKeyID(t *testing.T) {

	testCases := []string{
		`{
  "type": "tendermint/PrivKeyEd25519",
  "value": "wIVaBy3v4bKcrBxGsgFen9qJeqXiK4h18iWCM2LSYxMyH8PomXsANUb3KoucY9EBDj0NQi4LqrmG8DyT5D6xWQ=="
}`,
		`{
	"id":"0d846d89021b617026c3a3d4051ebcf4cdd09f7c",
	"priv_key":{
		"type":"tendermint/PrivKeyEd25519",
		"value":"J5EWnwSixAZuuw2Gf5nbXXNbyliaURFgBawfwN+zU/N7ucjnxu0GLcVi107XEj2Myq95101jcPPcJE+dCncY1A=="
	}
}`,
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			cfg := DefaultBaseConfig()
			tmpDir := t.TempDir()

			// create invalid node key file
			cfg.NodeKey = tmpDir + "/node_key.json"
			err := os.WriteFile(cfg.NodeKey, []byte(tc), 0600)
			require.NoError(t, err)

			// when I try to load the node key
			_, err = cfg.LoadNodeKeyID()

			// then I get an error
			assert.Error(t, err)
		})
	}
}
