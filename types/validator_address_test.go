package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewValidatorAddress(t *testing.T) {
	tests := []struct {
		uri     string
		wantErr bool
	}{
		{"35402a825b10d3dde01dd94969e533c863ebbd05@10.186.73.13:26656", false},
		{"", false},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got, err := NewValidatorAddress(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewValidatorAddress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.uri != "" {
				assert.NotZero(t, got)
			} else {
				assert.Zero(t, got)
			}
		})
	}
}

func TestValidatorAddress_Validate(t *testing.T) {
	tests := []struct {
		uri     string
		wantErr bool
	}{
		{"35402a825b10d3dde01dd94969e533c863ebbd05@10.186.73.13:26656", false},
		{"abcd@257.186.73.13:26656", false},
		{"", true},
	}
	//nolint:scopelint
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			a, err := NewValidatorAddress(tt.uri)
			require.NoError(t, err)
			if err := a.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("ValidatorAddress.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
