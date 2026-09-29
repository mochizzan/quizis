package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"quiz/internal/handlers"
	"quiz/internal/realtime"
	"quiz/internal/testutil"
)

// --- SSE client helpers (raw EventSource-style reads) ----------------------

// sseConn is an open stream; lines is fed by one reader goroutine and
// closed when the server ends the connection.
type sseConn struct {
	t     *testing.T
	resp  *http.Response
	lines chan string
}

func openSSE(t *testing.T, url string, cookies ...*http.Cookie) *sseConn {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req) // no timeout: streams stay open
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	c := &sseConn{t: t, resp: resp, lines: make(chan string, 1024)}
	go func() {
		defer close(c.lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	return c
}

// readEvent reads one SSE frame (lines up to the blank separator).
// ok=false means the stream ended or the timeout hit first.
func (c *sseConn) readEvent(timeout time.Duration) (event, data, raw string, ok bool) {
	c.t.Helper()
	deadline := time.After(timeout)
	var sb strings.Builder
	for {
		select {
		case line, open := <-c.lines:
			if !open {
				return event, data, sb.String(), false
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
			if line == "" {
				return event, data, sb.String(), true
			}
			if v, cut := strings.CutPrefix(line, "event:"); cut {
				event = strings.TrimSpace(v)
			}
			if v, cut := strings.CutPrefix(line, "data:"); cut {
				data = strings.TrimSpace(v)
			}
		case <-deadline:
			return event, data, sb.String(), false
		}
	}
}

// waitClosed drains the stream until the server closes the connection.
func (c *sseConn) waitClosed(timeout time.Duration) bool {
	c.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, open := <-c.lines:
			if !open {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// waitPing blocks until a heartbeat ping proves the subscription is live
// (publishes before this point could race the subscription registration).
func (c *sseConn) waitPing(timeout time.Duration) {
	c.t.Helper()
	deadline := time.After(timeout)
	for {
		ev, _, _, ok := c.readEvent(timeout)
		if !ok {
			c.t.Fatalf("stream ended while waiting for ping")
		}
		if ev == "ping" {
			return
		}
		select {
		case <-deadline:
			c.t.Fatal("no ping received — stream not live")
		default:
		}
	}
}

// streamStatus GETs a stream endpoint expecting a pre-upgrade answer
// (redirects not followed; a wrongly-opened stream times out instead of
// hanging — the read error is deliberately ignored).
func streamStatus(t *testing.T, url string, cookies ...*http.Cookie) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// newStreamServer serves one open (auth-less) stream endpoint on /s.
func newStreamServer(t *testing.T, topic string) (*realtime.Hub, *httptest.Server) {
	t.Helper()
	hub, err := realtime.New()
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	t.Cleanup(hub.Close)
	e := echo.New()
	e.GET("/s", hub.Handler(topic, nil))
	ts := httptest.NewServer(e)
	t.Cleanup(ts.Close)
	return hub, ts
}

// --- spec §6.3 stream access rules ----------------------------------------

// Membership + session gating of both streams: 200 stream for a member,
// envelopes otherwise, role checks, and the teacher monitor route guard.
func TestSSEStreamAccessRules(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	guru := guruLogin(t, ts)

	qid := createQuiz(t, ts, guru, quizForm("Stream access quiz"))
	var code string
	if err := pool.QueryRow(`SELECT code FROM quizzes WHERE id = ?`, qid).Scan(&code); err != nil {
		t.Fatalf("code: %v", err)
	}
	streamURL := fmt.Sprintf("%s/quiz/%s/stream", ts.URL, code)

	// anonymous → 401 envelope
	status, body := streamStatus(t, streamURL)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous stream = %d, want 401 (%s)", status, body)
	}
	if env := decodeEnv(t, body); env.Error != "UNAUTHENTICATED" {
		t.Errorf("error = %q, want UNAUTHENTICATED", env.Error)
	}

	// anonymous + UNKNOWN code is also 401: the session gate runs before
	// the code lookup, so a prober can never distinguish an existing join
	// code from a nonexistent one (no quiz-existence oracle)
	status, body = streamStatus(t, ts.URL+"/quiz/ZZZZ99/stream")
	if status != http.StatusUnauthorized {
		t.Errorf("anonymous unknown-code stream = %d, want 401 (%s)", status, body)
	}

	// a participant gets the open stream
	member := registerUser(t, ts, "mira", "mira@example.test", "Mira Stream", "secret123")
	resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/participants/add", ts.URL, qid),
		map[string]string{"username": "mira"}, guru)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add member = %d: %s", resp.StatusCode, body)
	}
	conn := openSSE(t, streamURL, member)
	if conn.resp.StatusCode != http.StatusOK {
		t.Fatalf("member stream = %d, want 200", conn.resp.StatusCode)
	}
	if ct := conn.resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	// non-member → 403 FORBIDDEN
	other := registerUser(t, ts, "toni", "toni@example.test", "Toni Stream", "secret123")
	status, body = streamStatus(t, streamURL, other)
	if status != http.StatusForbidden {
		t.Fatalf("non-member stream = %d, want 403 (%s)", status, body)
	}
	if env := decodeEnv(t, body); env.Error != "FORBIDDEN" {
		t.Errorf("error = %q, want FORBIDDEN", env.Error)
	}

	// a removed student is treated like a non-member on a reconnect
	if _, err := pool.Exec(
		`UPDATE participants SET status = 'dikeluarkan'
		 WHERE quiz_id = ? AND user_id = (SELECT id FROM users WHERE username = 'mira')`,
		qid); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	status, body = streamStatus(t, streamURL, member)
	if status != http.StatusForbidden {
		t.Fatalf("removed student stream = %d, want 403 (%s)", status, body)
	}

	// unknown code → 404 INVALID_CODE with the canonical message
	status, body = streamStatus(t, ts.URL+"/quiz/ZZZZ99/stream", other)
	if status != http.StatusNotFound {
		t.Fatalf("unknown code = %d, want 404 (%s)", status, body)
	}
	if env := decodeEnv(t, body); env.Error != "INVALID_CODE" || env.Message != handlers.MsgInvalidCode {
		t.Errorf("envelope = %+v, want INVALID_CODE %q", env, handlers.MsgInvalidCode)
	}

	// guru on the student stream → 401 (guru is not a murid)
	status, body = streamStatus(t, streamURL, guru)
	if status != http.StatusUnauthorized {
		t.Errorf("guru student stream = %d, want 401 (%s)", status, body)
	}

	// monitor: guru → open stream
	monitorURL := fmt.Sprintf("%s/teacher/quiz/%d/monitor/stream", ts.URL, qid)
	mconn := openSSE(t, monitorURL, guru)
	if mconn.resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor stream = %d, want 200", mconn.resp.StatusCode)
	}
	if ct := mconn.resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("monitor content-type = %q, want text/event-stream", ct)
	}
	// anonymous → redirect to /login; murid → redirect home (route guard)
	if status, _ := streamStatus(t, monitorURL); status != http.StatusFound {
		t.Errorf("anonymous monitor = %d, want 302", status)
	}
	if status, _ := streamStatus(t, monitorURL, other); status != http.StatusFound {
		t.Errorf("murid monitor = %d, want 302", status)
	}
	// unknown quiz → 404 envelope
	status, body = streamStatus(t,
		fmt.Sprintf("%s/teacher/quiz/999999/monitor/stream", ts.URL), guru)
	if status != http.StatusNotFound {
		t.Errorf("unknown monitor = %d, want 404 (%s)", status, body)
	}
}

// --- hub mechanics (open mini-servers with direct hub access) -------------

// Publish reaches the subscriber with the event type and a JSON payload
// round-trip; events for other topics do not leak.
func TestSSEPublishReachesSubscribers(t *testing.T) {
	topic := realtime.TopicForQuizID(9)
	hub, ts := newStreamServer(t, topic)
	hub.SetHeartbeatEvery(50 * time.Millisecond)

	conn := openSSE(t, ts.URL+"/s")
	if conn.resp.StatusCode != http.StatusOK {
		t.Fatalf("stream = %d, want 200", conn.resp.StatusCode)
	}
	conn.waitPing(5 * time.Second) // subscription confirmed live

	if err := hub.Publish(topic, realtime.Event{
		Type: "answered",
		Data: map[string]any{"pid": 3, "score": 400},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	ev, data, raw, ok := conn.readEvent(5 * time.Second)
	if !ok {
		t.Fatalf("no event received (raw %q)", raw)
	}
	if ev != "answered" {
		t.Fatalf("event = %q, want answered", ev)
	}
	var got realtime.Event
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("data %q: %v", data, err)
	}
	if got.Type != "answered" {
		t.Errorf("decoded type = %q, want answered", got.Type)
	}
	d, _ := got.Data.(map[string]any)
	if d["pid"] != float64(3) || d["score"] != float64(400) {
		t.Errorf("decoded data = %v, want pid=3 score=400", d)
	}

	// topic isolation — only pings may arrive during the window, never the
	// other quiz's event (the loop is time-bounded because pings keep it fed)
	if err := hub.Publish(realtime.TopicForQuizID(10),
		realtime.Event{Type: "secret", Data: nil}); err != nil {
		t.Fatalf("publish other: %v", err)
	}
	isoEnd := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(isoEnd) {
		ev, _, _, _ := conn.readEvent(time.Until(isoEnd))
		if ev == "secret" {
			t.Fatal("received an event from a different topic")
		}
	}
}

// A client that never reads cannot stall the hub: publishes stay fast and
// the connection is closed by the server (spec §6.3 slow-client guard).
func TestSSESlowClientCannotBlockHub(t *testing.T) {
	topic := realtime.TopicForQuizID(31)
	hub, ts := newStreamServer(t, topic)
	hub.SetHeartbeatEvery(50 * time.Millisecond)

	// raw connection: read only the first ping to prove the subscription is
	// live, then never drain again
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn,
		"GET /s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	br := bufio.NewReader(conn)
	prime := make(chan error, 1)
	go func() {
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				prime <- err
				return
			}
			if strings.HasPrefix(line, "event: ping") {
				prime <- nil
				return
			}
		}
	}()
	select {
	case err := <-prime:
		if err != nil {
			t.Fatalf("prime read: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream not live (no ping)")
	}

	// QueueSize+10 oversized events: the first few fill the socket buffers,
	// the rest must trip the guard instead of blocking Publish.
	payload := strings.Repeat("x", 4<<20) // 4 MB
	for i := 0; i < realtime.QueueSize+10; i++ {
		start := time.Now()
		_ = hub.Publish(topic, realtime.Event{Type: "flood", Data: payload}) // guard error expected
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("publish #%d blocked for %v — hub stalled", i, d)
		}
	}

	// the slow connection is closed: draining ends in EOF, not a timeout
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatalf("slow connection not closed: %v", err)
	}
}

// The heartbeat loop pings every active topic; the interval is settable so
// the test does not wait 15 s.
func TestSSEHeartbeatPing(t *testing.T) {
	topic := realtime.TopicForQuizID(41)
	hub, ts := newStreamServer(t, topic)
	hub.SetHeartbeatEvery(40 * time.Millisecond)

	conn := openSSE(t, ts.URL+"/s")
	for i := 1; i <= 2; i++ {
		ev, _, raw, ok := conn.readEvent(5 * time.Second)
		if !ok {
			t.Fatalf("ping #%d not received (raw %q)", i, raw)
		}
		if ev != "ping" {
			t.Fatalf("event = %q, want ping", ev)
		}
	}
}

// Restart recovery: hub #1's subscribers die with it; a fresh hub delivers
// a snapshot rebuilt from the same DB rows to new subscribers — the
// rehydrate contract §6.3 relies on.
func TestSSERestartRehydrate(t *testing.T) {
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users", "password_resets",
		"quizzes", "questions", "quiz_questions", "participants",
		"answers", "anti_cheat_events")
	uidA := seedUser(t, pool, "raa", false)
	uidB := seedUser(t, pool, "rbb", false)
	res, err := pool.Exec(
		`INSERT INTO quizzes (code, judul, timer_type) VALUES ('REHY1', 'Rehydrate quiz', 'global')`)
	if err != nil {
		t.Fatalf("quiz insert: %v", err)
	}
	qid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("quiz id: %v", err)
	}
	if _, err := pool.Exec(
		`INSERT INTO participants (quiz_id, user_id) VALUES (?, ?), (?, ?)`,
		qid, uidA, qid, uidB); err != nil {
		t.Fatalf("participants: %v", err)
	}
	topic := realtime.TopicForQuizID(uint64(qid))

	// hub #1 delivers live, then the process goes away
	hub1, ts1 := newStreamServer(t, topic)
	hub1.SetHeartbeatEvery(50 * time.Millisecond)
	conn1 := openSSE(t, ts1.URL+"/s")
	if conn1.resp.StatusCode != http.StatusOK {
		t.Fatalf("stream #1 = %d, want 200", conn1.resp.StatusCode)
	}
	conn1.waitPing(5 * time.Second)
	if err := hub1.Publish(topic, realtime.Event{
		Type: "joined", Data: map[string]any{"n": 1},
	}); err != nil {
		t.Fatalf("publish #1: %v", err)
	}
	if ev, _, _, ok := conn1.readEvent(5 * time.Second); !ok || ev != "joined" {
		t.Fatalf("first hub event = %q ok=%v, want joined", ev, ok)
	}
	hub1.Close()
	if !conn1.waitClosed(5 * time.Second) {
		t.Fatal("old connection survived the hub shutdown")
	}

	// hub #2 is fresh: a new subscriber sees only what is re-published
	// from the DB (the rehydrate Step 11 wires in)
	hub2, ts2 := newStreamServer(t, topic)
	hub2.SetHeartbeatEvery(50 * time.Millisecond)
	conn2 := openSSE(t, ts2.URL+"/s")
	if conn2.resp.StatusCode != http.StatusOK {
		t.Fatalf("stream #2 = %d, want 200", conn2.resp.StatusCode)
	}
	conn2.waitPing(5 * time.Second)

	var uids []uint64
	rows, err := pool.Query(`SELECT user_id FROM participants WHERE quiz_id = ? ORDER BY id`, qid)
	if err != nil {
		t.Fatalf("query participants: %v", err)
	}
	for rows.Next() {
		var u uint64
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			t.Fatalf("scan participant: %v", err)
		}
		uids = append(uids, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if err := hub2.Publish(topic, realtime.Event{Type: "snapshot", Data: uids}); err != nil {
		t.Fatalf("snapshot publish: %v", err)
	}
	ev, data, raw, ok := conn2.readEvent(5 * time.Second)
	if !ok {
		t.Fatalf("snapshot not received (raw %q)", raw)
	}
	if ev != "snapshot" {
		t.Fatalf("event = %q, want snapshot", ev)
	}
	var got struct {
		Data []uint64 `json:"data"`
	}
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("snapshot decode %q: %v", data, err)
	}
	want := map[uint64]bool{uidA: true, uidB: true}
	if len(got.Data) != len(want) {
		t.Fatalf("snapshot = %v, want both participants", got.Data)
	}
	for _, u := range got.Data {
		if !want[u] {
			t.Errorf("unexpected participant %d in snapshot", u)
		}
	}
}
