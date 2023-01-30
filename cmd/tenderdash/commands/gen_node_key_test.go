package commands

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

func TestGenNodeKeyFromPem(t *testing.T) {
	type testCase struct {
		pem                 []byte
		nodeID              types.NodeID
		expectErrorContains string
	}

	testCases := []testCase{
		{
			pem: []byte(`-----BEGIN PRIVATE KEY-----
MC4CAQAwBQYDK2VwBCIEIEWq4ajgZXt89uyYXZhFfvpMGbZVNoL7K+CZSc0X4A9e
-----END PRIVATE KEY-----`),
			nodeID: "e4667657b949e5f9f6362d19d653c4b7bdb078b7",
		},
		{
			pem: []byte(`-----BEGIN PRIVATE KEY-----
MC4CAQAwBQYDK2VwBCIEINP0WhqYyl63gJlPGaMImeuO1H6IlzF2CmTMuCT6QudH
-----END PRIVATE KEY-----`),
			nodeID: "47b681b98266b93cff9436c43c0318a6ed0db916",
		},
		{
			pem: []byte(`-----BEGIN PRIVATE KEY-----
NC4CAQAwBQYDK2VwBCIEINP0WhqYyl63gJlPGaMImeuO1H6IlzF2CmTMuCT6QudH
-----END PRIVATE KEY-----`),
			expectErrorContains: "cannot parse DER",
		},
		{
			pem:                 []byte{},
			expectErrorContains: "cannot PEM-decode input file",
		},
	}
	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			args := []string{"gen-node-key", "--" + flagFromPem, "-"}
			testGenNodeKey(t, tc.pem, tc.nodeID, tc.expectErrorContains, args)
		})
	}
}

func TestGenNodeKeyFromMnemonic(t *testing.T) {
	type testCase struct {
		mnemonic            string
		passphrase          string
		derivationPath      string
		nodeID              types.NodeID
		expectErrorContains string
	}

	testCases := []testCase{
		{
			mnemonic: "deposit mother mistake way oxygen bomb turn kangaroo loop radar minor isolate hill talent hello",
			nodeID:   "f3e4e41d3e4fe8c3c060d61ce86ea8ae665a4f78",
		},
		{
			mnemonic: "quarter ostrich pottery public magic neglect mansion trumpet remain airport race over",
			nodeID:   "748fcfab13b642e0a7c7333909035874574e2b49",
		},
		{
			mnemonic:       "quarter ostrich pottery public magic neglect mansion trumpet remain airport race over",
			derivationPath: "m/9'/5'/3'/4'",
			nodeID:         "3d14bde07355e2974c4940d4af421eeadccd3c36",
		},
		{
			mnemonic:   "quarter ostrich pottery public magic neglect mansion trumpet remain airport race over",
			passphrase: "everypasswordmatters",
			nodeID:     "293e5337f4be336cb939de0d096f5f7c110803ce",
		},
		{
			mnemonic: "safe fire saddle primary able rude estate title polar essence luggage never vacuum tank " +
				"trap frame orange dinosaur large monkey ball flash aerobic harbor",
			nodeID: "0a2b322cf65f4735414e6a919ff6c54d436db779",
		},
		{
			mnemonic:            "safe fire saddle",
			expectErrorContains: "mnemonic must have at least 12 words",
		},
	}
	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			input := []byte(tc.mnemonic + "\r\n" + tc.passphrase + "\r\n")
			args := []string{"gen-node-key", "--" + flagFromMnemonic}
			if tc.derivationPath != "" {
				args = append(args, "--"+flagDerivationPath, tc.derivationPath)
			}
			testGenNodeKey(t, input, tc.nodeID, tc.expectErrorContains, args)
		})
	}
}

func testGenNodeKey(t *testing.T, input []byte, expectedNodeID types.NodeID, expectErrorContains string, args []string) {
	conf := config.DefaultConfig()
	logger := log.NewTestingLogger(t)
	stdin := bytes.NewBuffer(input)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	cmd := MakeGenNodeKeyCommand(conf, logger)
	cmd.SilenceUsage = true

	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	err := cmd.Execute()
	
	if expectErrorContains == "" {
		assert.NoError(t, err)
		assert.Empty(t, stderr)
		assert.Contains(t, stdout.String(), `"id":"`+expectedNodeID+`"`)
	} else {
		assert.ErrorContains(t, err, expectErrorContains)
		assert.Contains(t, stderr.String(), expectErrorContains)
	}
}
