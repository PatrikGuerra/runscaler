package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ysya/runscaler/internal/health"
)

// Status is what GET /api/status returns.
type Status struct {
	Reachable bool                   `json:"reachable"`
	FetchedAt time.Time              `json:"fetchedAt"`
	Error     string                 `json:"error,omitempty"`
	Health    *health.HealthResponse `json:"health,omitempty"`
}

// Poller reads runscaler's /healthz and turns changes into history events.
type Poller struct {
	url    string
	client *http.Client
	store  *Store
	logger *slog.Logger

	mu     sync.RWMutex
	last   Status
	polled bool
}

func NewPoller(url string, store *Store, logger *slog.Logger) *Poller {
	return &Poller{url: url, client: &http.Client{Timeout: 3 * time.Second}, store: store, logger: logger}
}

func (p *Poller) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.last
}

func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		p.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Poller) poll(ctx context.Context) {
	cur := Status{FetchedAt: time.Now().UTC()}
	if h, err := p.fetch(ctx); err != nil {
		cur.Error = err.Error()
	} else {
		cur.Reachable, cur.Health = true, h
	}

	p.mu.Lock()
	prev, first := p.last, !p.polled
	p.last, p.polled = cur, true
	p.mu.Unlock()

	if first {
		return // baseline only: a panel restart is not a runscaler event
	}
	for _, e := range diffEvents(prev, cur) {
		if err := p.store.AddEvent(ctx, e.kind, e.detail); err != nil {
			p.logger.Warn("panel: cannot record event", "kind", e.kind, "err", err)
		}
	}
}

func (p *Poller) fetch(ctx context.Context) (*health.HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("healthz returned HTTP %d", resp.StatusCode)
	}
	var h health.HealthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&h); err != nil {
		return nil, fmt.Errorf("decode healthz: %w", err)
	}
	return &h, nil
}

type eventSpec struct {
	kind   string
	detail map[string]any
}

// diffEvents compares two consecutive polls.
func diffEvents(prev, cur Status) []eventSpec {
	var out []eventSpec
	switch {
	case prev.Reachable && !cur.Reachable:
		return []eventSpec{{"runscaler_down", map[string]any{"error": cur.Error}}}
	case !prev.Reachable && cur.Reachable:
		out = append(out, eventSpec{"runscaler_up", map[string]any{"version": cur.Health.Version}})
	case prev.Reachable && cur.Reachable && restarted(prev.Health.Uptime, cur.Health.Uptime):
		// Down for less than one poll interval: still a restart.
		out = append(out, eventSpec{"runscaler_up", map[string]any{"version": cur.Health.Version, "restart": true}})
	}
	if !cur.Reachable {
		return out
	}

	before := map[string]health.ScaleSetStatus{}
	if prev.Reachable {
		for _, s := range prev.Health.ScaleSets {
			before[s.Name] = s
		}
	}
	for _, s := range cur.Health.ScaleSets {
		old, seen := before[s.Name]
		if s.Ready && (!seen || !old.Ready) {
			out = append(out, eventSpec{"listener_connected", map[string]any{"scaleSet": s.Name}})
		}
		if !s.Ready && seen && old.Ready {
			out = append(out, eventSpec{"listener_disconnected", map[string]any{"scaleSet": s.Name, "error": s.LastError}})
		}
		oldStarted, oldCompleted, oldAssigned := jobCounts(old.Metrics)
		started, completed, assigned := jobCounts(s.Metrics)
		// runscaler v0.23 reports the runner as idle while it runs a job and counts
		// jobs_started only when the job ends; assigned_jobs is the live signal.
		if n := assigned - oldAssigned; n > 0 {
			out = append(out, eventSpec{"job_assigned", map[string]any{"scaleSet": s.Name, "count": n}})
		}
		if n := started - oldStarted; n > 0 {
			out = append(out, eventSpec{"job_started", map[string]any{"scaleSet": s.Name, "count": n}})
		}
		if n := completed - oldCompleted; n > 0 {
			out = append(out, eventSpec{"job_completed", map[string]any{"scaleSet": s.Name, "count": n}})
		}
		var oldFail int64
		if old.Runtime != nil {
			oldFail = old.Runtime.StartFailures
		}
		if s.Runtime != nil && s.Runtime.StartFailures > oldFail {
			out = append(out, eventSpec{"startup_failed", map[string]any{"scaleSet": s.Name, "reason": s.Runtime.FailureReason}})
		}
	}
	return out
}

func jobCounts(m *health.MetricsStatus) (started, completed, assigned int64) {
	if m == nil {
		return 0, 0, 0
	}
	return m.JobsStarted, m.JobsCompleted, int64(m.AssignedJobs)
}

// restarted reports a smaller uptime than the previous poll.
func restarted(prev, cur string) bool {
	p, err1 := time.ParseDuration(prev)
	c, err2 := time.ParseDuration(cur)
	return err1 == nil && err2 == nil && c < p
}
