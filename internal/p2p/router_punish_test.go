package p2p

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/libs/log"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

// punishFixture builds a Router with a real PeerManager holding one connected
// peer, which is the minimum needed to observe both eviction and scoring.
type punishFixture struct {
	router *Router
	pm     *PeerManager
	peerID types.NodeID
}

func newPunishFixture(ctx context.Context, t *testing.T, maxConnected uint16) *punishFixture {
	t.Helper()

	selfKey := ed25519.GenPrivKeyFromSecret([]byte{0x01, 0x02, 0x03, 0x04})
	pm, err := NewPeerManager(ctx, types.NodeIDFromPubKey(selfKey.PubKey()), dbm.NewMemDB(),
		PeerManagerOptions{MaxConnected: maxConnected})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pm.Close()) })

	peerID := types.NodeID(strings.Repeat("a1", 20))
	added, err := pm.Add(NodeAddress{NodeID: peerID, Protocol: "memory"})
	require.NoError(t, err)
	require.True(t, added)
	require.NoError(t, pm.Accepted(peerID))

	return &punishFixture{
		router: &Router{
			logger:        log.NewNopLogger(),
			peerManager:   pm,
			metrics:       NopMetrics(),
			lc:            newMetricsLabelCache(),
			chDescs:       map[ChannelID]*ChannelDescriptor{},
			channelQueues: map[ChannelID]queue{},
		},
		pm:     pm,
		peerID: peerID,
	}
}

// isEvicted reports whether the peer is scheduled for eviction. It consumes the
// schedule, so call it at most once per peer per test.
func (f *punishFixture) isEvicted() bool {
	id, err := f.pm.TryEvictNext()
	return err == nil && id == f.peerID
}

func (f *punishFixture) score() PeerScore { return f.pm.Scores()[f.peerID] }

// TestReportPeerError_InvalidData_EvictsAndScores asserts that provably invalid
// data costs the sender both its connection and a durable score penalty.
// Eviction alone is not enough: PeerManager.Errored carries a FIXME noting the
// peer manager immediately tries to reconnect, so a peer that is only evicted
// pays nothing lasting.
func TestReportPeerError_InvalidData_EvictsAndScores(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	before := f.score()

	f.router.reportPeerError(ctx, PeerError{
		NodeID: f.peerID,
		Err:    fmt.Errorf("wrapped validation: %w", ErrInvalidData),
	})

	require.Less(t, f.score(), before, "score must drop for a peer that sent invalid data")
	require.True(t, f.isEvicted(), "peer that sent invalid data must be evicted")
}

// TestReportPeerError_Ambiguous_ScoresWithoutEviction pins the other half of the
// contract: an error that is not proof of bad bytes must not sever the
// connection. Without this, tightening the fatal set would silently start
// evicting honest peers on timeouts and our own send failures.
func TestReportPeerError_Ambiguous_ScoresWithoutEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	before := f.score()

	f.router.reportPeerError(ctx, PeerError{
		NodeID: f.peerID,
		Err:    errors.New("timed out"),
	})

	require.Less(t, f.score(), before, "an ambiguous error still scores the peer down")
	require.False(t, f.isEvicted(), "an ambiguous error must not evict below capacity")
}

// TestReportPeerError_AtCapacity_EvictsAndScores covers the case where the
// router escalates to eviction because every peer slot is in use. The peer is
// still the one that erred, so the penalty must be recorded as well —
// otherwise a node at capacity forgets every misbehavior it ever saw.
func TestReportPeerError_AtCapacity_EvictsAndScores(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newPunishFixture(ctx, t, 1)
	require.Len(t, f.pm.connected, 1, "fixture must be at capacity for this test to mean anything")
	before := f.score()

	f.router.reportPeerError(ctx, PeerError{
		NodeID: f.peerID,
		Err:    errors.New("timed out"),
	})

	require.Less(t, f.score(), before, "at capacity the score penalty must still be recorded")
	require.True(t, f.isEvicted(), "at capacity the peer is evicted")
}

// fakeConn feeds a fixed script of payloads to receivePeer on channel 1 and then
// reports EOF. Embedding Connection supplies the methods receivePeer never
// calls; any unexpected call panics rather than passing silently.
type fakeConn struct {
	Connection
	payloads [][]byte
	pos      int
}

func (c *fakeConn) ReceiveMessage(context.Context) (ChannelID, []byte, error) {
	if c.pos >= len(c.payloads) {
		return 0, nil, io.EOF
	}
	bz := c.payloads[c.pos]
	c.pos++
	return 1, bz, nil
}

func (c *fakeConn) String() string { return "fakeConn" }

// registerChannel makes channel 1 known to the router, so a test exercises the
// decode paths rather than the unknown-channel path.
func (f *punishFixture) registerChannel(ctx context.Context, t *testing.T) {
	t.Helper()
	f.router.chDescs[1] = &ChannelDescriptor{ID: 1, Name: "test"}
	f.router.channelQueues[1] = newSimplePriorityQueue(ctx, 10)
}

// TestReceivePeer_MalformedBytes_EvictsPeer is the headline case: a peer sends
// bytes that are not valid protobuf at all. Before this change receivePeer
// logged and continued, so the sender paid nothing and could repeat forever.
func TestReceivePeer_MalformedBytes_EvictsPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	f.registerChannel(ctx, t)
	before := f.score()

	// A lone continuation byte run is not a decodable protobuf field tag.
	conn := &fakeConn{payloads: [][]byte{{0xff, 0xff, 0xff, 0xff}, {}}}
	require.ErrorIs(t, f.router.receivePeer(ctx, f.peerID, conn, 1), ErrInvalidData)
	require.Equal(t, 1, conn.pos, "invalid data must stop the receive loop")

	require.Less(t, f.score(), before, "malformed bytes must cost the sender score")
	require.True(t, f.isEvicted(), "malformed bytes must evict the sender")
}

// Unknown payloads lower ordinary-peer scores without forcing eviction below capacity.
func TestReceivePeer_UndecodableEnvelope_ScoresWithoutEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	f.registerChannel(ctx, t)
	before := f.score()

	// Zero bytes decode into an empty Envelope, whose Unwrap finds no payload.
	conn := &fakeConn{payloads: [][]byte{{}}}
	_ = f.router.receivePeer(ctx, f.peerID, conn, 1)

	require.Less(t, f.score(), before, "an undecodable envelope must cost the sender score")
	require.False(t, f.isEvicted(), "an undecodable envelope may be version skew, so do not evict")
}

// TestReceivePeer_UnknownChannel_IsNotPunished pins a deliberate carve-out. A
// peer on a newer build may send on a channel this node never registered, and
// NodeInfo.CompatibleWith admits it as long as one channel overlaps. Punishing
// it would sever connectivity across a version boundary during a rollout, so
// this path stays silent by design. If this test starts failing, someone has
// "tidied up" a gap that is load-bearing for upgrades.
func TestReceivePeer_UnknownChannel_IsNotPunished(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	before := f.score()

	// Channel 1 is deliberately not registered.
	conn := &fakeConn{payloads: [][]byte{{0xff, 0xff, 0xff, 0xff}}}
	_ = f.router.receivePeer(ctx, f.peerID, conn, 1)

	require.Equal(t, before, f.score(), "unknown channel must not change the peer's score")
	require.False(t, f.isEvicted(), "unknown channel must not evict: it is how version skew looks")
}

func TestReportPeerError_ReservedPeerAtCapacity(t *testing.T) {
	for _, tc := range []struct {
		name           string
		err            error
		fatal, evicted bool
	}{
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "invalid data", err: ErrInvalidData, evicted: true},
		{name: "fatal", err: errors.New("fatal"), fatal: true, evicted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newPunishFixture(ctx, t, 2)
			require.NoError(t, f.pm.Accepted(types.NodeID(strings.Repeat("b2", 20))))
			require.Len(t, f.pm.connected, 2)
			require.NoError(t, f.pm.SetProtectedPeers([]types.NodeID{f.peerID}))
			before := f.score()
			f.router.reportPeerError(ctx, PeerError{NodeID: f.peerID, Err: tc.err, Fatal: tc.fatal})
			require.Equal(t, tc.evicted, f.isEvicted(), "reserved slots survive pressure, but not invalid data")
			require.Equal(t, before, f.score(), "reserved peers retain their scoring exemption")
		})
	}
}

func TestReceivePeer_ExcessiveAttributes_EvictsAndScores(t *testing.T) {
	for _, name := range []string{"count", "bytes"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newPunishFixture(ctx, t, 10)
			f.registerChannel(ctx, t)
			before := f.score()
			attributes := map[string]string{}
			if name == "count" {
				for i := 0; i <= maxEnvelopeAttributes; i++ {
					attributes[fmt.Sprint(i)] = ""
				}
			} else {
				attributes["key"] = strings.Repeat("x", maxEnvelopeAttributeBytes)
			}
			envelope := p2pproto.Envelope{Attributes: attributes}
			bz, err := envelope.Marshal()
			require.NoError(t, err)
			conn := &fakeConn{payloads: [][]byte{bz}}
			require.ErrorIs(t, f.router.receivePeer(ctx, f.peerID, conn, 1), ErrInvalidData)
			require.Less(t, f.score(), before)
			require.True(t, f.isEvicted())
		})
	}
}

func TestReceivePeer_UnknownPayloadDoesNotFloodErrorLog(t *testing.T) {
	for _, capacity := range []uint16{1, 10} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newPunishFixture(ctx, t, capacity)
			f.registerChannel(ctx, t)
			var output bytes.Buffer
			logger, err := log.NewLogger("info", &output)
			require.NoError(t, err)
			f.router.logger = logger
			f.pm.SetLogger(logger)
			conn := &fakeConn{payloads: [][]byte{{}, {}, {}}}
			require.ErrorIs(t, f.router.receivePeer(ctx, f.peerID, conn, 1), io.EOF)
			require.Empty(t, output.String(), "peer-controlled unknown payloads must not flood production logs")
			require.Equal(t, capacity == 1, f.isEvicted())
		})
	}
}

// routePeer must preserve input classification through connection teardown.
func TestRoutePeer_InputLoggingAndInternalFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		payloads   [][]byte
		receiveErr error
		wantError  bool
	}{
		{name: "malformed", payloads: [][]byte{{0xff, 0xff, 0xff, 0xff}}},
		{name: "internal", receiveErr: errors.New("internal transport failure"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newPunishFixture(ctx, t, 1)
			f.registerChannel(ctx, t)
			var output bytes.Buffer
			logger, err := log.NewLogger("info", &output)
			require.NoError(t, err)
			f.router.logger = logger
			f.pm.SetLogger(logger)
			f.router.peerQueues = map[types.NodeID]queue{}
			f.router.peerChannels = map[types.NodeID]ChannelIDSet{}
			f.router.queueFactory = newFIFOQueue
			conn := &routeLoggingConn{fakeConn: fakeConn{payloads: tc.payloads}, receiveErr: tc.receiveErr}
			f.router.routePeer(ctx, f.peerID, conn, ChannelIDSet{1: {}})
			require.Equal(t, tc.wantError, strings.Contains(output.String(), `"level":"error"`), output.String())
			if tc.wantError {
				require.Contains(t, output.String(), "internal transport failure")
			} else {
				require.Equal(t, 1, conn.pos)
			}
		})
	}
}

type routeLoggingConn struct {
	fakeConn
	receiveErr error
}

func (c *routeLoggingConn) ReceiveMessage(ctx context.Context) (ChannelID, []byte, error) {
	if c.receiveErr != nil {
		return 0, nil, c.receiveErr
	}
	return c.fakeConn.ReceiveMessage(ctx)
}

func (*routeLoggingConn) Close() error { return nil }

func TestPeerManagerErrored_InternalFailureStaysVisible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newPunishFixture(ctx, t, 1)
	var output bytes.Buffer
	logger, err := log.NewLogger("info", &output)
	require.NoError(t, err)
	f.pm.SetLogger(logger)
	f.pm.Errored(f.peerID, errors.New("internal peer manager failure"))
	require.Contains(t, output.String(), `"level":"error"`)
	require.Contains(t, output.String(), "internal peer manager failure")
	require.True(t, f.isEvicted())
}

// countingDB counts writes so tests can bound per-frame persistence cost.
type countingDB struct {
	dbm.DB
	sets atomic.Int64
}

func (d *countingDB) Set(key, value []byte) error {
	d.sets.Add(1)
	return d.DB.Set(key, value)
}

func (d *countingDB) SetSync(key, value []byte) error {
	d.sets.Add(1)
	return d.DB.SetSync(key, value)
}

// An unknown-payload burst must not cost one database write per frame:
// MutableScore is ephemeral, so only the in-memory score may change.
func TestReceivePeer_UnknownPayloadBurstDoesNotWriteDatabase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newPunishFixture(ctx, t, 10)
	f.registerChannel(ctx, t)
	db := &countingDB{DB: dbm.NewMemDB()}
	f.pm.store.db = db
	before := f.score()

	const frames = 100
	conn := &fakeConn{payloads: make([][]byte, frames)}
	require.ErrorIs(t, f.router.receivePeer(ctx, f.peerID, conn, 1), io.EOF)

	require.EqualValues(t, before-frames, f.score(), "every unknown payload must still cost score")
	require.Zero(t, db.sets.Load(), "ephemeral score changes must not be persisted")
	require.False(t, f.isEvicted())
}
