package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"
)

// TestRecoverAndLogHandlerHTTP2 verifies that the status-capturing writer no
// longer faults on transports that do not implement http.Hijacker (HTTP/2),
// while HTTP/1.1 requests keep working.
func TestRecoverAndLogHandlerHTTP2(t *testing.T) {
	logger := log.NewNopLogger()
	h := recoverAndLogHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}), logger)

	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	testCases := []struct {
		name        string
		forceHTTP2  bool
		method      string
		wantVersion int
	}{
		{name: "http1.1-get", forceHTTP2: false, method: http.MethodGet, wantVersion: 1},
		{name: "http2-get", forceHTTP2: true, method: http.MethodGet, wantVersion: 2},
		{name: "http2-post", forceHTTP2: true, method: http.MethodPost, wantVersion: 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &http.Transport{
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
				ForceAttemptHTTP2: tc.forceHTTP2,
			}
			if !tc.forceHTTP2 {
				// Disable HTTP/2 negotiation entirely.
				tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
			}
			c := &http.Client{Transport: tr}

			req, err := http.NewRequest(tc.method, srv.URL, nil)
			require.NoError(t, err)
			res, err := c.Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, tc.wantVersion, res.ProtoMajor)
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, "ok", string(body))
		})
	}
}

func TestOriginChecker(t *testing.T) {
	const host = "node.example.com:26657"

	testCases := []struct {
		name           string
		allowedOrigins []string
		origin         string
		want           bool
	}{
		{name: "no origin header", allowedOrigins: nil, origin: "", want: true},
		{name: "same host", allowedOrigins: nil, origin: "http://" + host, want: true},
		{name: "same host case-insensitive", allowedOrigins: nil, origin: "http://NODE.EXAMPLE.COM:26657", want: true},
		{name: "cross-origin not listed", allowedOrigins: nil, origin: "http://evil.com", want: false},
		{name: "cross-origin listed exact", allowedOrigins: []string{"http://dash.org"}, origin: "http://dash.org", want: true},
		{name: "cross-origin listed exact case-insensitive", allowedOrigins: []string{"http://Dash.org"}, origin: "http://dash.ORG", want: true},
		{name: "cross-origin not in list", allowedOrigins: []string{"http://dash.org"}, origin: "http://evil.com", want: false},
		{name: "star allows all", allowedOrigins: []string{"*"}, origin: "http://evil.com", want: true},
		{name: "wildcard subdomain match", allowedOrigins: []string{"http://*.dash.org"}, origin: "http://app.dash.org", want: true},
		{name: "wildcard subdomain match nested", allowedOrigins: []string{"http://*.dash.org"}, origin: "http://a.b.dash.org", want: true},
		{name: "wildcard subdomain deny apex", allowedOrigins: []string{"http://*.dash.org"}, origin: "http://dash.org", want: false},
		{name: "wildcard subdomain deny other", allowedOrigins: []string{"http://*.dash.org"}, origin: "http://app.evil.org", want: false},
		{name: "wildcard case-insensitive", allowedOrigins: []string{"http://*.Dash.org"}, origin: "http://APP.dash.org", want: true},
		{name: "invalid origin url denied", allowedOrigins: nil, origin: "://not a url", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			check := OriginChecker(tc.allowedOrigins)
			r := httptest.NewRequest(http.MethodGet, "http://"+host+"/websocket", nil)
			r.Host = host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			assert.Equal(t, tc.want, check(r))
		})
	}
}

// TestWebsocketOriginCheck drives OriginChecker through a real upgrade.
func TestWebsocketOriginCheck(t *testing.T) {
	logger := log.NewNopLogger()

	funcMap := map[string]*RPCFunc{
		"c": NewWSRPCFunc(func(context.Context) (string, error) { return "foo", nil }),
	}

	testCases := []struct {
		name           string
		allowedOrigins []string
		setOrigin      bool
		origin         string
		wantUpgrade    bool
	}{
		{name: "no origin upgraded", allowedOrigins: nil, setOrigin: false, wantUpgrade: true},
		{name: "cross-origin not listed rejected", allowedOrigins: nil, setOrigin: true, origin: "http://evil.com", wantUpgrade: false},
		{name: "cross-origin listed upgraded", allowedOrigins: []string{"http://dash.org"}, setOrigin: true, origin: "http://dash.org", wantUpgrade: true},
		{name: "wildcard origin upgraded", allowedOrigins: []string{"http://*.dash.org"}, setOrigin: true, origin: "http://app.dash.org", wantUpgrade: true},
		{name: "star configured upgraded", allowedOrigins: []string{"*"}, setOrigin: true, origin: "http://evil.com", wantUpgrade: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wm := NewWebsocketManager(logger, funcMap)
			wm.CheckOrigin = OriginChecker(tc.allowedOrigins)

			mux := http.NewServeMux()
			mux.HandleFunc("/websocket", wm.WebsocketHandler)
			srv := httptest.NewServer(mux)
			defer srv.Close()

			var header http.Header
			if tc.setOrigin {
				header = http.Header{"Origin": []string{tc.origin}}
			}

			d := websocket.Dialer{}
			c, resp, err := d.Dial("ws://"+srv.Listener.Addr().String()+"/websocket", header)
			if tc.wantUpgrade {
				require.NoError(t, err)
				require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
				require.NoError(t, c.Close())
			} else {
				require.Error(t, err)
				require.NotNil(t, resp)
				require.Equal(t, http.StatusForbidden, resp.StatusCode)
			}
			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}

// TestJSONRPCBatchLimit verifies the hardcoded per-batch request cap.
func TestJSONRPCBatchLimit(t *testing.T) {
	mux := testMux()

	makeBatch := func(n int, notification bool) string {
		entries := make([]string, n)
		for i := range entries {
			if notification {
				entries[i] = `{"jsonrpc":"2.0","method":"c","params":["a","10"]}`
			} else {
				entries[i] = fmt.Sprintf(`{"jsonrpc":"2.0","method":"c","id":%d,"params":["a","10"]}`, i)
			}
		}
		return "[" + strings.Join(entries, ",") + "]"
	}

	testCases := []struct {
		name            string
		payload         string
		wantBatch       bool   // response is a JSON array
		wantCount       int    // number of responses when wantBatch
		wantErr         bool   // single error response (CodeInvalidRequest)
		wantErrContains string // substring required in combined message+data; defaults to maxBatchRequests when empty and wantErr
		wantEmpty       bool   // no response body at all
	}{
		{name: "batch of 100 processed", payload: makeBatch(maxBatchRequests, false), wantBatch: true, wantCount: maxBatchRequests},
		{name: "batch of 101 rejected", payload: makeBatch(maxBatchRequests+1, false), wantErr: true},
		{name: "single request unaffected", payload: `{"jsonrpc":"2.0","method":"c","id":0,"params":["a","10"]}`, wantBatch: false, wantCount: 1},
		{name: "batch of 100 notifications no response", payload: makeBatch(maxBatchRequests, true), wantEmpty: true},
		{name: "empty batch rejected", payload: `[]`, wantErr: true, wantErrContains: "empty batch"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "http://localhost/", strings.NewReader(tc.payload))
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			res := rec.Result()
			defer res.Body.Close()

			blob, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			switch {
			case tc.wantEmpty:
				require.Empty(t, blob)
			case tc.wantErr:
				var resp rpctypes.RPCResponse
				require.NoError(t, json.Unmarshal(blob, &resp))
				require.NotNil(t, resp.Error)
				assert.Equal(t, int(rpctypes.CodeInvalidRequest), resp.Error.Code)
				wantContains := tc.wantErrContains
				if wantContains == "" {
					wantContains = fmt.Sprintf("%d", maxBatchRequests)
				}
				assert.Contains(t, resp.Error.Message+resp.Error.Data, wantContains)
			case tc.wantBatch:
				var resps []rpctypes.RPCResponse
				require.NoError(t, json.Unmarshal(blob, &resps))
				assert.Len(t, resps, tc.wantCount)
			default:
				var resp rpctypes.RPCResponse
				require.NoError(t, json.Unmarshal(blob, &resp))
				assert.Nil(t, resp.Error)
			}
		})
	}
}
