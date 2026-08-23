package execute

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sauron.dev/sauron/runner-fleet/internal/domain"
	"sauron.dev/sauron/runner-fleet/internal/providers"
	fstore "sauron.dev/sauron/runner-fleet/internal/store"
)

// failingProvider simulates an unavailable substrate (docker daemon down…).
type failingProvider struct{}

func (failingProvider) Submit(context.Context, domain.Job) (domain.Handle, error) {
	return nil, errors.New("provider unavailable: connection refused")
}
func (failingProvider) Cancel(domain.Handle) error { return nil }
func (failingProvider) Poll(domain.Handle) (domain.PollState, domain.Outcome) {
	return domain.PollDone, domain.Outcome{}
}

// scriptedProvider lets tests hold a job mid-flight and inject outcomes.
type scriptedProvider struct {
	mu       sync.Mutex
	started  chan string
	release  map[string]domain.Outcome
	cancels  []string
	handleOf map[string]*scriptedHandle
}

type scriptedHandle struct{ runID string }

func newScriptedProvider() *scriptedProvider {
	return &scriptedProvider{
		started:  make(chan string, 16),
		release:  make(map[string]domain.Outcome),
		handleOf: make(map[string]*scriptedHandle),
	}
}

func (p *scriptedProvider) Submit(_ context.Context, job domain.Job) (domain.Handle, error) {
	p.mu.Lock()
	h := &scriptedHandle{runID: job.RunID}
	p.handleOf[job.RunID] = h
	p.mu.Unlock()
	p.started <- job.RunID
	return h, nil
}

func (p *scriptedProvider) Cancel(handle domain.Handle) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancels = append(p.cancels, handle.(*scriptedHandle).runID)
	return nil
}

func (p *scriptedProvider) Poll(handle domain.Handle) (domain.PollState, domain.Outcome) {
	h := handle.(*scriptedHandle)
	p.mu.Lock()
	outcome, ok := p.release[h.runID]
	p.mu.Unlock()
	if !ok {
		return domain.PollRunning, domain.Outcome{}
	}
	if outcome.Logs == nil {
		outcome.Logs = []byte("scripted logs\n")
	}
	outcome.Artifacts = []domain.Artifact{{
		Name:      "report.json",
		Digest:    providers.DigestOf(outcome.Logs),
		SizeBytes: int64(len(outcome.Logs)),
	}}
	return domain.PollDone, outcome
}

func (p *scriptedProvider) releaseWith(runID string, o domain.Outcome) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.release[runID] = o
}

func newTestStore(t *testing.T) *fstore.MemoryStore {
	t.Helper()
	return fstore.NewMemoryStore(time.Now)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
