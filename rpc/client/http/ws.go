package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/libs/log"
	rpcclient "github.com/dashpay/tenderdash/rpc/client"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	jsonrpcclient "github.com/dashpay/tenderdash/rpc/jsonrpc/client"
)

// wsEvents is a wrapper around WSClient, which implements SubscriptionClient.
type wsEvents struct {
	Logger log.Logger
	ws     *jsonrpcclient.WSClient

	// changes orders upstream (un)subscribes: a subscriber is recorded, and an
	// unused query confirmed, while holding it across the send. Taken before mtx.
	changes chan struct{}

	mtx sync.RWMutex
	// subscriptions maps each query, and its upstream subscription ID once known,
	// to the local subscribers sharing its single upstream subscription.
	subscriptions map[string]*wsSubscription

	// lifetime bounds resubscribing and event delivery. It ends with the context
	// passed to Start, or in Stop if the client shut down by itself first.
	lifetime     context.Context
	endLifetime  context.CancelFunc
	listenerDone chan struct{}
	// resubscribeDelay gives Tendermint time to restart after an RPC error.
	resubscribeDelay time.Duration
}

var errNotRunning = errors.New("websocket client is not running")

type wsSubscription struct {
	id          string
	query       string
	subscribers map[string]*wsSubscriber
}

type wsSubscriber struct {
	res chan coretypes.ResultEvent
	// gone closes on unsubscribing, releasing a delivery its owner no longer reads.
	gone chan struct{}
}

var _ rpcclient.SubscriptionClient = (*wsEvents)(nil)

func newWsEvents(remote string) (*wsEvents, error) {
	w := &wsEvents{
		Logger:        log.NewNopLogger(),
		subscriptions: make(map[string]*wsSubscription),
		changes:       make(chan struct{}, 1),

		resubscribeDelay: time.Second,
	}

	var err error
	w.ws, err = jsonrpcclient.NewWS(strings.TrimSuffix(remote, "/"), "/websocket")
	if err != nil {
		return nil, fmt.Errorf("can't create WS client: %w", err)
	}
	w.ws.OnReconnect(func() {
		// resubscribe immediately
		w.redoSubscriptionsAfter(w.lifetime, 0)
	})
	w.ws.Logger = w.Logger

	return w, nil
}

// Start starts the websocket client and the event loop.
func (w *wsEvents) Start(ctx context.Context) error {
	w.lifetime, w.endLifetime = context.WithCancel(ctx)
	if err := w.ws.Start(ctx); err != nil {
		w.endLifetime()
		return err
	}
	w.listenerDone = make(chan struct{})
	go w.eventListener(w.lifetime, w.listenerDone)
	return nil
}

// Stop waits for the websocket client, which joins reconnect callbacks, to shut
// down with the context passed to Start, then joins the event loop.
func (w *wsEvents) Stop() error {
	err := w.ws.Stop()
	w.stopListener()
	return err
}

// IsRunning reports whether the client has started and not yet shut down.
func (w *wsEvents) IsRunning() bool {
	if w.listenerDone == nil {
		return false
	}
	select {
	case <-w.listenerDone:
		return false
	default:
		return true
	}
}

// stopListener ends the event loop, even if blocked on a subscriber not reading.
func (w *wsEvents) stopListener() {
	if w.listenerDone != nil {
		w.endLifetime()
		<-w.listenerDone
	}
}

// Subscribe implements SubscriptionClient by using WSClient to subscribe given
// subscriber to query. By default, it returns a channel with cap=1. Error is
// returned if it fails to subscribe.
//
// When reading from the channel, keep in mind there's a single events loop, so
// if you don't read events for this subscription fast enough, other
// subscriptions will slow down in effect.
//
// The channel is never closed to prevent clients from seeing an erroneous
// event.
//
// Subscribers share one upstream subscription per query, which Tendermint keys
// by remote IP; subscriber only identifies the owner locally.
//
// It returns an error if wsEvents is not running.
func (w *wsEvents) Subscribe(ctx context.Context, subscriber, query string,
	outCapacity ...int) (out <-chan coretypes.ResultEvent, err error) {
	if err := w.lockChanges(ctx); err != nil {
		return nil, err
	}
	defer w.unlockChanges()
	if err := w.ws.Subscribe(ctx, query); err != nil {
		return nil, err
	}

	outCap := 1
	if len(outCapacity) > 0 {
		outCap = outCapacity[0]
	}

	outc := make(chan coretypes.ResultEvent, outCap)
	w.mtx.Lock()
	defer w.mtx.Unlock()
	sub, ok := w.subscriptions[query]
	if !ok {
		sub = &wsSubscription{query: query, subscribers: make(map[string]*wsSubscriber)}
		w.subscriptions[query] = sub
	}
	if old, ok := sub.subscribers[subscriber]; ok {
		close(old.gone)
	}
	sub.subscribers[subscriber] = &wsSubscriber{res: outc, gone: make(chan struct{})}

	return outc, nil
}

// Unsubscribe implements SubscriptionClient by unsubscribing given subscriber
// from query, and using WSClient to unsubscribe from the upstream once no
// subscriber is left.
//
// It returns an error if wsEvents is not running. The subscription is forgotten
// even then; see UnsubscribeAll.
func (w *wsEvents) Unsubscribe(ctx context.Context, subscriber, query string) error {
	w.mtx.Lock()
	var unused []string
	if sub, ok := w.subscriptions[query]; ok && sub.query == query {
		unused = w.removeSubscriber(sub, subscriber, unused)
	}
	w.mtx.Unlock()

	return w.unsubscribeUpstream(ctx, unused)
}

// UnsubscribeAll implements SubscriptionClient by unsubscribing given
// subscriber from all the queries, and using WSClient to unsubscribe from the
// upstream the queries no other subscriber uses.
//
// It returns an error if wsEvents is not running. Subscriptions are forgotten
// even then: a reconnect must not restore one whose channel nobody reads, which
// would stall every other subscription on the single events loop.
func (w *wsEvents) UnsubscribeAll(ctx context.Context, subscriber string) error {
	w.mtx.Lock()
	var unused []string
	for key, sub := range w.subscriptions {
		if key == sub.query {
			unused = w.removeSubscriber(sub, subscriber, unused)
		}
	}
	w.mtx.Unlock()

	return w.unsubscribeUpstream(ctx, unused)
}

// lockChanges waits for the changes lock as long as ctx allows: its holder's send
// can block until the writer restarts.
func (w *wsEvents) lockChanges(ctx context.Context) error {
	select {
	case w.changes <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *wsEvents) unlockChanges() { <-w.changes }

// removeSubscriber forgets subscriber's subscription to sub, and sub itself once
// unused, which it then appends to unused. Callers must hold mtx.
func (w *wsEvents) removeSubscriber(sub *wsSubscription, subscriber string, unused []string) []string {
	s, ok := sub.subscribers[subscriber]
	if !ok {
		return unused
	}
	close(s.gone)
	delete(sub.subscribers, subscriber)
	if len(sub.subscribers) > 0 {
		return unused
	}
	delete(w.subscriptions, sub.query)
	if sub.id != "" {
		delete(w.subscriptions, sub.id)
	}
	return append(unused, sub.query)
}

// unsubscribeUpstream unsubscribes from queries still unused once it holds the
// changes lock. Callers must not hold mtx: sending can block until the writer
// restarts. After an error, eventListener drops events for queries left subscribed.
func (w *wsEvents) unsubscribeUpstream(ctx context.Context, queries []string) error {
	if len(queries) == 0 {
		if !w.IsRunning() {
			return errNotRunning
		}
		return nil
	}
	if err := w.lockChanges(ctx); err != nil {
		return err
	}
	defer w.unlockChanges()
	for _, query := range queries {
		// Lock, not RLock: eventListener writes to the map under RLock.
		w.mtx.Lock()
		_, used := w.subscriptions[query]
		w.mtx.Unlock()
		if used {
			continue
		}
		if err := w.ws.Unsubscribe(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

// After being reconnected, it is necessary to redo subscription to server
// otherwise no data will be automatically received.
func (w *wsEvents) redoSubscriptionsAfter(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}

	// Sending can block until the writer restarts, so no lock is held across it.
	// eventListener writes to the map under RLock, so reading it here needs Lock.
	w.mtx.Lock()
	queries := make([]string, 0, len(w.subscriptions))
	for q, info := range w.subscriptions {
		if q != "" && q == info.id {
			continue
		}
		queries = append(queries, q)
	}
	w.mtx.Unlock()

	for _, q := range queries {
		w.mtx.Lock()
		_, ok := w.subscriptions[q]
		w.mtx.Unlock()
		if !ok {
			continue
		}
		// If q is forgotten during this send, it stays subscribed upstream until
		// the next reconnect. That is harmless: eventListener drops events for
		// unknown queries, and subscribing to q again gets ErrAlreadySubscribed,
		// which it ignores.
		if err := w.ws.Subscribe(ctx, q); err != nil {
			w.Logger.Error("failed to resubscribe", "query", q, "err", err)
			w.mtx.Lock()
			delete(w.subscriptions, q)
			w.mtx.Unlock()
		}
	}
}

func isErrAlreadySubscribed(err error) bool {
	return strings.Contains(err.Error(), pubsub.ErrAlreadySubscribed.Error())
}

func (w *wsEvents) eventListener(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case resp, ok := <-w.ws.ResponsesCh:
			if !ok {
				return
			}

			if resp.Error != nil {
				w.Logger.Error("WS error", "err", resp.Error.Error())
				// Error can be ErrAlreadySubscribed or max client (subscriptions per
				// client) reached or Tendermint exited.
				// We can ignore ErrAlreadySubscribed, but need to retry in other
				// cases.
				if !isErrAlreadySubscribed(resp.Error) {
					// Resubscribe after a delay to give Tendermint time to restart (if
					// crashed).
					w.redoSubscriptionsAfter(ctx, w.resubscribeDelay)
				}
				continue
			}

			result := new(coretypes.ResultEvent)
			err := json.Unmarshal(resp.Result, result)
			if err != nil {
				w.Logger.Error("failed to unmarshal response", "err", err)
				continue
			}

			w.mtx.RLock()
			var subscribers []*wsSubscriber
			if out, ok := w.subscriptions[result.Query]; ok {
				if _, idOk := w.subscriptions[result.SubscriptionID]; !idOk {
					out.id = result.SubscriptionID
					w.subscriptions[result.SubscriptionID] = out
				}
				for _, s := range out.subscribers {
					subscribers = append(subscribers, s)
				}
			}
			w.mtx.RUnlock()
			for _, s := range subscribers {
				select {
				case s.res <- *result:
				case <-s.gone:
				case <-ctx.Done():
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
