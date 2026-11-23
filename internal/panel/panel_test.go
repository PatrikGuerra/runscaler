package panel

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysya/runscaler/internal/health"
	"github.com/ysya/runscaler/internal/metrics"
)

func runtimeWithFailures(n int64) *metrics.RuntimeStatus {
	return &metrics.RuntimeStatus{StartFailures: n}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreEventsAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, k := range []string{"job_started", "job_completed", "job_started"} {
		if err := s.AddEvent(ctx, k, map[string]any{"n": 1}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	// History survives a restart.
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	all, _ := s.ListEvents(ctx, "", 0, 10)
	if len(all) != 3 || all[0].ID <= all[1].ID {
		t.Fatalf("events = %+v", all)
	}
	started, _ := s.ListEvents(ctx, "job_started", 0, 10)
	if len(started) != 2 {
		t.Fatalf("kind filter: %d events", len(started))
	}
	page, _ := s.ListEvents(ctx, "", all[0].ID, 10)
	if len(page) != 2 {
		t.Fatalf("before filter: %d events", len(page))
	}
	s.now = func() time.Time { return time.Now().Add(100 * 24 * time.Hour) }
	if n, _ := s.PruneEvents(ctx, s.now().Add(-90*24*time.Hour)); n != 3 {
		t.Fatalf("pruned %d", n)
	}
}

func TestAuthSetupLoginLockout(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a, token, err := NewAuth(ctx, s)
	if err != nil || token == "" {
		t.Fatalf("setup token = %q, err = %v", token, err)
	}
	if _, err := a.Setup(ctx, "wrong", "a-long-password"); err != errBadSetupToken {
		t.Fatalf("bad token: %v", err)
	}
	if _, err := a.Setup(ctx, token, "short"); err != errPasswordTooWeak {
		t.Fatalf("weak password: %v", err)
	}
	if _, err := a.Setup(ctx, token, "a-long-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Setup(ctx, token, "another-password"); err != errBadSetupToken {
		t.Fatalf("second setup: %v", err)
	}
	// A restarted panel with an admin prints no token.
	if _, again, _ := NewAuth(ctx, s); again != "" {
		t.Fatal("setup token issued although an admin exists")
	}

	if _, err := a.Login(ctx, "a-long-password"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a.now = func() time.Time { return now }
	for i := 0; i < maxLoginFailures; i++ {
		if _, err := a.Login(ctx, "nope"); err != errBadPassword {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := a.Login(ctx, "a-long-password"); err != errLocked {
		t.Fatalf("expected lockout, got %v", err)
	}
	now = now.Add(loginLockout + time.Second)
	if _, err := a.Login(ctx, "a-long-password"); err != nil {
		t.Fatalf("after lockout: %v", err)
	}
}

func TestDiffEvents(t *testing.T) {
	up := func(uptime string, ready bool, started, completed, failures int64, assigned ...int) Status {
		m := &health.MetricsStatus{JobsStarted: started, JobsCompleted: completed}
		if len(assigned) > 0 {
			m.AssignedJobs = assigned[0]
		}
		return Status{Reachable: true, Health: &health.HealthResponse{Version: "v", Uptime: uptime, ScaleSets: []health.ScaleSetStatus{{
			Name: "orangepi3", Ready: ready,
			Metrics: m,
			Runtime: runtimeWithFailures(failures),
		}}}}
	}
	down := Status{Error: "connection refused"}
	kinds := func(es []eventSpec) string {
		var k []string
		for _, e := range es {
			k = append(k, e.kind)
		}
		return strings.Join(k, ",")
	}
	cases := []struct {
		name      string
		prev, cur Status
		want      string
	}{
		{"steady", up("1m", true, 1, 1, 0), up("1m5s", true, 1, 1, 0), ""},
		{"job assigned", up("1m", true, 1, 1, 0), up("1m5s", true, 1, 1, 0, 1), "job_assigned"},
		{"job lifecycle", up("1m", true, 1, 1, 0), up("1m5s", true, 2, 1, 0), "job_started"},
		{"job done", up("1m", true, 2, 1, 0), up("1m5s", true, 2, 2, 0), "job_completed"},
		{"down", up("1m", true, 1, 1, 0), down, "runscaler_down"},
		{"up", down, up("3s", true, 0, 0, 0), "runscaler_up,listener_connected"},
		{"quick restart", up("1h", true, 5, 5, 0), up("4s", true, 0, 0, 0), "runscaler_up"},
		{"listener lost", up("1m", true, 1, 1, 0), up("1m5s", false, 1, 1, 0), "listener_disconnected"},
		{"startup failure", up("1m", true, 1, 1, 0), up("1m5s", true, 1, 1, 1), "startup_failed"},
	}
	for _, c := range cases {
		if got := kinds(diffEvents(c.prev, c.cur)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.log")
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("line ")
		if i%10 == 0 {
			b.WriteString("ERROR ")
		}
		b.WriteString(time.Duration(i).String() + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, truncated, _ := tailLines(path, 5, "")
	if len(lines) != 5 || !truncated || !strings.HasSuffix(lines[4], "49ns") {
		t.Fatalf("tail = %v truncated=%v", lines, truncated)
	}
	lines, _, _ = tailLines(path, 100, "error")
	if len(lines) != 5 {
		t.Fatalf("filter = %v", lines)
	}
	lines, truncated, err := tailLines(filepath.Join(t.TempDir(), "missing.log"), 10, "")
	if err != nil || len(lines) != 0 || truncated {
		t.Fatalf("missing log: %v %v %v", lines, truncated, err)
	}
}

// TestServerFlow drives the API like the browser does.
func TestServerFlow(t *testing.T) {
	var jobs atomic.Int64
	fakeRunscaler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(health.HealthResponse{Status: "ok", Version: "test", Uptime: "1m",
			ScaleSets: []health.ScaleSetStatus{{Name: "orangepi3", Ready: true, Busy: 1,
				Metrics: &health.MetricsStatus{JobsStarted: jobs.Load()}}}})
	}))
	defer fakeRunscaler.Close()

	ctx := context.Background()
	store := newStore(t)
	auth, setupToken, _ := NewAuth(ctx, store)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := NewPoller(fakeRunscaler.URL, store, logger)
	exchange := t.TempDir()
	_ = os.MkdirAll(filepath.Join(exchange, "logs"), 0o700)
	_ = os.WriteFile(filepath.Join(exchange, "logs", "runner.log"), []byte("hello\nRunner started\n"), 0o600)
	srv := httptest.NewServer((&Server{ExchangeDir: exchange, Store: store, Auth: auth, Poller: poller, Logger: logger}).Handler())
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	call := func(method, path, body, csrf string) (*http.Response, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if csrf != "" {
			req.Header.Set(csrfHeader, csrf)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}

	if resp, _ := call("GET", "/api/status", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without session: %d", resp.StatusCode)
	}
	if resp, s := call("GET", "/api/auth/session", "", ""); resp.StatusCode != 200 || s["setupRequired"] != true {
		t.Fatalf("session before setup: %v", s)
	}
	if resp, _ := call("POST", "/api/auth/setup", `{"setupToken":"x","password":"a-long-password"}`, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("setup with bad token: %d", resp.StatusCode)
	}
	if resp, _ := call("POST", "/api/auth/setup", `{"setupToken":"`+setupToken+`","password":"a-long-password","admin":true}`, ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("setup with unknown field: %d", resp.StatusCode)
	}
	if resp, _ := call("POST", "/api/auth/setup", `{"setupToken":"`+setupToken+`","password":"a-long-password"}`, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("setup: %d", resp.StatusCode)
	}
	_, s := call("GET", "/api/auth/session", "", "")
	csrf, _ := s["csrfToken"].(string)
	if s["authenticated"] != true || csrf == "" {
		t.Fatalf("session after setup: %v", s)
	}

	poller.poll(ctx) // baseline
	jobs.Store(1)
	poller.poll(ctx) // job_started
	resp, st := call("GET", "/api/status", "", "")
	if resp.StatusCode != 200 || st["reachable"] != true {
		t.Fatalf("status: %d %v", resp.StatusCode, st)
	}
	if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing")
	}
	_, h := call("GET", "/api/history?kind=job_started", "", "")
	if evs, _ := h["events"].([]any); len(evs) != 1 {
		t.Fatalf("history: %v", h)
	}
	_, logs := call("GET", "/api/logs?q=runner", "", "")
	if ls, _ := logs["lines"].([]any); len(ls) != 1 {
		t.Fatalf("logs: %v", logs)
	}

	if resp, _ := call("POST", "/api/auth/logout", "", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF: %d", resp.StatusCode)
	}
	if resp, _ := call("POST", "/api/auth/logout", "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if resp, _ := call("GET", "/api/status", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status after logout: %d", resp.StatusCode)
	}
	if resp, _ := call("POST", "/api/auth/login", `{"password":"a-long-password"}`, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	if resp, _ := call("GET", "/api/unknown", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown api route: %d", resp.StatusCode)
	}
	if resp, _ := call("GET", "/assets/missing.js", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset: %d", resp.StatusCode)
	}
}
