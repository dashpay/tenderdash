package ed25519

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFromMnemonic(t *testing.T) {
	type testCase struct {
		mnemonic        string
		passphrase      string
		deriviationPath string
		pubkeyAddress   string
	}

	testCases := []testCase{
		{
			mnemonic:        "injury urban mention wide beach cereal industry rule capital picnic aerobic hotel cable okay curious",
			passphrase:      "zażółć",
			deriviationPath: "m/9'/5'/3'/4'/0'",
			pubkeyAddress:   "D34C6CF6BD79C7822C1E40950C279CE3A03126F1",
		},
		{
			mnemonic:        "injury urban mention wide beach cereal industry rule capital picnic aerobic hotel cable okay curious",
			passphrase:      "",
			deriviationPath: "m/9'/5'/3'/4'/0'",
			pubkeyAddress:   "7F9DC230E64396E96BE0061A0B6F36AF2F89ACA0",
		},
		{
			mnemonic:        "pave review wealth sign level crane stick sick remind club ordinary gauge despair jeans lava anxiety accuse ship buddy price globe",
			passphrase:      "",
			deriviationPath: "m/9'/5'/3'/4'/0'",
			pubkeyAddress:   "449E1D79E782DEB8AE7B77E72FBFB52561BD0AC5",
		},
		{
			mnemonic:        "pave review wealth sign level crane stick sick remind club ordinary gauge despair jeans lava anxiety accuse ship buddy price globe",
			passphrase:      "",
			deriviationPath: "m/9'/5'/3'/4'/1'",
			pubkeyAddress:   "29942C234C0871509840376056E27C778FEFDC71",
		},
	}
	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			pk, err := FromBip39Mnemonic(tc.mnemonic, tc.passphrase, tc.deriviationPath)
			assert.NoError(t, err)
			require.NotNil(t, pk)
			assert.Equal(t, tc.pubkeyAddress, pk.PubKey().Address().String())
		})
	}
}
