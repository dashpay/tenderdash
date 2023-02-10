package ed25519

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFromMnemonic(t *testing.T) {
	type testCase struct {
		mnemonic        string
		passphrase      string
		deriviationPath string
		pubkeyHex       string
	}

	testCases := []testCase{
		{
			mnemonic:        "injury urban mention wide beach cereal industry rule capital picnic aerobic hotel cable okay curious",
			passphrase:      "",
			deriviationPath: "m/9'/5'/3'/4'/0'",
			pubkeyHex:       "98549849af5468eabf377e0c7eb493c00daa37b7ca6fa18989fb430ca83ff58d",
		},
		{
			mnemonic:        "injury urban mention wide beach cereal industry rule capital picnic aerobic hotel cable okay curious",
			passphrase:      "zazolc",
			deriviationPath: "m/9'/5'/3'/4'/0'",
			pubkeyHex:       "9041e8deaddde890e6041b28a9239aa73bdaf7743531646ede9f198e53b1bed6",
		},
		{
			mnemonic:        "pave review wealth sign level crane stick sick remind club ordinary gauge despair jeans lava anxiety accuse ship buddy price globe",
			passphrase:      "",
			deriviationPath: "m/9'/5'/3'/4'/0'",
			pubkeyHex:       "fc0399d47050e3eaf99e0023f987551438137d8d550b2e17f6bc8e48dece917d",
		},
		{
			mnemonic:        "pave review wealth sign level crane stick sick remind club ordinary gauge despair jeans lava anxiety accuse ship buddy price globe",
			passphrase:      "",
			deriviationPath: "m/9'/5'/3'/4'/1'",
			pubkeyHex:       "eb0c4ccd7b422fa880bb4cd37eb04bf7086dfa1991588f2d63840e32cac9c1b8",
		},
	}
	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			pk, err := FromBip39Mnemonic(tc.mnemonic, tc.passphrase, tc.deriviationPath)
			assert.NoError(t, err)
			require.NotNil(t, pk)
			assert.Equal(t, tc.pubkeyHex, pk.PubKey().HexString())
		})
	}
}
