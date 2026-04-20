package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- Helpers -------------------------------------------------------------

// fakeServer wraps an httptest.Server and tracks the requests it sees,
// so tests can assert on headers/body/path. Each handler is a func
// that gets called for the matching path; default returns 404.
type fakeServer struct {
	t        *testing.T
	srv      *httptest.Server
	handlers map[string]http.HandlerFunc
	mu       chan struct{} // crude lock for slice mutation
	hits     []*http.Request
	bodies   [][]byte
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{
		t:        t,
		handlers: map[string]http.HandlerFunc{},
		mu:       make(chan struct{}, 1),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu <- struct{}{}
		f.hits = append(f.hits, r)
		f.bodies = append(f.bodies, body)
		<-f.mu

		// Reset body so handlers can re-read if needed.
		r.Body = io.NopCloser(strings.NewReader(string(body)))

		// Path matching: try exact, then path-prefix.
		if h, ok := f.handlers[r.URL.Path]; ok {
			h(w, r)
			return
		}
		for prefix, h := range f.handlers {
			if strings.HasPrefix(r.URL.Path, prefix) {
				h(w, r)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) handle(path string, h http.HandlerFunc) { f.handlers[path] = h }

func (f *fakeServer) client() *Client {
	c := New(f.srv.URL)
	c.HTTP.Timeout = 2 * time.Second
	return c
}

// --- OpenSession ---------------------------------------------------------

func TestOpenSessionHappyPath(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("missing Accept header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"room_id": "abc12345",
			"sender_secret": "sec",
			"viewer_url": "https://app.fernsicht.space/#room=abc",
			"signaling_url": "https://signal.fernsicht.space",
			"expires_at": "2026-04-19T12:00:00Z",
			"expires_in": 43200,
			"max_viewers": 8,
			"poll_interval_hint": 25
		}`))
	})

	c := srv.client()
	sess, err := c.OpenSession(context.Background(), SessionConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.RoomID != "abc12345" || sess.SenderSecret != "sec" ||
		sess.MaxViewers != 8 || sess.PollIntervalHint != 25 {
		t.Errorf("unexpected fields: %+v", sess)
	}
	if c.SenderSecret != "sec" {
		t.Errorf("client sender_secret not stored: %q", c.SenderSecret)
	}
}

func TestOpenSessionSendsAPIKeyHeader(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/session", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Fernsicht-Api-Key"); got != "secret-api-key" {
			t.Errorf("X-Fernsicht-Api-Key = %q", got)
		}
		_, _ = w.Write([]byte(`{"room_id":"r","sender_secret":"s","viewer_url":"u"}`))
	})

	c := srv.client()
	if _, err := c.OpenSession(context.Background(), SessionConfig{APIKey: "secret-api-key"}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSessionSendsMaxViewersBody(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/session", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]int
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["max_viewers"] != 16 {
			t.Errorf("max_viewers in body: %v", got)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing Content-Type header")
		}
		_, _ = w.Write([]byte(`{"room_id":"r","sender_secret":"s","viewer_url":"u","max_viewers":16}`))
	})

	c := srv.client()
	if _, err := c.OpenSession(context.Background(), SessionConfig{MaxViewers: 16}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSessionRejectsNon200(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/session", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("invalid api key"))
	})

	c := srv.client()
	_, err := c.OpenSession(context.Background(), SessionConfig{})
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected status code in error: %v", err)
	}
}

func TestOpenSessionRejectsMissingFields(t *testing.T) {
	cases := map[string]string{
		"missing room_id":       `{"sender_secret":"s","viewer_url":"u"}`,
		"missing sender_secret": `{"room_id":"r","viewer_url":"u"}`,
		"missing viewer_url":    `{"room_id":"r","sender_secret":"s"}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newFakeServer(t)
			srv.handle("/session", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(payload))
			})
			c := srv.client()
			if _, err := c.OpenSession(context.Background(), SessionConfig{}); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestOpenSessionRejectsInvalidJSON(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/session", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	c := srv.client()
	if _, err := c.OpenSession(context.Background(), SessionConfig{}); err == nil {
		t.Error("expected error")
	}
}

func TestOpenSessionTransientOnNetworkError(t *testing.T) {
	// Point at an unused port to force a connection refused.
	c := New("http://127.0.0.1:1") // port 1 is reserved/unused
	c.HTTP.Timeout = 200 * time.Millisecond
	_, err := c.OpenSession(context.Background(), SessionConfig{})
	if err == nil {
		t.Fatal("expected network error")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

// --- PollTickets ---------------------------------------------------------

func TestPollTicketsHappyPath(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/poll/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret123" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/poll/abc12345" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"tickets":[
			{"ticket_id":"t-1","offer":{"type":"offer","sdp":"v=0\r\n..."}},
			{"ticket_id":"t-2","offer":{"type":"offer","sdp":"v=0\r\n..."}}
		]}`))
	})

	c := srv.client()
	c.SenderSecret = "secret123"
	tickets, err := c.PollTickets(context.Background(), "abc12345")
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 {
		t.Fatalf("expected 2 tickets, got %d", len(tickets))
	}
	if tickets[0].TicketID != "t-1" || tickets[1].TicketID != "t-2" {
		t.Errorf("ticket IDs: %+v", tickets)
	}
	if tickets[0].Offer.Type != "offer" {
		t.Errorf("offer type: %q", tickets[0].Offer.Type)
	}
}

func TestPollTicketsEmpty(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/poll/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tickets":[]}`))
	})
	c := srv.client()
	tickets, err := c.PollTickets(context.Background(), "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 0 {
		t.Errorf("expected empty, got %+v", tickets)
	}
}

func TestPollTicketsNullTicketsField(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/poll/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	c := srv.client()
	tickets, err := c.PollTickets(context.Background(), "r")
	if err != nil {
		t.Fatal(err)
	}
	if tickets == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestPollTicketsInvalidSecret(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/poll/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	c := srv.client()
	_, err := c.PollTickets(context.Background(), "r")
	if !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("expected ErrInvalidSecret, got %v", err)
	}
}

func TestPollTicketsRoomNotFound(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/poll/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := srv.client()
	_, err := c.PollTickets(context.Background(), "r")
	if !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestPollTicketsTransientOn5xx(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/poll/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	c := srv.client()
	_, err := c.PollTickets(context.Background(), "r")
	if !errors.Is(err, ErrTransient) {
		t.Errorf("expected ErrTransient on 502, got %v", err)
	}
}

func TestPollTicketsContextCancellation(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/poll/", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte(`{"tickets":[]}`))
	})
	c := srv.client()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.PollTickets(ctx, "r")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// --- PostAnswer ----------------------------------------------------------

func TestPostAnswerHappyPath(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.Path != "/ticket/t-1/answer" {
			t.Errorf("path: %q", r.URL.Path)
		}
		var body struct {
			Answer SessionDescription `json:"answer"`
			Secret string             `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Secret != "sec" {
			t.Errorf("secret in body: %q", body.Secret)
		}
		if body.Answer.Type != "answer" {
			t.Errorf("answer type: %q", body.Answer.Type)
		}
		w.WriteHeader(http.StatusOK)
	})

	c := srv.client()
	c.SenderSecret = "sec"
	err := c.PostAnswer(context.Background(), "t-1", SessionDescription{Type: "answer", SDP: "v=0\r\n"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostAnswerInvalidSecret(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	c := srv.client()
	err := c.PostAnswer(context.Background(), "t", SessionDescription{Type: "answer", SDP: ""})
	if !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("expected ErrInvalidSecret, got %v", err)
	}
}

// --- PostSenderICE -------------------------------------------------------

func TestPostSenderICEHappyPath(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ticket/t-1/ice/sender" {
			t.Errorf("path: %q", r.URL.Path)
		}
		var body struct {
			Candidates []ICECandidate `json:"candidates"`
			Secret     string         `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Candidates) != 2 {
			t.Errorf("candidate count: %d", len(body.Candidates))
		}
		w.WriteHeader(http.StatusOK)
	})

	c := srv.client()
	c.SenderSecret = "sec"
	mid := "0"
	idx := 0
	cands := []ICECandidate{
		{Candidate: "candidate:1 1 UDP 0 192.0.2.1 1234 typ host", SDPMid: &mid, SDPMLineIndex: &idx},
		{Candidate: "candidate:2 1 UDP 0 192.0.2.2 5678 typ host"},
	}
	if err := c.PostSenderICE(context.Background(), "t-1", cands); err != nil {
		t.Fatal(err)
	}
}

func TestPostSenderICEEmptyIsNoop(t *testing.T) {
	// The Python SDK's _flush_sender_ice early-returns on empty.
	// Replicate that: empty candidates should NOT make an HTTP call.
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not have been called")
	})
	c := srv.client()
	if err := c.PostSenderICE(context.Background(), "t-1", nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := c.PostSenderICE(context.Background(), "t-1", []ICECandidate{}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- PollViewerICE -------------------------------------------------------

func TestPollViewerICEHappyPath(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ticket/t-1/ice/viewer" {
			t.Errorf("path: %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("since"); got != "5" {
			t.Errorf("since query: %q", got)
		}
		_, _ = w.Write([]byte(`{
			"candidates": [
				{"candidate":"candidate:1 1 UDP 0 192.0.2.1 1234 typ host","sdpMid":"0","sdpMLineIndex":0}
			],
			"seq": 7
		}`))
	})
	c := srv.client()
	resp, err := c.PollViewerICE(context.Background(), "t-1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Seq != 7 {
		t.Errorf("seq: %d", resp.Seq)
	}
	if len(resp.Candidates) != 1 {
		t.Errorf("candidates: %+v", resp.Candidates)
	}
}

func TestPollViewerICEEmptyCandidates(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"seq":3}`))
	})
	c := srv.client()
	resp, err := c.PollViewerICE(context.Background(), "t-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Candidates == nil {
		t.Error("expected non-nil empty slice")
	}
}

func TestPollViewerICE404RoomNotFound(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := srv.client()
	_, err := c.PollViewerICE(context.Background(), "t-1", 0)
	if !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("expected ErrRoomNotFound, got %v", err)
	}
}

func TestPollViewerICETransientOn5xx(t *testing.T) {
	srv := newFakeServer(t)
	srv.handle("/ticket/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := srv.client()
	_, err := c.PollViewerICE(context.Background(), "t-1", 0)
	if !errors.Is(err, ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

// --- Backoff -------------------------------------------------------------

func TestBackoffSuccessResetsState(t *testing.T) {
	bo := DefaultBackoff()
	now := time.Now()
	bo.RecordFailure(now)
	bo.RecordFailure(now.Add(1 * time.Second))
	if bo.Failures() != 2 {
		t.Errorf("failures: %d", bo.Failures())
	}
	bo.RecordSuccess()
	if bo.Failures() != 0 {
		t.Errorf("failures after success: %d", bo.Failures())
	}
}

func TestBackoffFirstFailureRetriesImmediately(t *testing.T) {
	// §9: "First failure: retry immediately."
	bo := DefaultBackoff()
	ev, d := bo.RecordFailure(time.Now())
	if ev != BackoffRetry {
		t.Errorf("event: %v", ev)
	}
	if d != 0 {
		t.Errorf("expected 0 delay, got %v", d)
	}
}

func TestBackoffExponentialGrowth(t *testing.T) {
	bo := &Backoff{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		AlertAfter:   100, // out of the way
		FatalAfter:   1 * time.Hour,
	}
	now := time.Now()

	// failure 1 → 0
	if _, d := bo.RecordFailure(now); d != 0 {
		t.Errorf("failure 1: expected 0, got %v", d)
	}
	// failure 2 → InitialDelay
	if _, d := bo.RecordFailure(now); d != 100*time.Millisecond {
		t.Errorf("failure 2: expected 100ms, got %v", d)
	}
	// failure 3 → InitialDelay * 2
	if _, d := bo.RecordFailure(now); d != 200*time.Millisecond {
		t.Errorf("failure 3: expected 200ms, got %v", d)
	}
	// failure 4 → InitialDelay * 4
	if _, d := bo.RecordFailure(now); d != 400*time.Millisecond {
		t.Errorf("failure 4: expected 400ms, got %v", d)
	}
}

func TestBackoffCapsAtMaxDelay(t *testing.T) {
	bo := &Backoff{
		InitialDelay: 1 * time.Second,
		MaxDelay:     5 * time.Second,
		AlertAfter:   100,
		FatalAfter:   1 * time.Hour,
	}
	now := time.Now()
	// Push hard.
	for i := 0; i < 20; i++ {
		bo.RecordFailure(now)
	}
	_, d := bo.RecordFailure(now)
	if d != 5*time.Second {
		t.Errorf("expected capped at 5s, got %v", d)
	}
}

func TestBackoffEmitsAlertAtThreshold(t *testing.T) {
	bo := &Backoff{
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		AlertAfter:   3,
		FatalAfter:   1 * time.Hour,
	}
	now := time.Now()
	got := []BackoffEvent{}
	for i := 0; i < 5; i++ {
		ev, _ := bo.RecordFailure(now)
		got = append(got, ev)
	}
	// Want: Retry, Retry, Alert, Retry, Retry  (alert fires once at 3rd)
	want := []BackoffEvent{BackoffRetry, BackoffRetry, BackoffAlert, BackoffRetry, BackoffRetry}
	for i, ev := range got {
		if ev != want[i] {
			t.Errorf("failure %d: got %v, want %v", i+1, ev, want[i])
		}
	}
}

func TestBackoffAlertReArmsAfterSuccess(t *testing.T) {
	bo := &Backoff{
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		AlertAfter:   2,
		FatalAfter:   1 * time.Hour,
	}
	now := time.Now()
	bo.RecordFailure(now)
	bo.RecordFailure(now) // alert fires
	bo.RecordSuccess()    // reset
	bo.RecordFailure(now)
	ev, _ := bo.RecordFailure(now) // should alert again
	if ev != BackoffAlert {
		t.Errorf("expected re-armed alert, got %v", ev)
	}
}

func TestBackoffEmitsFatalAfterFiveMinutes(t *testing.T) {
	bo := &Backoff{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     60 * time.Second,
		AlertAfter:   5,
		FatalAfter:   5 * time.Minute,
	}
	now := time.Now()
	// First failure starts the clock.
	bo.RecordFailure(now)
	// Failures within window are still retries / alerts.
	ev, _ := bo.RecordFailure(now.Add(2 * time.Minute))
	if ev == BackoffFatal {
		t.Errorf("fatal too early: %v", ev)
	}
	// Crossing the 5-minute boundary fires Fatal.
	ev, d := bo.RecordFailure(now.Add(5 * time.Minute))
	if ev != BackoffFatal {
		t.Errorf("expected BackoffFatal, got %v", ev)
	}
	if d != 0 {
		t.Errorf("fatal delay should be 0, got %v", d)
	}
}

func TestBackoffDoesNotGoFatalIfFatalAfterIsZero(t *testing.T) {
	// FatalAfter=0 disables the fatal threshold. Useful for tests.
	bo := &Backoff{
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		AlertAfter:   2,
		FatalAfter:   0,
	}
	now := time.Now()
	for i := 0; i < 100; i++ {
		ev, _ := bo.RecordFailure(now.Add(time.Hour * time.Duration(i)))
		if ev == BackoffFatal {
			t.Fatalf("unexpected fatal at iteration %d", i)
		}
	}
}
