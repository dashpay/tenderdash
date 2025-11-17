package types

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

type SampleResult struct {
	Value string
}

// Valid JSON identifier texts.
var testIDs = []string{
	`"1"`, `"alphabet"`, `""`, `"àáâ"`, "-1", "0", "1", "100",
}

func TestResponses(t *testing.T) {
	for _, id := range testIDs {
		req := RPCRequest{id: json.RawMessage(id)}

		a := req.MakeResponse(&SampleResult{"hello"})
		b, err := json.Marshal(a)
		require.NoError(t, err, "input id: %q", id)
		s := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{"Value":"hello"}}`, id)
		assert.Equal(t, s, string(b))

		d := req.MakeErrorf(CodeParseError, "hello world")
		e, err := json.Marshal(d)
		require.NoError(t, err)
		f := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"error":{"code":-32700,"message":"Parse error","data":"hello world"}}`, id)
		assert.Equal(t, f, string(e))

		g := req.MakeErrorf(CodeMethodNotFound, "foo")
		h, err := json.Marshal(g)
		require.NoError(t, err)
		i := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"error":{"code":-32601,"message":"Method not found","data":"foo"}}`, id)
		assert.Equal(t, string(h), i)
	}
}

func TestUnmarshallResponses(t *testing.T) {
	for _, id := range testIDs {
		response := &RPCResponse{}
		input := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{"Value":"hello"}}`, id)
		require.NoError(t, json.Unmarshal([]byte(input), &response))

		req := RPCRequest{id: json.RawMessage(id)}
		a := req.MakeResponse(&SampleResult{"hello"})
		assert.Equal(t, *response, a)
	}
	var response RPCResponse
	const input = `{"jsonrpc":"2.0","id":true,"result":{"Value":"hello"}}`
	require.Error(t, json.Unmarshal([]byte(input), &response))
}

func TestRPCError(t *testing.T) {
	assert.Equal(t, "RPC error 12 - Badness: One worse than a code 11",
		fmt.Sprintf("%v", &RPCError{
			Code:    12,
			Message: "Badness",
			Data:    "One worse than a code 11",
		}))

	assert.Equal(t, "RPC error 12 - Badness",
		fmt.Sprintf("%v", &RPCError{
			Code:    12,
			Message: "Badness",
		}))
}

func TestRPCRequestMakeErrorMapsCodes(t *testing.T) {
	req := RPCRequest{id: json.RawMessage(`"1"`)}
	testCases := []struct {
		name string
		err  error
		want ErrorCode
	}{
		{"invalid request", coretypes.ErrInvalidRequest, CodeInvalidRequest},
		{"tx already in cache", types.ErrTxInCache, CodeTxAlreadyExists},
		{"tx too large", types.ErrTxTooLarge{Max: 1, Actual: 2}, CodeTxTooLarge},
		{"mempool full", types.ErrMempoolIsFull{}, CodeMempoolIsFull},
		{"deadline exceeded", context.DeadlineExceeded, CodeTimeout},
		{"too many requests sentinel", coretypes.ErrTooManyRequests, CodeTooManyRequests},
		{"too many requests string", errors.New("too_many_requests: limiter triggered"), CodeTooManyRequests},
		{"broadcast confirmation", fmt.Errorf("%w", coretypes.ErrBroadcastConfirmationNotReceived), CodeBroadcastConfirmationNotReceived},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := req.MakeError(tc.err)
			require.NotNil(t, resp.Error)
			assert.Equal(t, int(tc.want), resp.Error.Code)
		})
	}
}
