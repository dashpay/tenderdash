package server

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

func TestOriginAllowlistChecker(t *testing.T) {
	tests := []struct {
		name    string
		origins []string
		origin  string
		allowed bool
	}{
		{name: "non-browser client", allowed: true},
		{name: "empty list", origin: "https://app.example"},
		{name: "same host requires configuration", origin: "http://proxy.example"},
		{name: "configured origin", origins: []string{"https://app.example"}, origin: "https://app.example", allowed: true},
		{name: "different origin", origins: []string{"https://app.example"}, origin: "https://other.example"},
		{name: "different scheme", origins: []string{"https://app.example"}, origin: "http://app.example"},
		{name: "configured pattern", origins: []string{"https://*.example"}, origin: "https://app.example", allowed: true},
		{name: "explicit allow all", origins: []string{"*"}, origin: "https://app.example", allowed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://proxy.example/websocket", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			require.Equal(t, tc.allowed, OriginAllowlistChecker(log.NewNopLogger(), tc.origins)(r))
		})
	}
}
