package domain

import "context"

// PollState enumerates provider execution states between Submit and Outcome.
type PollState int

// Poll states.
const (
	PollRunning PollState = iota
	PollDone
)

// Provider abstracts an execution substrate. Submit launches the job, Poll is
// non-blocking, Cancel is best-effort (B5: sim enforces safety by construction;
// docker is NOT-FOR-PRODUCTION until THREAT_MODEL graduation).
type Provider interface {
	Submit(ctx context.Context, job Job) (Handle, error)
	Cancel(handle Handle) error
	Poll(handle Handle) (PollState, Outcome)
}

// Handle is an opaque provider-side reference to a submitted job.
type Handle interface{}
