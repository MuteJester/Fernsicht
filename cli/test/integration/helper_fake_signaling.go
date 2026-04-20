// Shared fake signaling server for integration tests.
//
// Phase 1 / Phase 2 tests originally didn't need a bridge connection
// (run.go just spawned the wrapped command). Phase 3 made run.go
// always open a session, so every test now needs SOMETHING listening
// on FERNSICHT_SERVER_URL — otherwise each test would wait the full
// 30s session-open timeout connecting to a non-existent server.
//
// We start one fake signaling server per test process via TestMain
// and inject FERNSICHT_SERVER_URL into the env. Tests that want a
// dedicated fake (Phase 3 tests) override the env via run() helper.

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

// sharedFake is a process-wide fake signaling server. Started by
// TestMain in run_test.go.
type sharedFakeServer struct {
	srv     *httptest.Server
	mu      sync.Mutex
	polls   int
}

func startSharedFake() *sharedFakeServer {
	f := &sharedFakeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/session", f.handleSession)
	mux.HandleFunc("/poll/", f.handlePoll)
	mux.HandleFunc("/ticket/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *sharedFakeServer) URL() string { return f.srv.URL }
func (f *sharedFakeServer) Close()      { f.srv.Close() }

func (f *sharedFakeServer) handleSession(w http.ResponseWriter, _ *http.Request) {
	body := map[string]any{
		"room_id":            "shared-fake",
		"sender_secret":      "shared-secret",
		"viewer_url":         "https://app.example/#room=shared-fake",
		"signaling_url":      f.srv.URL,
		"expires_at":         "2099-01-01T00:00:00Z",
		"expires_in":         3600,
		"max_viewers":        8,
		"poll_interval_hint": 30,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (f *sharedFakeServer) handlePoll(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.polls++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"tickets":[]}`))
}
