package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Minimal fake signaling server ---------------------------------
//
// Just enough of /session + /poll/{room} for the bridge dispatcher to
// open a session and idle on the poll loop. No real WebRTC needed —
// embed never sees viewer handshakes when no /watch arrives.

func newFakeSignaling(t *testing.T, opts fakeOpts) *fakeServer {
	t.Helper()
	f := &fakeServer{
		t:        t,
		opts:     opts,
		polls:    0,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/session", f.handleSession)
	mux.HandleFunc("/poll/", f.handlePoll)
	mux.HandleFunc("/ticket/", func(w http.ResponseWriter, r *http.Request) {
		// embed tests never queue tickets; reject anything that arrives.
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type fakeOpts struct {
	roomID         string
	requireAPIKey  string
	sessionLatency time.Duration
}

type fakeServer struct {
	t    *testing.T
	srv  *httptest.Server
	opts fakeOpts

	mu    sync.Mutex
	polls int
}

func (f *fakeServer) URL() string { return f.srv.URL }
func (f *fakeServer) PollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

func (f *fakeServer) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if f.opts.sessionLatency > 0 {
		time.Sleep(f.opts.sessionLatency)
	}
	if f.opts.requireAPIKey != "" {
		got := r.Header.Get("X-Fernsicht-Api-Key")
		if got != f.opts.requireAPIKey {
			http.Error(w, "bad api key", http.StatusForbidden)
			return
		}
	}
	body := map[string]any{
		"room_id":            f.opts.roomID,
		"sender_secret":      "test-secret",
		"viewer_url":         fmt.Sprintf("https://app.example/#room=%s", f.opts.roomID),
		"signaling_url":      f.srv.URL,
		"expires_at":         "2099-01-01T00:00:00Z",
		"expires_in":         3600,
		"max_viewers":        8,
		"poll_interval_hint": 1,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeServer) handlePoll(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.polls++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"tickets":[]}`))
}

// --- Tests ---------------------------------------------------------

func TestEmbed_OpenAndClose_RoundTrip(t *testing.T) {
	srv := newFakeSignaling(t, fakeOpts{roomID: "abc12345"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := Open(ctx, Config{
		ServerURL:  srv.URL(),
		MaxViewers: 4,
		SDKID:      "embed-test",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if !sess.IsOpen() {
		t.Errorf("expected IsOpen() true after Open")
	}
	if sess.RoomID() != "abc12345" {
		t.Errorf("RoomID got %q want abc12345", sess.RoomID())
	}
	if !strings.Contains(sess.ViewerURL(), "abc12345") {
		t.Errorf("ViewerURL should include roomID; got %q", sess.ViewerURL())
	}
	if sess.Info().BridgeVersion == "" {
		t.Errorf("BridgeVersion should be populated from hello_ack")
	}

	if err := sess.Close(ctx); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if sess.IsOpen() {
		t.Errorf("expected IsOpen() false after Close")
	}
	// Closing again is idempotent.
	if err := sess.Close(ctx); err != nil {
		t.Errorf("second Close returned error: %v", err)
	}
}

func TestEmbed_OpenWithAPIKey(t *testing.T) {
	srv := newFakeSignaling(t, fakeOpts{
		roomID:        "auth1",
		requireAPIKey: "secret123",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Wrong key → fails.
	_, err := Open(ctx, Config{
		ServerURL:  srv.URL(),
		JoinSecret: "wrong",
		MaxViewers: 1,
		SDKID:      "embed-test",
	})
	if err == nil {
		t.Error("expected Open to fail with wrong join secret")
	}

	// Right key → succeeds.
	sess, err := Open(ctx, Config{
		ServerURL:  srv.URL(),
		JoinSecret: "secret123",
		MaxViewers: 1,
		SDKID:      "embed-test",
	})
	if err != nil {
		t.Fatalf("Open with correct key failed: %v", err)
	}
	defer sess.Close(ctx)
}

func TestEmbed_SessionTimeout(t *testing.T) {
	srv := newFakeSignaling(t, fakeOpts{
		roomID:         "slow1",
		sessionLatency: 2 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Tight timeout → expect ErrTimeout (or equivalent).
	_, err := Open(ctx, Config{
		ServerURL:      srv.URL(),
		SDKID:          "embed-test",
		SessionTimeout: 200 * time.Millisecond,
		HelloTimeout:   1 * time.Second,
	})
	if err == nil {
		t.Error("expected timeout error; got nil")
	}
}

func TestEmbed_Tick_RequiresOpenSession(t *testing.T) {
	srv := newFakeSignaling(t, fakeOpts{roomID: "r1"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := Open(ctx, Config{ServerURL: srv.URL(), SDKID: "embed-test"})
	if err != nil {
		t.Fatal(err)
	}
	sess.Close(ctx)

	if err := sess.Tick(Tick{TaskID: "t1", Value: 0.5}); err != ErrClosed {
		t.Errorf("Tick on closed session: got %v, want ErrClosed", err)
	}
	if err := sess.StartTask("t1", "Training"); err != ErrClosed {
		t.Errorf("StartTask on closed session: got %v, want ErrClosed", err)
	}
}

func TestEmbed_FullLifecycle_StartTickEnd(t *testing.T) {
	srv := newFakeSignaling(t, fakeOpts{roomID: "lifecycle"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := Open(ctx, Config{ServerURL: srv.URL(), SDKID: "embed-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(ctx)

	if err := sess.StartTask("t1", "Training"); err != nil {
		t.Errorf("StartTask: %v", err)
	}
	for i := 1; i <= 5; i++ {
		err := sess.Tick(Tick{
			TaskID: "t1",
			Value:  float64(i) / 5,
			N:      i,
			Total:  5,
			Unit:   "step",
		})
		if err != nil {
			t.Errorf("Tick %d: %v", i, err)
		}
	}
	if err := sess.EndTask("t1"); err != nil {
		t.Errorf("EndTask: %v", err)
	}
}

func TestEmbed_PollLoopHitsServer(t *testing.T) {
	srv := newFakeSignaling(t, fakeOpts{roomID: "poll1"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := Open(ctx, Config{ServerURL: srv.URL(), SDKID: "embed-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(ctx)

	// Bridge polls every poll_interval_hint seconds (we set it to 1).
	// Wait a couple cycles, expect ≥1 poll.
	time.Sleep(2500 * time.Millisecond)
	if got := srv.PollCount(); got == 0 {
		t.Errorf("expected ≥1 poll within 2.5s; got 0")
	}
}

func TestEmbed_EventHookReceivesAsyncEvents(t *testing.T) {
	srv := newFakeSignaling(t, fakeOpts{roomID: "hook1"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := Open(ctx, Config{ServerURL: srv.URL(), SDKID: "embed-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(ctx)

	var seenMu sync.Mutex
	seen := []string{}
	sess.SetEventHook(func(name string, _ json.RawMessage) {
		seenMu.Lock()
		seen = append(seen, name)
		seenMu.Unlock()
	})

	// Trigger the `closed` event by closing.
	sess.Close(ctx)

	seenMu.Lock()
	gotClosed := false
	for _, n := range seen {
		if n == "closed" {
			gotClosed = true
		}
	}
	seenMu.Unlock()
	if !gotClosed {
		t.Errorf("expected 'closed' event via hook; saw %v", seen)
	}
}
