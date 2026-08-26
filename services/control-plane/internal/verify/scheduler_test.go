package verify

import (
	"context"
	"sync"
	"testing"
	"time"

	"sauron.dev/sauron/control-plane/internal/audit"
	"sauron.dev/sauron/control-plane/internal/domain"
)

// fakeAuditSink captures security-audit rows emitted by the scheduler.
type fakeAuditSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (f *fakeAuditSink) InsertSecurityAudit(_ context.Context, ev audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeAuditSink) count(kind audit.Kind) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, ev := range f.events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func TestSchedulerRunOnceOKNotifiesWithoutAuditRow(t *testing.T) {
	sink := &fakeAuditSink{}
	var statuses []string
	s := NewScheduler(
		func(context.Context) (*Report, error) { return &Report{Entries: 7, Checkpoints: 1}, nil },
		time.Second, sink, "org_test",
		func(status string) { statuses = append(statuses, status) },
		func(string) { t.Fatal("no audit metric on success") },
	)
	s.RunOnce(context.Background())
	if len(statuses) != 1 || statuses[0] != "ok" {
		t.Fatalf("statuses = %v, want [ok]", statuses)
	}
	if got := sink.count(audit.KindChainVerifyFailure); got != 0 {
		t.Fatalf("audit rows on healthy chain = %d, want 0", got)
	}
}

func TestSchedulerRunOnceFailureAuditsExactlyOnce(t *testing.T) {
	sink := &fakeAuditSink{}
	var statuses []string
	var audited []string
	broken := domain.ErrChainBroken
	s := NewScheduler(
		func(context.Context) (*Report, error) {
			return &Report{}, wrappedError{broken}
		},
		time.Second, sink, "org_test",
		func(status string) { statuses = append(statuses, status) },
		func(kind string) { audited = append(audited, kind) },
	)
	s.RunOnce(context.Background())

	if len(statuses) != 1 || statuses[0] != "fail" {
		t.Fatalf("statuses = %v, want [fail]", statuses)
	}
	if got := sink.count(audit.KindChainVerifyFailure); got != 1 {
		t.Fatalf("audit rows = %d, want exactly 1", got)
	}
	if len(audited) != 1 || audited[0] != string(audit.KindChainVerifyFailure) {
		t.Fatalf("audit notify = %v, want exactly one %s", audited, audit.KindChainVerifyFailure)
	}
}

func TestSchedulerDisabledWhenIntervalNonPositive(t *testing.T) {
	sink := &fakeAuditSink{}
	calls := 0
	s := NewScheduler(
		func(context.Context) (*Report, error) { calls++; return &Report{}, nil },
		0, sink, "org_test", nil, nil,
	)
	done := make(chan struct{})
	go func() {
		s.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run must exit immediately when interval is 0")
	}
	if calls != 0 {
		t.Fatalf("verify ran %d times, want 0 (loop disabled)", calls)
	}
}

func TestSchedulerTicksUntilContextCancelled(t *testing.T) {
	sink := &fakeAuditSink{}
	calls := make(chan struct{}, 8)
	s := NewScheduler(
		func(context.Context) (*Report, error) { calls <- struct{}{}; return &Report{}, nil },
		5*time.Millisecond, sink, "org_test", nil, nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	<-calls
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after ctx cancel")
	}
}

type wrappedError struct{ err error }

func (w wrappedError) Error() string { return w.err.Error() }
func (w wrappedError) Unwrap() error { return w.err }
