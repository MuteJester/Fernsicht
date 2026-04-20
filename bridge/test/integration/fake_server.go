// Package integration hosts the Phase 7 end-to-end test that spawns
// fernsicht-bridge as a subprocess and drives the full §4 protocol
// against a stand-in HTTP signaling server + an in-process pion
// viewer.
//
// This file implements the fake signaling server — a minimal HTTP
// fake that matches the real Fernsicht server's endpoint contracts
// closely enough to convince the bridge it's talking to production.
//
// The fake is NOT a full server simulation: it doesn't validate
// secrets, doesn't enforce rate limits, and doesn't expire anything.
// It only has to be faithful enough for one end-to-end handshake.
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSignalingServer is an httptest.Server exposing the minimum set
// of Fernsicht endpoints the bridge calls. Tests enqueue tickets for
// the bridge to poll and read back the answer/ICE the bridge posts
// so they can be forwarded to the in-process viewer.
type fakeSignalingServer struct {
	srv *httptest.Server

	mu sync.Mutex

	// Session state. Populated when POST /session is called.
	roomID string
	secret string

	// Tickets queued for the next GET /poll/{room} response.
	pendingTickets []ticketPayload

	// For each ticket: the SDP answer the bridge posted (nil = not yet)
	// and the ICE candidates the bridge has posted so far.
	answers       map[string]sdpPayload
	senderICE     map[string][]map[string]any
	// ICE candidates the viewer has pushed (served via GET ice/viewer).
	viewerICE    map[string][]map[string]any
	viewerICESeq map[string]int
}

type ticketPayload struct {
	TicketID string      `json:"ticket_id"`
	Offer    sdpPayload  `json:"offer"`
}

type sdpPayload struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// newFakeSignalingServer stands up a server wrapped in t.Cleanup.
func newFakeSignalingServer(t *testing.T) *fakeSignalingServer {
	t.Helper()
	f := &fakeSignalingServer{
		roomID:       "room-integ-abc",
		secret:       "sec-integ-xyz",
		answers:      map[string]sdpPayload{},
		senderICE:    map[string][]map[string]any{},
		viewerICE:    map[string][]map[string]any{},
		viewerICESeq: map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/session", f.handleSession)
	mux.HandleFunc("/poll/", f.handlePoll)
	mux.HandleFunc("/ticket/", f.handleTicket)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// URL returns the base URL tests should pass to the bridge's
// `session` command.
func (f *fakeSignalingServer) URL() string { return f.srv.URL }

// RoomID returns the room ID the server is advertising. Lets tests
// assert the bridge's session_ready event lines up.
func (f *fakeSignalingServer) RoomID() string { return f.roomID }

// QueueTicket enqueues a ticket for the bridge's next /poll call.
func (f *fakeSignalingServer) QueueTicket(ticketID, sdp string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingTickets = append(f.pendingTickets, ticketPayload{
		TicketID: ticketID,
		Offer:    sdpPayload{Type: "offer", SDP: sdp},
	})
}

// WaitForAnswer blocks until the bridge has POSTed an answer for
// ticketID, or the deadline expires.
func (f *fakeSignalingServer) WaitForAnswer(t *testing.T, ticketID string, timeout time.Duration) sdpPayload {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		ans, ok := f.answers[ticketID]
		f.mu.Unlock()
		if ok {
			return ans
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("bridge never posted answer for ticket %s within %s", ticketID, timeout)
	return sdpPayload{}
}

// PushViewerICE stores a viewer ICE candidate that the bridge will
// fetch on its next PollViewerICE call.
func (f *fakeSignalingServer) PushViewerICE(ticketID string, candidate map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewerICE[ticketID] = append(f.viewerICE[ticketID], candidate)
}

// DrainSenderICE returns all sender ICE candidates the bridge has
// POSTed for ticketID since the last call (and clears the buffer).
// Used to forward them to the in-process viewer.
func (f *fakeSignalingServer) DrainSenderICE(ticketID string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.senderICE[ticketID]
	f.senderICE[ticketID] = nil
	return out
}

// --- HTTP handlers ------------------------------------------------------

func (f *fakeSignalingServer) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f.mu.Lock()
	resp := map[string]any{
		"room_id":            f.roomID,
		"sender_secret":      f.secret,
		"viewer_url":         fmt.Sprintf("https://app.example/#room=%s", f.roomID),
		"signaling_url":      f.srv.URL,
		"expires_at":         "2026-04-19T12:00:00Z",
		"expires_in":         3600,
		"max_viewers":        8,
		"poll_interval_hint": 1, // short for tests
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeSignalingServer) handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// We don't check Authorization — tests care about flow, not auth.
	f.mu.Lock()
	tickets := f.pendingTickets
	f.pendingTickets = nil
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tickets": tickets})
}

// handleTicket routes the ticket sub-paths:
//
//	POST /ticket/{id}/answer
//	POST /ticket/{id}/ice/sender
//	GET  /ticket/{id}/ice/viewer
func (f *fakeSignalingServer) handleTicket(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ticket/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	ticketID := parts[0]
	sub := strings.Join(parts[1:], "/")

	switch {
	case r.Method == http.MethodPost && sub == "answer":
		f.postAnswer(w, r, ticketID)
	case r.Method == http.MethodPost && sub == "ice/sender":
		f.postSenderICE(w, r, ticketID)
	case r.Method == http.MethodGet && sub == "ice/viewer":
		f.getViewerICE(w, r, ticketID)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeSignalingServer) postAnswer(w http.ResponseWriter, r *http.Request, ticketID string) {
	var body struct {
		Answer sdpPayload `json:"answer"`
		Secret string     `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.answers[ticketID] = body.Answer
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fakeSignalingServer) postSenderICE(w http.ResponseWriter, r *http.Request, ticketID string) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Candidates []map[string]any `json:"candidates"`
		Secret     string           `json:"secret"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.senderICE[ticketID] = append(f.senderICE[ticketID], body.Candidates...)
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fakeSignalingServer) getViewerICE(w http.ResponseWriter, r *http.Request, ticketID string) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	f.mu.Lock()
	current := f.viewerICESeq[ticketID]
	all := f.viewerICE[ticketID]
	// Filter to candidates after `since`. We index by position in the
	// slice; candidates aren't removed, so `since` is a monotonic
	// offset matching the slice length.
	var delivered []map[string]any
	if since < len(all) {
		delivered = all[since:]
		current = len(all)
	} else {
		delivered = nil
	}
	f.viewerICESeq[ticketID] = current
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"candidates": delivered,
		"seq":        current,
	})
}
