package dashtypes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatorAddress_String(t *testing.T) {

	tests := []struct {
		uri  string
		want string
	}{
		{
			uri:  "tcp://node@fqdn.address.com:1234",
			want: "tcp://node@fqdn.address.com:1234",
		},
		{
			uri:  "tcp://fqdn.address.com:1234",
			want: "tcp://fqdn.address.com:1234",
		},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			va, err := ParseValidatorAddress(tt.uri)
			assert.NoError(t, err)
			got := va.String()
			assert.EqualValues(t, tt.want, got)
		})
	}
}

// TestValidatorAddress_NodeID_fail checks if NodeID lookup fails when trying to connect to ssh port
// NOTE: Positive flow is tested as part of node_test.go TestNodeStartStop()
func TestValidatorAddress_NodeID_fail(t *testing.T) {

	tests := []struct {
		uri     string
		want    string
		wantErr bool
	}{
		{
			uri:  "tcp://node@fqdn.address.com:1234",
			want: "node",
		},
		{
			uri:     "tcp://127.0.0.1:22",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			va, err := ParseValidatorAddress(tt.uri)
			assert.NoError(t, err)
			got, err := va.NodeID()
			assert.Equal(t, err != nil, tt.wantErr, "wantErr=%s, but err = %s", tt.wantErr, err)
			assert.EqualValues(t, tt.want, got)
		})
	}
}
