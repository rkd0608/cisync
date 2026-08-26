package api

import (
	"context"

	"cisync.dev/cisync/github-connector/internal/checks"
)

// recordingPublisher captures payloads so tests can assert exactly what the
// dry-run path would render, without touching GitHub.
type recordingPublisher struct {
	payloads []checks.CheckPayload
	repos    []string
}

func (p *recordingPublisher) Publish(_ context.Context, repo string, payload checks.CheckPayload) (int64, error) {
	p.repos = append(p.repos, repo)
	p.payloads = append(p.payloads, payload)
	return 0, nil
}
