package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/go-github/v66/github"
)

// Publisher publishes check payloads in either direction of the lifecycle:
// Create for the first phase sighting, Update to walk the SAME run forward
// (plan §4.1: one check run per candidate revision).
type Publisher interface {
	Create(ctx context.Context, repo string, payload CheckPayload) (checkRunID int64, err error)
	Update(ctx context.Context, repo string, checkRunID int64, payload CheckPayload) error
}

// DryRunPublisher writes deterministic lines describing the exact API effect
// that would happen. Output goes through an injected io.Writer so tests can
// assert byte-stable goldens; production wires the redacted stdout sink.
type DryRunPublisher struct {
	w io.Writer
}

// NewDryRunPublisher builds the logging publisher.
func NewDryRunPublisher(w io.Writer) *DryRunPublisher {
	return &DryRunPublisher{w: w}
}

// Create implements Publisher by logging the would-be create call.
func (p *DryRunPublisher) Create(_ context.Context, repo string, payload CheckPayload) (int64, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return 0, fmt.Errorf("checks: render dry-run payload: %w", err)
	}
	// Payloads are check bodies only — no secrets by construction (plan §2.1).
	fmt.Fprintf(p.w, "DRYRUN create repo=%s payload=%s\n", repo, raw)
	return 0, nil
}

// Update implements Publisher by logging the would-be update call.
func (p *DryRunPublisher) Update(_ context.Context, repo string, checkRunID int64, payload CheckPayload) error {
	raw, err := marshalPayload(payload)
	if err != nil {
		return fmt.Errorf("checks: render dry-run payload: %w", err)
	}
	fmt.Fprintf(p.w, "DRYRUN update repo=%s check_run_id=%d payload=%s\n", repo, checkRunID, raw)
	return nil
}

func marshalPayload(payload CheckPayload) ([]byte, error) {
	return json.Marshal(payload)
}

// LivePublisher drives real check runs through go-github using a per-
// installation client supplied by the ghauth registry.
type LivePublisher struct {
	client *github.Client
	logger *slog.Logger
}

// NewLivePublisher builds a publisher on top of a ready-to-use client.
func NewLivePublisher(client *github.Client, logger *slog.Logger) *LivePublisher {
	return &LivePublisher{client: client, logger: logger}
}

// Create implements Publisher via Checks.CreateCheckRun. Conclusion and
// CompletedAt ride only completed payloads — GitHub rejects conclusions on
// non-terminal runs.
func (p *LivePublisher) Create(ctx context.Context, repo string, payload CheckPayload) (int64, error) {
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return 0, err
	}
	opts := github.CreateCheckRunOptions{
		Name:       payload.Name,
		HeadSHA:    payload.HeadSHA,
		Status:     github.String(payload.Status),
		DetailsURL: github.String(payload.DetailsURL),
		ExternalID: github.String(payload.ExternalID),
		Output:     outputFor(payload),
	}
	if payload.Conclusion != "" {
		opts.Conclusion = github.String(payload.Conclusion)
	}
	if payload.CompletedAt != nil {
		ts := github.Timestamp{Time: *payload.CompletedAt}
		opts.CompletedAt = &ts
	}
	run, _, err := p.client.Checks.CreateCheckRun(ctx, owner, name, opts)
	if err != nil {
		return 0, fmt.Errorf("checks: create check run for %s@%s: %w", repo, payload.HeadSHA, err)
	}
	p.logPublished(repo, run.GetID(), payload)
	return run.GetID(), nil
}

// Update implements Publisher via Checks.UpdateCheckRun.
func (p *LivePublisher) Update(ctx context.Context, repo string, checkRunID int64, payload CheckPayload) error {
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return err
	}
	opts := github.UpdateCheckRunOptions{
		Name:       payload.Name,
		Status:     github.String(payload.Status),
		DetailsURL: github.String(payload.DetailsURL),
		Output:     outputFor(payload),
	}
	if payload.Conclusion != "" {
		opts.Conclusion = github.String(payload.Conclusion)
	}
	if payload.CompletedAt != nil {
		ts := github.Timestamp{Time: *payload.CompletedAt}
		opts.CompletedAt = &ts
	}
	if _, _, err := p.client.Checks.UpdateCheckRun(ctx, owner, name, checkRunID, opts); err != nil {
		return fmt.Errorf("checks: update check run %d for %s@%s: %w", checkRunID, repo, payload.HeadSHA, err)
	}
	p.logPublished(repo, checkRunID, payload)
	return nil
}

func outputFor(payload CheckPayload) *github.CheckRunOutput {
	out := &github.CheckRunOutput{
		Title:   github.String(CheckName),
		Summary: github.String(payload.Summary),
	}
	for _, a := range payload.Annotations {
		ga := &github.CheckRunAnnotation{
			Message: github.String(a.Message),
			Title:   github.String(a.Title),
		}
		if a.Path != "" {
			ga.Path = github.String(a.Path)
			// WHY EndLine=StartLine: GitHub requires end_line >= start_line;
			// single-line findings satisfy it without inventing ranges.
			ga.StartLine = github.Int(a.StartLine)
			ga.EndLine = github.Int(a.EndLine)
		}
		out.Annotations = append(out.Annotations, ga)
	}
	return out
}

func (p *LivePublisher) logPublished(repo string, checkRunID int64, payload CheckPayload) {
	p.logger.Info("published github check",
		slog.String("repo", repo),
		slog.Int64("check_run_id", checkRunID),
		slog.String("status", payload.Status),
		slog.String("conclusion", payload.Conclusion))
}

// SplitRepo splits "owner/name" exactly once; anything else is invalid.
func SplitRepo(repo string) (owner, name string, err error) {
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
