package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestConfigValidateBasicIncludesP2P asserts the [p2p] section is actually
// reached by Config.ValidateBasic. Every rule in P2PConfig.ValidateBasic was
// unreachable while nothing called it, so a config violating them started
// normally.
func TestConfigValidateBasicIncludesP2P(t *testing.T) {
	testCases := []struct {
		name        string
		mutate      func(*P2PConfig)
		errContains string
	}{
		{
			name:        "NegativeFlushThrottleTimeout",
			mutate:      func(c *P2PConfig) { c.FlushThrottleTimeout = -1 * time.Second },
			errContains: "flush-throttle-timeout",
		},
		{
			name:        "NegativeMaxPacketMsgPayloadSize",
			mutate:      func(c *P2PConfig) { c.MaxPacketMsgPayloadSize = -1 },
			errContains: "max-packet-msg-payload-size",
		},
		{
			name:        "NegativeSendRate",
			mutate:      func(c *P2PConfig) { c.SendRate = -1 },
			errContains: "send-rate",
		},
		{
			name:        "NegativeRecvRate",
			mutate:      func(c *P2PConfig) { c.RecvRate = -1 },
			errContains: "recv-rate",
		},
		{
			name: "MoreOutgoingThanTotalConnections",
			mutate: func(c *P2PConfig) {
				c.MaxConnections = 4
				c.MaxOutgoingConnections = 5
			},
			errContains: "max-outgoing-connections",
		},
		{
			name:        "NegativeMaxIncomingConnectionTime",
			mutate:      func(c *P2PConfig) { c.MaxIncomingConnectionTime = -1 * time.Second },
			errContains: "max-incoming-connection-time",
		},
		{
			name:        "ZeroIncomingConnectionWindow",
			mutate:      func(c *P2PConfig) { c.IncomingConnectionWindow = 0 },
			errContains: "incoming-connection-window",
		},
		{
			name:        "SubMillisecondIncomingConnectionWindow",
			mutate:      func(c *P2PConfig) { c.IncomingConnectionWindow = 500 * time.Microsecond },
			errContains: "incoming-connection-window",
		},
		{
			name: "AllowlistOnlyWithoutPeers",
			mutate: func(c *P2PConfig) {
				c.AllowlistOnly = true
				c.PersistentPeers = ""
				c.BootstrapPeers = ""
			},
			errContains: "allowlist-only",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeFull
			tc.mutate(cfg.P2P)

			err := cfg.ValidateBasic()
			require.Error(t, err, "Config.ValidateBasic must reject this [p2p] value")
			require.Contains(t, err.Error(), "[p2p] section",
				"the error must tell the operator which section is at fault")
			require.Contains(t, err.Error(), tc.errContains,
				"the error must name the offending configuration key")
		})
	}

	t.Run("DefaultsAreValid", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Mode = ModeFull
		require.NoError(t, cfg.P2P.ValidateBasic(), "a default [p2p] section must remain valid")
	})
}
