package main

import (
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"

	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
)

// The application decodes finalize_hold into a time.Duration; the runner must
// emit a form that round-trips, and nothing at all when the hold is disabled.
func TestMakeAppConfigFinalizeHold(t *testing.T) {
	testnet := &e2e.Testnet{Name: "test", ABCIProtocol: e2e.ProtocolBuiltin}
	for _, hold := range []time.Duration{0, 65 * time.Second} {
		node := &e2e.Node{Testnet: testnet, Mode: e2e.ModeFull, FinalizeHold: hold}
		data, err := MakeAppConfig(node)
		require.NoError(t, err)
		var cfg struct {
			FinalizeHold *time.Duration `toml:"finalize_hold"`
		}
		_, err = toml.Decode(string(data), &cfg)
		require.NoError(t, err)
		if hold == 0 {
			require.Nil(t, cfg.FinalizeHold)
			continue
		}
		require.NotNil(t, cfg.FinalizeHold)
		require.Equal(t, hold, *cfg.FinalizeHold)
	}
}
