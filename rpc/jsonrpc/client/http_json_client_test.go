package client

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPClientMakeHTTPDialer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Hi!\n"))
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	tsTLS := httptest.NewTLSServer(handler)
	defer tsTLS.Close()
	// This silences a TLS handshake error, caused by the dialer just immediately
	// disconnecting, which we can just ignore.
	tsTLS.Config.ErrorLog = log.New(io.Discard, "", 0)

	for _, testURL := range []string{ts.URL, tsTLS.URL} {
		u, err := newParsedURL(testURL)
		require.NoError(t, err)
		dialFn := makeHTTPDialer(dialParamsFromURL(u))
		require.NoError(t, err)

		conn, err := dialFn(ctx, u.Scheme, u.GetHostWithPath())
		require.NoError(t, err)
		require.NotNil(t, conn)
	}
}

func Test_parsedURL(t *testing.T) {
	type test struct {
		url                  string
		expectedURL          string
		expectedHostWithPath string
		expectedDialAddress  string
	}

	tests := map[string]test{
		"unix endpoint": {
			url:                  "unix:///tmp/test",
			expectedURL:          "unix://.tmp.test",
			expectedHostWithPath: "/tmp/test",
			expectedDialAddress:  "/tmp/test",
		},

		"http endpoint": {
			url:                  "https://example.com",
			expectedURL:          "https://example.com",
			expectedHostWithPath: "example.com",
			expectedDialAddress:  "example.com",
		},

		"http endpoint with port": {
			url:                  "https://example.com:8080",
			expectedURL:          "https://example.com:8080",
			expectedHostWithPath: "example.com:8080",
			expectedDialAddress:  "example.com:8080",
		},

		"http path routed endpoint": {
			url:                  "https://example.com:8080/rpc",
			expectedURL:          "https://example.com:8080/rpc",
			expectedHostWithPath: "example.com:8080/rpc",
			expectedDialAddress:  "example.com:8080",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			parsed, err := newParsedURL(tt.url)
			require.NoError(t, err)
			require.Equal(t, tt.expectedDialAddress, parsed.GetDialAddress())
			require.Equal(t, tt.expectedURL, parsed.GetTrimmedURL())
			require.Equal(t, tt.expectedHostWithPath, parsed.GetHostWithPath())
		})
	}
}

func TestDialParamsFromURL(t *testing.T) {
	testCases := []struct {
		addr         string
		wantAddr     string
		wantProtocol string
	}{
		{
			addr:         "https://localhost",
			wantProtocol: "tcp",
			wantAddr:     "localhost:https",
		},
		{
			addr:         "http://localhost",
			wantProtocol: "tcp",
			wantAddr:     "localhost:http",
		},
		{
			addr:         "tcp://localhost",
			wantProtocol: "tcp",
			wantAddr:     "localhost:tcp",
		},
		{
			addr:         "ftp://localhost",
			wantProtocol: "ftp",
			wantAddr:     "localhost:ftp",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			pURL, err := newParsedURL(tc.addr)
			require.NoError(t, err)
			protocol, addr := dialParamsFromURL(pURL)
			require.Equal(t, tc.wantProtocol, protocol)
			require.Equal(t, tc.wantAddr, addr)
		})
	}
}
