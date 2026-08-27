package report

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/go-github/v66/github"

	"cisync.dev/cisync/github-connector/internal/checks"
	"cisync.dev/cisync/github-connector/internal/domain"
	"cisync.dev/cisync/github-connector/internal/obs"
)

// InstallationResolver mirrors emit.InstallationResolver structurally so the
// PG store satisfies both without adapters.
type InstallationResolver interface {
	ResolveInstallation(ctx context.Context, owner string, repo string) (installationID int64, err error)
}

// ClientRegistry is the slice of ghauth.Registry the poster needs.
type ClientRegistry interface {
	Client(installationID int64) (*github.Client, error)
}

const (
	metricPosts    = "cisync_report_posts_total"
	metricFailures = "cisync_report_post_failures_total"
	postsHelp      = "Sticky verification-report comments written (post or in-place patch)"
	failuresHelp   = "Sticky verification-report pushes that failed at a GitHub stage"
)

// Poster owns ONE sticky verification comment per (repo, pull_number): it
// finds the prior App-authored comment by MarkerLine prefix, PATCHes it in
// place when present, else creates it (internal-protocols §4.1 W6).
type Poster struct {
	resolver   InstallationResolver
	registry   ClientRegistry
	detailsURL string
	logger     *slog.Logger
	metrics    *obs.Metrics
}

// NewPoster wires the sticky-comment upserter.
func NewPoster(resolver InstallationResolver, registry ClientRegistry,
	detailsURL string, metrics *obs.Metrics, logger *slog.Logger) *Poster {
	return &Poster{resolver: resolver, registry: registry,
		detailsURL: detailsURL, metrics: metrics, logger: logger}
}

// Post renders env into the comment body and upserts the sticky comment on
// env.PRNumber. Errors are returned typed/staged for logging by the caller;
// they NEVER flip the decision push response (caller treats as best-effort).
func (p *Poster) Post(ctx context.Context, env *domain.DecisionEnvelope) error {
	body, err := RenderComment(env, p.detailsURL)
	if err != nil {
		return p.fail("render", fmt.Errorf("report: %w", err))
	}
	owner, name, err := checks.SplitRepo(env.Repo)
	if err != nil {
		return p.fail("resolve", fmt.Errorf("report: %w", err))
	}
	instID, err := p.resolver.ResolveInstallation(ctx, owner, name)
	if err != nil {
		return p.fail("resolve", fmt.Errorf("report: resolve installation for %s: %w", env.Repo, err))
	}
	client, err := p.registry.Client(instID)
	if err != nil {
		return p.fail("resolve", fmt.Errorf("report: client for installation %d: %w", instID, err))
	}

	existingID, found, err := findStickyComment(ctx, client.Issues, owner, name, env.PRNumber)
	if err != nil {
		return p.fail("list", fmt.Errorf("report: list comments %s#%d: %w", env.Repo, env.PRNumber, err))
	}
	comment := &github.IssueComment{Body: github.String(body)}
	if found {
		if _, _, err := client.Issues.EditComment(ctx, owner, name, existingID, comment); err != nil {
			return p.fail("edit", fmt.Errorf("report: edit comment %d on %s#%d: %w",
				existingID, env.Repo, env.PRNumber, err))
		}
		p.count("update")
		p.logger.Info("sticky verification comment updated in place",
			slog.String("repo", env.Repo), slog.Int("pr", env.PRNumber), slog.Int64("comment_id", existingID))
		return nil
	}
	created, _, err := client.Issues.CreateComment(ctx, owner, name, env.PRNumber, comment)
	if err != nil {
		return p.fail("create", fmt.Errorf("report: create comment on %s#%d: %w", env.Repo, env.PRNumber, err))
	}
	p.count("post")
	p.logger.Info("sticky verification comment created",
		slog.String("repo", env.Repo), slog.Int("pr", env.PRNumber),
		slog.Int64("comment_id", created.GetID()), slog.String("decision_id", env.DecisionID))
	return nil
}

// findStickyComment scans paginated issue comments for OUR sticky post. The
// prefix match means only comments whose FIRST line is MarkerLine qualify —
// quoted/hijacked occurrences deeper in foreign bodies never match.
func findStickyComment(ctx context.Context, svc IssuesServiceAPI,
	owner, repo string, number int) (int64, bool, error) {
	for page := 1; ; page++ {
		comments, _, err := svc.ListComments(ctx, owner, repo, number,
			&github.IssueListCommentsOptions{ListOptions: github.ListOptions{
				Page: page, PerPage: 100}})
		if err != nil {
			return 0, false, err
		}
		for _, c := range comments {
			if startsWithMarker(c.GetBody()) {
				return c.GetID(), true, nil
			}
		}
		if len(comments) < 100 {
			return 0, false, nil
		}
	}
}

// IssuesServiceAPI narrows go-github to the exact Methods used, keeping the
// poster honest and easily stubbed beyond the httptest loopback.
type IssuesServiceAPI interface {
	ListComments(ctx context.Context, owner, repo string, number int,
		opts *github.IssueListCommentsOptions) ([]*github.IssueComment, *github.Response, error)
	CreateComment(ctx context.Context, owner, repo string, number int,
		body *github.IssueComment) (*github.IssueComment, *github.Response, error)
	EditComment(ctx context.Context, owner, repo string, commentID int64,
		body *github.IssueComment) (*github.IssueComment, *github.Response, error)
}

func startsWithMarker(body string) bool {
	return strings.HasPrefix(body, MarkerLine+"\n")
}

func (p *Poster) fail(stage string, err error) error {
	if p.metrics != nil {
		p.metrics.CounterInc(metricFailures, failuresHelp, "stage", stage)
	}
	return err
}

func (p *Poster) count(outcome string) {
	if p.metrics != nil {
		p.metrics.CounterInc(metricPosts, postsHelp, "outcome", outcome)
	}
}
