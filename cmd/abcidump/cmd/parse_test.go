package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/internal/libs/protoio"
)

func init() {
}

func TestParse(t *testing.T) {
	testCases := []struct {
		input      proto.Message
		format     string
		args       []string
		conditions []string
	}{
		{
			input: &types.Request{
				Value: &types.Request_ListSnapshots{
					ListSnapshots: &types.RequestListSnapshots{},
				}},
			format:     formatHex,
			conditions: []string{"\"listSnapshots\": {"},
		},
		{
			input: &types.Request{
				Value: &types.Request_BeginBlock{
					BeginBlock: &types.RequestBeginBlock{
						Hash: []byte("block start"),
					},
				}},
			format: formatBase64,
			conditions: []string{
				"beginBlock",
				"\"partSetHeader\"",
			},
		},
		{
			input: &types.Request{
				Value: &types.Request_BeginBlock{
					BeginBlock: &types.RequestBeginBlock{
						Hash: []byte("block start"),
					},
				}},
			format: formatHex,
			conditions: []string{
				"beginBlock",
				"\"partSetHeader\"",
			},
		},
	}
	for _, raw := range []string{"raw", "delim"} {
		for i, tc := range testCases {

			t.Run(strconv.Itoa(i)+"_"+raw, func(t *testing.T) {
				// rootCmd := &ParseCmd{}
				rootCmd := MakeRootCmd()

				parsecmd := &ParseCmd{}
				rootCmd.AddCommand(parsecmd.Command())
				cmd := rootCmd

				args := []string{"parse", "--format", tc.format}

				var (
					inputBytes []byte
					err        error
				)
				if raw == "raw" {
					inputBytes, err = proto.Marshal(tc.input)
					args = append(args, "--raw")
				} else {
					inputBytes, err = protoio.MarshalDelimited(tc.input)
				}
				require.NoError(t, err)

				var input string
				switch tc.format {
				case formatBase64:
					input = base64.StdEncoding.EncodeToString(inputBytes)
				case formatHex:
					input = hex.EncodeToString(inputBytes)
				default:
					t.Errorf("unsupported format: %s", tc.format)
				}
				args = append(args, []string{"--input", input}...)

				t.Log(input)
				require.NotZero(t, input)

				args = append(args, tc.args...)
				cmd.SetArgs(args)

				outBuf := &bytes.Buffer{}
				cmd.SetOut(outBuf)

				errBuf := &bytes.Buffer{}
				cmd.SetErr(errBuf)

				err = cmd.Execute()
				assert.NoError(t, err)
				assert.Equal(t, 0, errBuf.Len(), errBuf.String())

				s := outBuf.String()

				for _, condition := range tc.conditions {
					assert.Contains(t, s, condition)
				}

				t.Log(s)
			})
		}
	}
}
