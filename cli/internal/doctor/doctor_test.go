package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPlatformSupport_RecognizesCurrentPlatform(t *testing.T) {
	cfg := Config{}.defaults()
	res := cfg.checkPlatformSupport(context.Background())
	if res.Status != StatusPass {
		t.Errorf("expected PASS for current platform; got %v (%s)",
			res.Status, res.Detail)
	}
}

func TestDNS_LocalhostResolves(t *testing.T) {
	cfg := Config{ServerURL: "https://localhost"}.defaults()
	res := cfg.checkDNS(context.Background())
	if res.Status != StatusPass {
		t.Errorf("expected PASS for localhost; got %v: %s", res.Status, res.Detail)
	}
}

func TestDNS_BadHostFails(t *testing.T) {
	cfg := Config{ServerURL: "https://no.such.host.example.invalid"}.defaults()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res := cfg.checkDNS(ctx)
	if res.Status != StatusFail {
		t.Errorf("expected FAIL for bogus host; got %v: %s", res.Status, res.Detail)
	}
}

func TestHealthz_OKFromFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	cfg := Config{ServerURL: srv.URL, HTTPClient: srv.Client()}.defaults()
	res := cfg.checkHealthz(context.Background())
	if res.Status != StatusPass {
		t.Errorf("expected PASS; got %v: %s", res.Status, res.Detail)
	}
}

func TestHealthz_NonOKFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	cfg := Config{ServerURL: srv.URL, HTTPClient: srv.Client()}.defaults()
	res := cfg.checkHealthz(context.Background())
	if res.Status != StatusFail {
		t.Errorf("expected FAIL; got %v: %s", res.Status, res.Detail)
	}
}

func TestProxyEnv_NoProxyDefaultsToPass(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	cfg := Config{}.defaults()
	res := cfg.checkProxyEnv(context.Background())
	if res.Status != StatusPass {
		t.Errorf("expected PASS when no proxy; got %v: %s", res.Status, res.Detail)
	}
	if !strings.Contains(res.Detail, "no proxy") {
		t.Errorf("expected 'no proxy' message; got %q", res.Detail)
	}
}

func TestProxyEnv_RedactsAuth(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://alice:supersecret@proxy.corp:8080")
	t.Setenv("HTTP_PROXY", "")
	cfg := Config{}.defaults()
	res := cfg.checkProxyEnv(context.Background())
	if strings.Contains(res.Detail, "supersecret") {
		t.Errorf("proxy password leaked into detail: %q", res.Detail)
	}
	if !strings.Contains(res.Detail, "<redacted>") {
		t.Errorf("expected '<redacted>' marker; got: %q", res.Detail)
	}
}

func TestRunner_AggregatesWorstStatus(t *testing.T) {
	checks := []Check{
		{Name: "passing", Run: func(_ context.Context) Result {
			return Result{Status: StatusPass}
		}},
		{Name: "warning", Run: func(_ context.Context) Result {
			return Result{Status: StatusWarn}
		}},
		{Name: "passing-2", Run: func(_ context.Context) Result {
			return Result{Status: StatusPass}
		}},
	}
	var buf strings.Builder
	r := &Runner{Out: &buf, NoColor: true}
	worst := r.Run(context.Background(), checks)
	if worst != StatusWarn {
		t.Errorf("expected worst=WARN; got %v", worst)
	}
}

func TestRunner_FAILDominatesWARN(t *testing.T) {
	checks := []Check{
		{Name: "warning", Run: func(_ context.Context) Result {
			return Result{Status: StatusWarn}
		}},
		{Name: "failing", Run: func(_ context.Context) Result {
			return Result{Status: StatusFail, Hint: "fix me"}
		}},
	}
	var buf strings.Builder
	r := &Runner{Out: &buf, NoColor: true}
	worst := r.Run(context.Background(), checks)
	if worst != StatusFail {
		t.Errorf("expected FAIL; got %v", worst)
	}
	if !strings.Contains(buf.String(), "fix me") {
		t.Errorf("expected hint in output; got: %q", buf.String())
	}
}

func TestRedactProxyAuth(t *testing.T) {
	cases := map[string]string{
		"http://alice:pw@host:8080":          "http://<redacted>@host:8080",
		"https://user:pw@proxy:443":          "https://<redacted>@proxy:443",
		"http://proxy:8080":                  "http://proxy:8080", // no auth
		"":                                   "",
	}
	for in, want := range cases {
		got := redactProxyAuth(in)
		if got != want {
			t.Errorf("redactProxyAuth(%q): got %q want %q", in, got, want)
		}
	}
}
