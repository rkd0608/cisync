package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/go-github/v66/github"
)

// Publisher turns a rendered check payload into either a live GitHub API
// call or a dry-run log line.
type Publisher interface {
	Publish(ctx context.Context, repo string, payload CheckPayload) (checkRunID int64, err error)
}

// DryRunPublisher logs the would-be check payload instead of calling GitHub;
// it keeps compose environments green without credentials.
type DryRunPublisher struct {
	logger *slog.Logger
}

// NewDryRunPublisher builds the logging publisher.
func NewDryRunPublisher(logger *slog.Logger) *DryRunPublisher {
	return &DryRunPublisher{logger: logger}
}

// Publish implements Publisher by logging the exact payload that would be
// sent to POST /repos/{repo}/check-runs.
func (p *DryRunPublisher) Publish(_ context.Context, repo string, payload CheckPayload) (int64, error) {
	raw, err := json.Marshal(map[string]any{"repo": repo, "check_run": payload})
	if err != nil {
		return 0, fmt.Errorf("checks: render dry-run payload: %w", err)
	}
	p.logger.Info("dry-run: would publish github check",
		slog.String("payload", string(raw)))
	return 0, nil
}

// LivePublisher creates real check runs through go-github using an
// installation token minted from the configured GitHub App.
type LivePublisher struct {
	client *github.Client
	logger *slog.Logger
}

// NewLivePublisher builds a publisher on top of a ready-to-use client.
func NewLivePublisher(client *github.Client, logger *slog.Logger) *LivePublisher {
	return &LivePublisher{client: client, logger: logger}
}

// Publish implements Publisher via Checks.CreateCheckRun.
func (p *LivePublisher) Publish(ctx context.Context, repo string, payload CheckPayload) (int64, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return 0, err
	}
	details := payload.DetailsURL
	external := payload.ExternalID
	summary := payload.Summary
	conclusion := payload.Conclusion
	status := payload.Status
	run, _, err := p.client.Checks.CreateCheckRun(ctx, owner, name, github.CreateCheckRunOptions{
		Name:       payload.Name,
		HeadSHA:    payload.HeadSHA,
		Status:     github.String(status),
		Conclusion: github.String(conclusion),
		DetailsURL: &details,
		ExternalID: &external,
		Output: &github.CheckRunOutput{
			Title:   github.String(CheckName),
			Summary: &summary,
		},
	})
	if err != nil {
		return 0, fmt.Errorf("checks: create check run for %s@%s: %w", repo, payload.HeadSHA, err)
	}
	p.logger.Info("published github check",
		slog.String("repo", repo), slog.Int64("check_run_id", run.GetID()))
	return run.GetID(), nil
}

func splitRepo(repo string) (owner, name string, err error) {
	var slash int
	for i, c := range repo {
		if c == '/' {
			slash++
			if slash == 1 {
				owner, name = repo[:i], repo[i+1:]
			}
		}
	}
	if owner == "" || name == "" || slash != 1 {
		return "", "", fmt.Errorf("checks: repo must be owner/name, got %q", repo)
	}
	return owner, name, nil
}
