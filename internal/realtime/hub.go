// Package realtime wraps github.com/tmaxmax/go-sse with a bounded, never
// blocking fan-out for the live quiz streams (spec §6.3): a slow client must
// not be able to stall the hub's dispatch loop.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/tmaxmax/go-sse"
)

const (
	// TopicPrefix namespaces every quiz topic: quiz:<id>, quiz:<id>:teacher.
	TopicPrefix = "quiz:"

	heartbeatEvery = 15 * time.Second
	writeDeadline  = 30 * time.Second

	// QueueSize bounds the per-client delivery queue; exceeding it cancels
	// the subscription instead of blocking the hub (tested directly).
	QueueSize = 64
)

// Event is the JSON envelope broadcast on every stream: {"type":..,"data":..}.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Hub is the process-wide pub-sub hub (one per process).
type Hub struct {
	provider *sse.Joe

	heartbeatNanos atomic.Int64
	wake           chan struct{}
	closed         chan struct{}
	closeOnce      sync.Once

	topicsMu sync.Mutex
	active   map[string]int
}

// New builds the hub and starts its heartbeat goroutine.
func New() (*Hub, error) {
	h := &Hub{
		provider: &sse.Joe{Replayer: greetingReplayer{}},
		wake:     make(chan struct{}, 1),
		closed:   make(chan struct{}),
		active:   make(map[string]int),
	}
	h.heartbeatNanos.Store(int64(heartbeatEvery))
	go h.heartbeatLoop()
	return h, nil
}

// Close stops the heartbeat and shuts the provider down: every open
// subscription returns and the corresponding SSE connections close.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		close(h.closed)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.provider.Shutdown(ctx)
	})
}

// SetHeartbeatEvery overrides the ping interval (tests set a short one) and
// restarts the current wait so the new interval applies immediately.
func (h *Hub) SetHeartbeatEvery(d time.Duration) {
	h.heartbeatNanos.Store(int64(d))
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Publish broadcasts one event to every subscriber of topic. It never blocks
// on a slow client: deliveries go through guardedWriter's bounded queue.
func (h *Hub) Publish(topic string, ev Event) error {
	m, err := buildMessage(ev)
	if err != nil {
		return err
	}
	return h.provider.Publish(m, []string{topic})
}

// buildMessage turns an Event into a typed SSE message ({"type":..,"data":..}).
func buildMessage(ev Event) (*sse.Message, error) {
	t, err := sse.NewType(ev.Type)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	m := &sse.Message{Type: t}
	m.AppendData(string(b))
	return m, nil
}

// greetingReplayer hands each new subscriber its greeting at SUBSCRIPTION
// TIME. Joe runs Replay inside its serialized dispatch loop, before the
// subscriber joins the fan-out map, so:
//   - the greeting is guaranteed to be this connection's FIRST frame;
//   - events published while the snapshot is being built (its DB reads)
//     wait in the provider's queue and are delivered immediately behind the
//     greeting — never dropped on a (re)connect.
//
// The dispatch loop waits for the build — bounded by one snapshot's DB
// reads, only at (re)connect — while client delivery itself stays
// non-blocking through guardedWriter (a slow client still cannot stall it).
type greetingReplayer struct{}

// Put passes every message through untouched: there is no replay buffer —
// the DB-built snapshot is the replay.
func (greetingReplayer) Put(m *sse.Message, _ []string) (*sse.Message, error) { return m, nil }

// Replay builds THIS connection's greeting and queues it to its writer. A
// failure must not kill the stream (the inline greeting it replaced skipped
// on error the same way), and a panic must not escape: Joe would disable
// its replayer process-wide, silently dropping every future snapshot.
func (greetingReplayer) Replay(sub sse.Subscription) (err error) {
	defer func() {
		if recover() != nil {
			err = nil // one bad greeting must not disable the rest
		}
	}()
	gw, ok := sub.Client.(*guardedWriter)
	if !ok || gw.greeting == nil {
		return nil
	}
	ev := gw.greeting()
	if ev == nil {
		return nil
	}
	m, err := buildMessage(*ev)
	if err != nil {
		return nil
	}
	return gw.Send(m)
}

// Option customises one stream handler.
type Option func(*handlerCfg)

type handlerCfg struct {
	greeting func() *Event
	onEnter  func()
	onLeave  func(lastSent time.Time)
}

// WithGreeting makes the handler send one event to THIS connection right
// after the upgrade (the reconnecting client's snapshot, spec §6.3) — it
// never fans out to other subscribers. The snapshot is built at subscription
// time (greetingReplayer): it is always the connection's first frame, and
// events published while it builds queue behind it instead of being lost.
func WithGreeting(fn func() *Event) Option {
	return func(cfg *handlerCfg) { cfg.greeting = fn }
}

// WithLifecycle reports connection boundaries to the caller: onEnter runs
// after the topic join (before the greeting is built), onLeave when the
// handler exits, carrying the time of the last successful write to this
// client (its last received heartbeat — the disconnect freeze uses it).
func WithLifecycle(onEnter func(), onLeave func(lastSent time.Time)) Option {
	return func(cfg *handlerCfg) { cfg.onEnter, cfg.onLeave = onEnter, onLeave }
}

// Handler upgrades the request to SSE and subscribes it to topic. auth runs
// before the upgrade and its error (a 401 envelope) is returned as-is.
func (h *Hub) Handler(topic string, auth func(*echo.Context) error, opts ...Option) echo.HandlerFunc {
	var cfg handlerCfg
	for _, o := range opts {
		o(&cfg)
	}
	return func(c *echo.Context) error {
		if auth != nil {
			if err := auth(c); err != nil {
				return err
			}
		}
		sess, err := sse.Upgrade(c.Response(), c.Request())
		if err != nil {
			return err // nothing written yet
		}
		rc := http.NewResponseController(c.Response())
		_ = rc.SetWriteDeadline(time.Now().Add(writeDeadline))

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		// Kill a write stuck on a dead client as soon as the subscription ends.
		abortDone := make(chan struct{})
		go func() {
			defer close(abortDone)
			<-ctx.Done()
			_ = rc.SetWriteDeadline(time.Unix(1, 0)) // a past deadline fails any pending Write
		}()

		h.enterTopic(topic)
		defer h.leaveTopic(topic)
		if cfg.onEnter != nil {
			cfg.onEnter()
		}

		gw := &guardedWriter{ch: make(chan *sse.Message, QueueSize), cancel: cancel}
		defer func() {
			if cfg.onLeave != nil {
				cfg.onLeave(gw.lastSentTime())
			}
		}()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			pumpMessages(ctx, rc, sess, gw)
		}()

		// Commit 200 + text/event-stream through echo first: go-sse's flush
		// would otherwise commit the underlying writer behind echo's back
		// (making every later Write log a superfluous WriteHeader), and the
		// pump must never get a frame before the headers exist.
		c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
		c.Response().WriteHeader(http.StatusOK)
		_ = sess.Flush()

		// Per-connection greeting (never fanned out): built inside Joe's
		// dispatch loop the moment THIS subscription registers — see
		// greetingReplayer. It must no longer be built here, before
		// Subscribe: an event published during the snapshot's DB reads
		// (e.g. a `cheat` committed mid-build) would find no subscriber in
		// the fan-out map and be dropped for this connection, leaving the
		// reconnecting monitor stale until that participant's next event.
		// onEnter has already run, so the snapshot still observes any
		// resume the lifecycle just wrote.
		gw.greeting = cfg.greeting

		_ = h.provider.Subscribe(ctx, sse.Subscription{Client: gw, Topics: []string{topic}})
		cancel()
		wg.Wait()
		<-abortDone
		_ = rc.SetWriteDeadline(time.Time{}) // hand the connection back deadline-free
		return nil
	}
}

// guardedWriter queues messages for the drain goroutine; when the queue is
// full, Send cancels the subscription instead of blocking the hub. It also
// remembers the time of the last message actually written to the socket —
// the connection's last received heartbeat.
type guardedWriter struct {
	ch       chan *sse.Message
	cancel   context.CancelFunc
	greeting func() *Event // this connection's snapshot builder; run by greetingReplayer at subscribe time
	lastSent atomic.Int64  // unix nanos of the last successful socket write
}

var errClientTooSlow = errors.New("realtime: client too slow")

func (g *guardedWriter) Send(m *sse.Message) error {
	select {
	case g.ch <- m:
		return nil
	default:
		g.cancel()
		return errClientTooSlow
	}
}

func (g *guardedWriter) Flush() error { return nil }

// lastSentTime is the last write instant; connections that never sent
// anything fall back to now (they have no heartbeat yet).
func (g *guardedWriter) lastSentTime() time.Time {
	if n := g.lastSent.Load(); n > 0 {
		return time.Unix(0, n)
	}
	return time.Now()
}

// pumpMessages drains the queue to the socket, refreshing the write deadline
// before each message; a write error cancels the subscription.
func pumpMessages(ctx context.Context, rc *http.ResponseController, sess *sse.Session, gw *guardedWriter) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-gw.ch:
			_ = rc.SetWriteDeadline(time.Now().Add(writeDeadline))
			if err := sess.Send(m); err != nil {
				gw.cancel()
				return
			}
			if err := sess.Flush(); err != nil {
				gw.cancel()
				return
			}
			gw.lastSent.Store(time.Now().UnixNano())
		}
	}
}

// heartbeatLoop pings every active topic on the configured interval; the
// interval is re-read each round so tests can shorten it after New().
func (h *Hub) heartbeatLoop() {
	for {
		wait := time.Duration(h.heartbeatNanos.Load())
		if wait <= 0 {
			wait = heartbeatEvery
		}
		timer := time.NewTimer(wait)
		select {
		case <-h.closed:
			timer.Stop()
			return
		case <-h.wake:
			timer.Stop()
			continue // interval changed — restart the wait
		case <-timer.C:
			h.pingActive()
		}
	}
}

func (h *Hub) pingActive() {
	h.topicsMu.Lock()
	topics := make([]string, 0, len(h.active))
	for t, n := range h.active {
		if n > 0 {
			topics = append(topics, t)
		}
	}
	h.topicsMu.Unlock()
	for _, t := range topics {
		// server_now lets clients skew-correct their countdowns on heartbeat
		// (the plan's "re-synced on every heartbeat").
		_ = h.Publish(t, Event{Type: "ping", Data: map[string]any{"server_now": time.Now().Unix()}})
	}
}

func (h *Hub) enterTopic(topic string) {
	h.topicsMu.Lock()
	h.active[topic]++
	h.topicsMu.Unlock()
}

func (h *Hub) leaveTopic(topic string) {
	h.topicsMu.Lock()
	if n := h.active[topic]; n <= 1 {
		delete(h.active, topic)
	} else {
		h.active[topic] = n - 1
	}
	h.topicsMu.Unlock()
}
