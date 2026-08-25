// Package emit routes check writes to the right publication path: resolve
// repo → installation (FAIL-CLOSED), apply the write-budget gate, enqueue on
// exhaustion, and fall back to dry-run logging whenever live mode cannot be
// proven safe (plan §5.5.2/§6.3).
package emit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/ghauth"
	"sauron.dev/sauron/github-connector/internal/obs"
	"sauron.dev/sauron/github-connector/internal/queue"
	"sauron.dev/sauron/github-connector/internal/ratelimit"
)

// ErrUnknownInstallation reports a repo with no provable installation
// binding; the caller never guesses (cross-installation isolation §6.3).
var ErrUnknownInstallation = errors.New("emit: no installation binding for repo")

// InstallationResolver is the ResolveInstallation seam. The signature
// matches store.Store.ResolveInstallation exactly, so the PGStore satisfies
// it structurally — no adapter needed at wiring time. A nil resolver means
// every repo is unresolvable ⇒ dry-run for all writes.
type InstallationResolver interface {
	ResolveInstallation(ctx context.Context, owner string, repo string) (installationID int64, err error)
}

// Router owns the publication decision for every connector write.
type Router struct {
	dry      *checks.DryRunPublisher
	resolver InstallationResolver
	registry *ghauth.Registry
	gate     *ratelimit.Gate
	budget   *ratelimit.Budget
	pending  queue.Store // optional: nil ⇒ exhaustion propagates to the caller
	metrics  *obs.Metrics
	logger   *slog.Logger
}

// NewRouter wires the emit path. resolver/pending may be nil; registry must
// be nil only in dry-run deployments.
func NewRouter(dry *checks.DryRunPublisher, resolver InstallationResolver, registry *ghauth.Registry,
	gate *ratelimit.Gate, budget *ratelimit.Budget, pending queue.Store, metrics *obs.Metrics, logger *slog.Logger) *Router {
	return &Router{
		dry: dry, resolver: resolver, registry: registry, gate: gate,
		budget: budget, pending: pending, metrics: metrics, logger: logger,
	}
}

// Result reports how a write landed.
type Result struct {
	Queued     bool  // buffered in the outbox; will drain later
	CheckRunID int64 // 0 when queued or dry-run
	DryRun     bool
}

// Create publishes a first-sighting payload (queued phase or an immediate
// completed decision with no tracked run).
func (r *Router) Create(ctx context.Context, repo string, payload checks.CheckPayload) (Result, error) {
	return r.write(ctx, repo, 0, payload)
}

// Update walks an existing check run forward in place.
func (r *Router) Update(ctx context.Context, repo string, checkRunID int64, payload checks.CheckPayload) (Result, error) {
	if checkRunID <= 0 {
		// No tracked GitHub id (dry-run lineage or lost state): create is
		// the only meaningful effect; external_id keeps identity stable.
		return r.write(ctx, repo, 0, payload)
	}
	return r.write(ctx, repo, checkRunID, payload)
}

// PublishDirect writes WITHOUT re-acquiring budget tokens: it is the drain
// path for pending writes whose budget was already granted by the drainer.
// WHY: routing a drained write through Create/Update would consume a second
// token and could re-enqueue the same write forever.
func (r *Router) PublishDirect(ctx context.Context, repo string, checkRunID int64, payload checks.CheckPayload) (Result, error) {
	_, pub, live, err := r.resolvePublisher(ctx, repo)
	if err != nil {
		return Result{}, err
	}
	if !live {
		id, dryErr := r.dryWrite(repo, checkRunID, payload)
		return Result{DryRun: true, CheckRunID: id}, dryErr
	}
	if checkRunID > 0 {
		return Result{CheckRunID: checkRunID}, pub.Update(ctx, repo, checkRunID, payload)
	}
	id, err := pub.Create(ctx, repo, payload)
	return Result{CheckRunID: id}, err
}

type writeFunc func(pub checks.Publisher) (int64, error)

func (r *Router) write(ctx context.Context, repo string, checkRunID int64, payload checks.CheckPayload) (Result, error) {
	instID, pub, live, err := r.resolvePublisher(ctx, repo)
	if err != nil {
		return Result{}, err
	}
	if !live {
		id, dryErr := r.dryWrite(repo, checkRunID, payload)
		return Result{DryRun: true, CheckRunID: id}, dryErr
	}
	var published Result
	err = r.gate.Do(ctx, instID, func(gateCtx context.Context) error {
		var werr error
		if checkRunID > 0 {
			werr = pub.Update(gateCtx, repo, checkRunID, payload)
			published.CheckRunID = checkRunID
		} else {
			published.CheckRunID, werr = pub.Create(gateCtx, repo, payload)
		}
		return werr
	})
	if errors.Is(err, ratelimit.ErrBudgetExhausted) {
		r.metrics.CounterInc("conn_write_budget_exhausted_total",
			"GitHub writes deferred to the pending queue by the local budget", "repo", repo)
		if r.pending == nil {
			// Without a durable queue we must NOT accept-and-forget: surface
			// the pressure so the relay redelivers later.
			return Result{}, fmt.Errorf("emit: %w", err)
		}
		if qerr := r.enqueuePending(ctx, instID, repo, checkRunID, payload); qerr != nil {
			return Result{}, qerr
		}
		return Result{Queued: true}, nil
	}
	if err != nil {
		r.metrics.CounterInc("conn_check_publish_failures_total", "GitHub check publications that failed")
		return Result{}, err
	}
	r.metrics.CounterInc("conn_checks_published_total", "Agent Verification Gate API writes accepted",
		"status", payload.Status, "conclusion", payload.Conclusion)
	return published, nil
}

func (r *Router) dryWrite(repo string, checkRunID int64, payload checks.CheckPayload) (int64, error) {
	if checkRunID > 0 {
		return checkRunID, r.dry.Update(context.Background(), repo, checkRunID, payload)
	}
	return r.dry.Create(context.Background(), repo, payload)
}

// InstallationFor exposes the fail-closed resolution to callers that need
// the installation id for scoping (rerun budget buckets). ok=false ⇒
// unresolvable; callers must NOT guess an id.
func (r *Router) InstallationFor(ctx context.Context, repo string) (int64, bool) {
	if r.resolver == nil {
		return 0, false
	}
	owner, name, err := checks.SplitRepo(repo)
	if err != nil {
		return 0, false
	}
	instID, err := r.resolver.ResolveInstallation(ctx, owner, name)
	if err != nil {
		return 0, false
	}
	return instID, true
}

// resolvePublisher implements the fail-closed resolution ladder:
// unknown repo / no creds / no key ⇒ dry-run + metric, NEVER a guess.
func (r *Router) resolvePublisher(ctx context.Context, repo string) (int64, checks.Publisher, bool, error) {
	if r.registry == nil {
		return 0, nil, false, nil
	}
	if r.resolver == nil {
		r.metrics.CounterInc("conn_installation_unresolved_total",
			"Writes that fell back to dry-run because resolution is unwired", "reason", "resolver_unwired")
		return 0, nil, false, nil
	}
	owner, name, err := checks.SplitRepo(repo)
	if err != nil {
		return 0, nil, false, err
	}
	instID, err := r.resolver.ResolveInstallation(ctx, owner, name)
	if err != nil {
		r.metrics.CounterInc("conn_installation_unresolved_total",
			"Writes that fell back to dry-run because resolution is unwired", "reason", "unknown_repo")
		return 0, nil, false, nil
	}
	client, err := r.registry.Client(instID)
	if err != nil {
		return 0, nil, false, fmt.Errorf("emit: build client for installation %d: %w", instID, err)
	}
	return instID, checks.NewLivePublisher(client, r.logger), true, nil
}

func (r *Router) enqueuePending(ctx context.Context, instID int64, repo string, checkRunID int64, payload checks.CheckPayload) error {
	op := queue.OpCreateCheck
	if checkRunID > 0 {
		op = queue.OpUpdateCheck
	}
	w := queue.PendingWrite{
		Key:            payload.ExternalID + ":" + payload.Status,
		InstallationID: instID,
		Repo:           repo,
		Op:             op,
		CheckRunID:     checkRunID,
		Payload:        payload,
	}
	if err := r.pending.Enqueue(ctx, w); err != nil {
		return fmt.Errorf("emit: enqueue pending write: %w", err)
	}
	r.logger.Warn("write budget exhausted; write queued",
		slog.String("repo", repo), slog.String("key", w.Key), slog.String("op", string(op)))
	return nil
}
