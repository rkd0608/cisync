<img src="https://r2cdn.perplexity.ai/pplx-full-logo-primary-dark%402x.png" style="height:64px;margin-right:32px"/>

# CI/CD and build systems Most pipelines were designed for a handful of human-triggered builds and tests per hour. Agents can open dozens or hundreds of PRs, branches, and experimental changes simultaneously. Result: queue explosions, flaky-test amplification, compute contention, and slow feedback loops that negate the agents’ speed advantage. A first-principles redesign would treat continuous agent activity as the default workload—ephemeral, highly parallel, priority-aware runners, semantic test selection, and agent-native failure recovery instead of human-centric job queues.Is something like this been already built?

Yes—but mostly as **components**, not as a fully integrated “agent-first CI operating system.” Today’s platforms can absorb much of the workload you describe, but their core control plane is usually still *workflow/job queue centric*, with AI layered on top rather than treating autonomous change generation as the native workload.

## What already exists

| Capability in your model | Existing implementations | Maturity |
| :-- | :-- | :-- |
| Ephemeral, elastic execution | GitHub Actions supports ephemeral self-hosted runners, and Actions Runner Controller (ARC) can use Kubernetes to automatically create and scale runner pods based on queued workflows. GitHub specifically recommends ephemeral rather than persistent self-hosted runners for autoscaling. [^1_1][^1_2] | Production-ready |
| High parallelism and test sharding | Buildkite Test Engine distributes tests across parallel agents and balances test work to shorten builds. [^1_3] | Production-ready |
| Semantic / predictive test selection | Launchable uses ML-based predictive test selection to choose tests likely to be affected by a specific code change; this category has research roots in predictive test selection for CI. [^1_4][^1_5] | Production-ready, imperfect |
| Flake detection and quarantine | Buildkite can detect and quarantine flaky tests; its detection identifies cases where a test both passes and fails for the same commit. [^1_3][^1_6] | Production-ready |
| Automated failure classification/retry | CI vendors and emerging “agentic CI/CD” products increasingly classify transient failures, retry them, and route issues based on inferred cause. [^1_7][^1_8] | Emerging |
| Parallel coding-agent workflows | Agent-workspace tooling promotes isolated worktrees, a coordinator/verifier pattern, automated gates, and sequential integration. [^1_9] | Early, mostly workflow-level |

A credible practical stack today would be: GitHub/GitLab + ARC/Kubernetes runners + Buildkite/CircleCI-style test execution + Launchable-style test selection + a custom control plane that arbitrates agent-generated work.

## The gap: the scheduler is still human-centric

Your thesis is correct: elastic runners and smarter test execution solve *capacity* and *test cost*, but they do not by themselves solve **agent-induced arrival rate**.

The default CI contract is still approximately:

$$
\text{push / PR event} \rightarrow \text{enqueue fixed workflow DAG} \rightarrow \text{run required jobs} \rightarrow \text{report status}
$$

That breaks down when hundreds of agents generate correlated, speculative changes. The missing control plane should instead decide:

$$
\text{candidate change} \rightarrow \text{estimate value, risk, overlap, and test impact} \rightarrow \text{allocate bounded validation budget}
$$

In particular, most current platforms do not natively offer all of the following together:

- **Change deduplication / semantic coalescing:** Detect that 40 agent branches are variations of the same underlying fix, cluster them, and validate representative candidates before committing compute to all of them.
- **A portfolio-level scheduler:** Allocate compute according to expected value, risk, dependency centrality, mergeability, deadline, and cost—not simply FIFO by workflow arrival time.
- **Speculation-aware cancellation:** Cancel or downgrade work that becomes obsolete when another agent’s patch supersedes it, a base branch changes, or an upstream validation fails.
- **Cross-branch result reuse:** Reuse build artifacts, test evidence, dependency resolution, static-analysis results, and environment snapshots across overlapping agent changes.
- **Agent-native recovery loop:** Classify a failure, determine whether it is infrastructure, flake, test defect, merge conflict, policy violation, or product regression, then give a constrained remediation task back to the responsible agent.
- **Merge-queue-aware agent coordination:** Coordinate changes based on the likely final merge train rather than testing each branch only against a stale base SHA.

That is more than “AI in CI.” It is a **resource-and-evidence scheduler for a population of autonomous software workers**.

## The closest current patterns

The nearest real implementations are fragmented:

1. **GitHub ARC / Buildkite / Depot-type execution layers**
These handle short-lived, isolated, autoscaled workers well. ARC runner scale sets can create ephemeral container-based runners and scale them around active workflows.[^1_2]
2. **Launchable-style test intelligence**
These systems reduce the test surface area by learning relationships between code changes and historical test outcomes.  This is closest to your “semantic test selection,” although it is typically test-impact prediction, not full semantic verification planning.[^1_4]
3. **Buildkite Test Engine-style test reliability control**
Test splitting, flaky-test identification, mute/quarantine controls, and test analytics address the amplification effect once AI agents produce more changes.[^1_3][^1_6]
4. **Coding-agent orchestration tools**
They manage worktrees, roles, code review, and merge sequencing, but generally stop before operating a CI fleet as a globally optimized shared resource.[^1_9]
5. **Internal systems at hyperscalers**
The conceptual precursor is large-scale presubmit infrastructure: affected-test selection, remote build caching, hermetic execution, merge trains, and prioritization. The novel part in your framing is that the workload generator is autonomous, continuously active, cheap to spawn, and able to react to its own failed validation.

## What a true agent-native CI platform would do

A differentiated design would make the unit of scheduling an **intent graph**, not a CI job.

For each candidate patch $c$, maintain:

- Changed symbols, APIs, files, dependency edges, and ownership boundaries.
- Semantic intent and issue/spec linkage.
- Predicted merge-conflict probability.
- Expected regression risk.
- Historical test-impact distribution.
- Resource estimate: CPU, memory, GPU, licenses, preview environments.
- Relationship to other candidate patches: duplicate, alternative, prerequisite, conflicting, or composable.

Then schedule validation by expected utility, for example:

$$
\text{priority}(c) =
\frac{
P(\text{valid} \mid c)
\times \text{business value}(c)
\times \text{merge urgency}(c)
\times \text{information gain}(c)
}{
\text{expected validation cost}(c)
}
$$

The system would use multi-stage validation:

1. **Cheap evidence:** format, type check, static analysis, targeted unit tests, dependency/security checks.
2. **Selective evidence:** impacted integration tests, contract tests, fuzzing for affected surfaces.
3. **Shared evidence:** reuse artifacts and results across semantically overlapping patches.
4. **Expensive evidence:** full suites, preview environments, canaries, or human review only for candidates that survive earlier stages.
5. **Recovery:** route failures to a specialized agent with exact logs, traces, repository context, and an explicit remediation budget.

That is much closer to a distributed scheduler plus a Bayesian evidence system than a conventional YAML pipeline executor.

## Bottom line

**The ingredients have been built; the category-level product has not been fully built yet.** Existing CI platforms provide autoscaling ephemeral runners, parallel execution, flaky-test handling, and predictive test selection.  What remains open is a unified agent-aware control plane that controls change generation, deduplicates and prioritizes speculative work, shares validation evidence, and closes the remediation loop automatically.[^1_1][^1_3][^1_4]

That is a legitimate infrastructure opportunity—particularly for companies where coding agents are already producing enough branches and PRs that CI, environments, and merge queues become the new bottleneck.
<span style="display:none">[^1_10][^1_11][^1_12][^1_13][^1_14][^1_15][^1_16][^1_17][^1_18][^1_19][^1_20][^1_21][^1_22][^1_23][^1_24][^1_25][^1_26][^1_27][^1_28][^1_29][^1_30]</span>

<div align="center">⁂</div>

[^1_1]: https://docs.github.com/en/actions/reference/runners/self-hosted-runners

[^1_2]: https://docs.github.com/en/actions/concepts/runners/actions-runner-controller

[^1_3]: https://buildkite.com/docs/pipelines/configure/tests

[^1_4]: https://help.launchableinc.com/features/predictive-test-selection/

[^1_5]: https://arxiv.org/html/1810.05286v2

[^1_6]: https://buildkite.com/solutions/testing-at-scale/

[^1_7]: https://testkube.io/blog/building-trust-in-ai-generated-code-through-continuous-testing

[^1_8]: https://zylos.ai/research/2026-05-12-agentic-cicd-ai-driven-delivery-pipelines/

[^1_9]: https://www.augmentcode.com/guides/how-to-run-a-multi-agent-coding-workspace

[^1_10]: https://www.harness.io/blog/flaky-tests-the-quiet-killer-of-productivity-in-your-ci-pipeline

[^1_11]: https://arxiv.org/html/2606.05608v1

[^1_12]: https://arxiv.org/html/2512.08769

[^1_13]: https://www.incredibuild.com/blog/agentic-ai-changing-software-development

[^1_14]: https://orkes.io/content/devguide/ai/agent-evals

[^1_15]: https://getautonoma.com/blog/automated-e2e-testing

[^1_16]: https://www.ranger.net/post/load-balancing-faster-test-execution-best-practices

[^1_17]: https://www.wiz.io/academy/ai-security/ai-agent-development

[^1_18]: https://www.linkedin.com/posts/pranay-kaikini-49632857_prototyping-an-ai-agent-is-relatively-straightforward-activity-7485385780241006593-a8xY

[^1_19]: https://www.reddit.com/r/theprimeagen/comments/1udsozo/the_end_of_software_engineering_how_ai_agents_are/

[^1_20]: https://dev.to/kuldeep_paul/a-practical-guide-to-integrating-ai-evals-into-your-cicd-pipeline-3mlb

[^1_21]: https://www.linkedin.com/posts/arpitbhayani_months-of-building-and-shipping-ai-systems-activity-7481550296742133760-Q9oT

[^1_22]: https://github.blog/changelog/2021-09-20-github-actions-ephemeral-self-hosted-runners-new-webhooks-for-auto-scaling/

[^1_23]: https://buildkite.com/platform/test-engine/

[^1_24]: https://buildkite.com/resources/releases/2023-06/flaky-test-tracker/

[^1_25]: https://oneuptime.com/blog/post/2026-01-27-github-actions-self-hosted-runners-k8s/view

[^1_26]: https://www.launchableinc.com/eng/predictive-test-selection-efficient-software-test-execution/

[^1_27]: https://dev.to/rajeevsrivastava/test-flakiness-prediction-using-machine-learning-in-cicd-pipelines-177j

[^1_28]: https://secure-pipelines.com/ci-cd-security/lab-ephemeral-self-hosted-runners-actions-runner-controller/

[^1_29]: https://www.launchableinc.com/docs/features/insights/flaky-tests/

[^1_30]: https://www.reddit.com/r/devops/comments/1gp0nzo/selfhost_github_actions_runners_with_actions/


---

# If Cursor Origin went down and solved the git issue from the first principles how should I solve this problem from the first principles as well end to end

Yes. Treat this as the same kind of move Cursor Origin makes with Git: don’t bolt “agent support” onto a human-era CI provider. Build the **system of record and control plane around agent-generated change activity**.

Cursor’s premise is that code hosting, PRs, and agents should live together because agents natively clone, branch, commit, push, and open PRs at high frequency. Origin is positioned as agent-scale Git hosting with repo, PR, code-search, and GitHub synchronization in one surface.  Your equivalent is an **agent-native verification and integration plane**: the place where agent changes acquire evidence, compete for compute, recover from failure, and become safe merge candidates.[^2_1][^2_2]

## Reframe the problem

GitHub Actions, Buildkite, and similar systems assume this:

> A human creates a change; CI validates it.

Your system should assume:

> Agents continuously generate competing hypotheses about how to change a codebase; the platform allocates verification budget and integrates only the strongest evidence-backed changes.

So your core abstraction should **not** be a pipeline, workflow YAML, branch, or PR.

It should be a **Change Intent**.

A Change Intent is a versioned object such as:

```json
{
  "intent_id": "ci_01J...",
  "repo": "acme/payments",
  "base_snapshot": "sha256:...",
  "goal": "Prevent duplicate charge on retry after timeout",
  "constraints": [
    "preserve idempotency API",
    "no schema migration",
    "p99 latency increase < 2 ms"
  ],
  "owned_surfaces": [
    "services/checkout/**",
    "libs/idempotency/**"
  ],
  "candidate_patches": ["patch_a", "patch_b", "patch_c"],
  "risk_class": "high",
  "deadline": null,
  "agent_lineage": ["planner-agent", "implementer-agent"]
}
```

Branches and commits become implementation artifacts underneath that object—not the unit at which the platform reasons.

## Build the equivalent of Origin

Cursor Origin’s strategic move is to own the metadata and operations around Git, while remaining Git-compatible. Its early beta supports repositories, PRs, agents, and GitHub mirroring, letting teams retain GitHub as the source of truth while evaluating the new system.[^2_3][^2_1]

Your initial wedge should be an **overlay control plane**, not a replacement for GitHub Actions or GitLab CI.


| Cursor Origin concept | Your CI-native analogue |
| :-- | :-- |
| Repository | Codebase plus dependency, ownership, test, and build graph |
| Branch | Candidate patch / change attempt |
| Pull request | Change Intent and its evidence dossier |
| Commit history | Experiment / remediation lineage |
| Code review | Evidence review plus policy decision |
| GitHub synchronization | GitHub Checks, status, PR, merge-queue, and artifact integrations |
| Agent automation | Agent dispatch, retry, repair, deduplication, and cancellation |
| Merge | A verified integration transaction |

Start as a GitHub App that watches PRs, commits, merge-queue events, workflow outcomes, and changed-file metadata. It posts one status check—say, **Agent Verification Gate**—but runs its scheduling and reasoning outside GitHub Actions.

This avoids requiring teams to migrate their source control system before receiving value.

## The end-to-end architecture

### 1. Ingest and normalize events

Your control plane subscribes to:

- PR opened, updated, synchronized, or marked ready for review
- Commit pushed
- Merge queue entry and base-branch movement
- Existing CI job started, succeeded, failed, or timed out
- Test-level results, durations, retries, and historical flake signals
- Deployment and rollback signals, when available
- Agent task lifecycle: created, paused, failed, repaired, superseded

Convert all of these into append-only events:

```text
ChangeProposed
PatchCreated
CandidateSuperseded
ValidationRequested
ValidationCompleted
TestFlakeSuspected
MergeBaseAdvanced
FailureClassified
RepairRequested
EvidenceAccepted
EvidenceExpired
MergeAuthorized
```

This event log is your source of truth. It enables replay, audits, model training, debugging, and policy changes without losing causality.

### 2. Create a semantic repository graph

You need more than files changed.

Build and incrementally maintain a graph containing:

- Files, symbols, packages, modules, services, APIs, schemas, migrations.
- Runtime call edges and service dependencies.
- Test-to-symbol, test-to-package, test-to-service, and test-to-deployment mappings.
- CODEOWNERS, domain teams, past incidents, and blast-radius signals.
- Build targets, cache keys, container images, test fixtures, and preview environments.
- Candidate-patch overlap and conflict edges.

At first, use practical approximations:

- AST and import/dependency analysis.
- Build-system metadata: Bazel targets, Gradle modules, Maven artifacts, Nx/Turborepo packages, etc.
- Test coverage and historical co-change data.
- Static code search plus embeddings for weak semantic links.
- OpenTelemetry traces to establish runtime dependencies, later.

The first high-value output is:

$$
P(\text{test fails} \mid \text{patch}, \text{test})
$$

The second is:

$$
P(\text{candidate } a \text{ conflicts with candidate } b)
$$

Do not begin by trying to prove that an LLM understands the entire codebase. Begin by becoming better than broad CI at deciding **what evidence is worth buying**.

### 3. Establish staged validation

Every candidate should pass through an evidence ladder, with promotion based on risk and prior results.


| Tier | Validation | Typical cost | Platform decision |
| :-- | :-- | --: | :-- |
| 0: Admission | Policy, diff sanity, secret scan, format/type/lint | Seconds | Reject obvious invalid work |
| 1: Local impact | Compile affected targets, targeted unit tests, static analysis | Minutes | Find cheap, high-signal failures |
| 2: Contract | Impacted integration, API compatibility, schema and dependency checks | Minutes to tens of minutes | Check cross-module behavior |
| 3: System | E2E, browser/mobile, load, fuzz, security, preview environments | Expensive | Run only when warranted |
| 4: Integration | Rebase/merge-train validation, canary, progressive rollout | Highest | Produce merge/deploy authorization |

Existing products demonstrate pieces of this model. Launchable uses ML-driven predictive test selection for code changes, while Buildkite provides test splitting plus flaky-test detection and quarantine.  Your differentiation is to make these decisions first-class scheduling and policy decisions across an agent population.[^2_4][^2_5]

### 4. Build an evidence-based scheduler

This is the heart of the company.

Every request for compute should include:

```text
candidate ID
intent ID
test/build action
estimated execution time
estimated cost
risk class
expected information gain
deadline / merge urgency
cancellation condition
cache and artifact dependencies
```

Then rank work by expected value, not arrival order:

$$
\text{priority}(v) =
\frac{
P(\text{decision changes} \mid v)
\cdot \text{risk reduction}(v)
\cdot \text{merge urgency}(v)
\cdot \text{business value}
}{
\text{cost}(v) \cdot \text{contention penalty}(v)
}
$$

This yields behavior conventional CI cannot naturally provide:

- A payment or authentication patch runs ahead of a low-risk documentation experiment.
- A 20-second targeted test can outrank a 45-minute E2E run if it is likely to disqualify the candidate.
- Forty near-duplicate agent patches are clustered; only a few diverse representatives receive expensive validation.
- Tests already proven against an identical artifact subtree are reused as evidence instead of rerun.
- A candidate is canceled immediately when its intent has been solved by another candidate.
- An expensive suite is paused or deprioritized when a base-branch advance makes its result stale.

Make every scheduling decision explainable:

> “Skipped 1,842 tests: no impacted symbols, no dependency-path intersection, no elevated risk signal. Ran 36 targeted tests and one payments contract suite. Confidence: 0.987. Full regression deferred until merge-train stage.”

That explanation is an important trust mechanism.

### 5. Make workers disposable and cacheable

The execution plane should be cloud-neutral and self-hostable:

- A Kubernetes-native runner fleet with one isolated, ephemeral worker per job.
- Firecracker microVMs for strong isolation where untrusted agent code runs.
- Locality-aware scheduling for large source trees, Docker layers, remote build cache, and test fixture datasets.
- A content-addressed artifact store keyed by source snapshot, dependency lockfile, toolchain, build flags, test fixture version, and environment signature.
- Warm pools for dominant workloads, but short-lived execution identities and credentials.
- CPU/memory/GPU/license quotas per repository, team, agent identity, and risk tier.

GitHub’s ARC provides a relevant baseline: it orchestrates and automatically scales self-hosted GitHub Actions runners using Kubernetes runner scale sets; ephemeral runners handle one job before removal.  Your platform should use that capability initially if it accelerates adoption, but should own the admission control, scheduling, artifact graph, and validation policy above it.[^2_6][^2_7]

## Agent-native recovery

This is where the product becomes qualitatively different from “smart CI.”

A failed job should never just emit a red X. It should produce a **failure diagnosis and next action**.

First, classify failure:


| Failure class | System response |
| :-- | :-- |
| Infrastructure/transient | Retry under a bounded policy, preferably on a different worker or zone |
| Known flake | Quarantine from the decision gate, record evidence, open or update a flake issue |
| New probable flake | Repeat under controlled conditions, compare environment and timing fingerprints |
| Compile/type/lint regression | Return a constrained repair task to the authoring agent |
| Test expectation drift | Ask a test-repair agent to determine whether behavior or test is wrong; require policy approval for changed expectations |
| Functional regression | Dispatch diagnostic agent with logs, diff, affected dependency graph, and reproduction command |
| Merge conflict/base staleness | Rebase candidate, rerun only invalidated evidence |
| Security/policy violation | Block and route to a dedicated remediation path; never autonomously waive |

A repair envelope should be bounded:

```json
{
  "candidate": "patch_123",
  "failure_type": "contract_test_regression",
  "reproduction": "bazel test //payments/checkout:retry_contract_test",
  "failed_assertion": "duplicate charge observed after retry",
  "suspected_diff_hunks": ["..."],
  "allowed_paths": ["services/checkout/**", "libs/idempotency/**"],
  "max_iterations": 2,
  "required_evidence_after_repair": [
    "targeted_unit",
    "retry_contract",
    "idempotency_integration"
  ]
}
```

The agent does not receive a vague “CI failed—fix it.” It gets an executable diagnosis, guardrails, and a finite budget. That avoids unbounded loops and runaway spend.

## Solve concurrency before CI

The biggest early insight may be: **you cannot solve validation cost if you let agents generate arbitrary redundant work.**

Build an agent admission and coordination layer:

1. Require every agent job to declare an intent, expected files/symbols, risk category, and success criterion.
2. Search for active intents and candidate patches before dispatching new coding work.
3. Cluster candidates by task, changed symbols, embeddings, dependency subgraph, and diff similarity.
4. Assign one of four relations: duplicate, alternative, composable, or conflicting.
5. Run alternatives as a small bounded tournament, rather than allowing unlimited PR proliferation.
6. Reserve scarce environments and expensive test capacity for representatives and likely winners.
7. Cancel losing or obsolete branches automatically, while preserving their useful diagnostics and patches.

Git worktrees solve local file-system isolation for parallel agents, but they do not solve semantic overlap, merge ordering, verification cost, or global compute allocation.  That is precisely the gap your control plane must own.[^2_8][^2_9]

## A product wedge

Do not launch as “a new CI/CD platform.” That invites competition with GitHub Actions, GitLab, CircleCI, Buildkite, Harness, and cloud vendors at once.

Launch as:

> **A verification scheduler for AI-generated code.**
> Reduce CI time and compute waste while safely allowing teams to run many coding agents in parallel.

Your first customer profile:

- Teams already running Cursor, Claude Code, Codex, or internal coding agents.
- Monorepos or multiple services with slow and costly CI.
- Significant integration/E2E burden, flaky tests, preview-environment scarcity, or merge-queue pain.
- Roughly 20–300 engineers is a strong initial segment: agent volume is visible, but they lack an internal developer-productivity organization that can build this themselves.

Your first three sellable outcomes:

- “We prevent agent PRs from overwhelming CI.”
- “We reduce median time-to-trust for agent-authored patches.”
- “We reduce expensive test and preview-environment spend without weakening required policy.”


## Minimal viable product

The MVP does **not** need an LLM to diagnose everything.

Build this sequence:

1. **GitHub App and event collector**
Ingest PR, push, check-run, merge-queue, and test-result events. Store an immutable event timeline.
2. **Test intelligence sidecar**
Learn test duration, pass/fail history, flaky behavior, changed-file correlation, and historical impact. Recommend or execute targeted suites.
3. **Queue controller**
Enforce per-repo and per-agent concurrency budgets; cancel obsolete runs; prioritize merge-queue work and short, high-information tests.
4. **Evidence UI and GitHub Check**
Show tests run, tests deferred, cache hits, flake treatment, confidence/risk, projected full-suite status, and the exact policy reason for every decision.
5. **Failure taxonomy plus deterministic recovery**
Start with infra retry, flake quarantine, stale-base revalidation, and precise failure routing—not open-ended agent repair.
6. **Bounded repair loop**
Only after you have trustworthy failure data, dispatch a patching agent for narrow compilation, lint, deterministic unit-test, and merge-conflict failures.
7. **Cross-candidate clustering and shared evidence**
This is the moat: identify alternatives and duplicates, test representative candidates, and reuse artifacts/evidence safely.

The MVP can be deployed as a GitHub App plus a hosted control plane, orchestrating native GitHub Actions initially. Later, offer Buildkite, GitLab, and self-hosted Kubernetes workers.

## Your durable moat

The moat is not “we invoke an agent when a test fails.” Every CI vendor can add that.

It is a proprietary, continuously improving **verification graph**:

$$
(\text{change}, \text{intent}, \text{symbol}, \text{dependency}, \text{test}, \text{environment}, \text{artifact}, \text{failure}, \text{repair}, \text{merge outcome})
$$

Over time, you learn:

- Which tests establish credible evidence for which classes of changes.
- Which test failures are flaky, environmental, or causally attributable to a patch.
- Which agent/task pairings produce mergeable code.
- Which changes conflict or compose before they reach a merge queue.
- What verification budget is justified for a specific risk profile.
- Which repair interventions actually convert failures into safe merge outcomes.

That data compounds with use and is deeply embedded in a customer’s build/test architecture. It is much harder to reproduce than an agent wrapper, workflow UI, or generic runner service.

## The one-sentence product thesis

**Build the agent-native merge and verification control plane: a system that converts an unbounded stream of agent-generated code into prioritized, deduplicated, evidence-backed integration decisions.**

That is the CI/CD equivalent of Cursor Origin’s first-principles move: not “Git with a chatbot,” but a source-control and collaboration substrate designed around continuous autonomous work.
<span style="display:none">[^2_10][^2_11][^2_12][^2_13][^2_14][^2_15][^2_16][^2_17][^2_18][^2_19][^2_20][^2_21]</span>

<div align="center">⁂</div>

[^2_1]: https://appwrite.io/blog/post/cursor-origin-review-an-engineers-perspective

[^2_2]: https://cursor.com/changelog/origin-code-hosting

[^2_3]: https://kingy.ai/blog/cursor-origin-vs-github/

[^2_4]: https://buildkite.com/docs/pipelines/configure/tests

[^2_5]: https://help.launchableinc.com/features/predictive-test-selection/

[^2_6]: https://docs.github.com/en/actions/reference/runners/self-hosted-runners

[^2_7]: https://docs.github.com/en/actions/concepts/runners/actions-runner-controller

[^2_8]: https://wavect.io/blog/git-worktrees-vs-jujutsu-ai-coding-agents/

[^2_9]: https://www.augmentcode.com/guides/git-worktrees-parallel-ai-agent-execution

[^2_10]: https://devops.com/cursor-launches-origin-code-hosting-platform-as-github-rival/

[^2_11]: https://www.linkedin.com/posts/gargeya-sharma_cursor-origin-git-forge-for-agents-gargeya-activity-7495459067725819904-Bu6y

[^2_12]: https://finance.biggo.com/news/979fe270-a07e-4684-b99e-f1af5d31317e

[^2_13]: https://dev.to/arifszn/git-worktrees-the-power-behind-cursors-parallel-agents-19j1

[^2_14]: https://zylos.ai/research/2026-02-22-git-worktree-parallel-ai-development/

[^2_15]: https://wavect.io/blog/cursor-origin-vs-github-code-hosting/

[^2_16]: https://x.com/mark_k/status/2066974487715966990

[^2_17]: https://engineering.zenity.io/p/parallel-development-with-git-worktree-for-cursor-claude-code

[^2_18]: https://www.learncursor.dev/guides/cursor-origin

[^2_19]: https://www.reddit.com/r/github/comments/1vrisbd/cursor_is_launching_a_github_competitor_is_this_a/

[^2_20]: projects.slack_knowledge_bot

[^2_21]: business.target_audience.audience_type


---

# No lets not discuss about what should I start with. I want to discuss about what should a finished product look like

A finished product should look like an **agent-native software integration system**—not a CI dashboard with AI retries. Its job is to continuously turn a large, concurrent stream of agent-generated code into a small number of **safe, evidence-backed production changes**.

Cursor Origin’s completed-product analogy is useful: it combines code hosting, pull requests, agent work, checks, and merges in the same operating surface, rather than making agents act through a separate automation layer.  Your equivalent owns the layer from *agent intent* through *validation*, *repair*, *integration*, *deployment*, and *learning*.[^3_1]

## The product in one view

A user opens the product and does **not** primarily see hundreds of branches or thousands of CI jobs.

They see a continuously updating **Change Graph**:

```text
Product goals / incidents / tickets
            ↓
      Change intents
            ↓
  Agent-generated candidate patches
            ↓
  Evidence plans and validation runs
            ↓
 Repair / supersede / combine / reject
            ↓
  Merge-ready integration sets
            ↓
 Progressive deployment and production evidence
```

The primary object is a **Change**, not a pull request.

A Change represents a desired outcome, such as:

> “Prevent duplicate payments when a checkout retry races with an upstream timeout.”

Under that one object, the system tracks all competing implementations, agents, commits, test evidence, deployment exposure, failures, and ultimate production outcome.

A PR remains a compatibility artifact for GitHub/GitLab and a useful human-review surface, but it is no longer the system’s fundamental unit of work.

## Core product objects

| Object | Meaning | Why it replaces human-era CI concepts |
| :-- | :-- | :-- |
| **Intent** | A goal, constraints, acceptance criteria, affected domain, risk class, and owner | Replaces “a branch appeared, run everything” |
| **Candidate** | A concrete implementation proposed by an agent or human | Replaces a one-to-one assumption between PR and solution |
| **Integration set** | A compatible, ordered group of candidates validated together | Replaces independent branch-by-branch merging |
| **Evidence** | A signed result from a build, test, analyzer, reviewer, preview, canary, or production metric | Replaces a binary green/red CI status |
| **Validation plan** | The minimum sufficient set of checks required for a candidate under policy | Replaces static workflow YAML as the decision-maker |
| **Environment lease** | An isolated, temporary execution or preview environment with a resource budget | Replaces manually shared staging bottlenecks |
| **Failure case** | A classified failure with repro, causal evidence, confidence, and remediation policy | Replaces raw logs and a red job |
| **Agent contract** | Permissions, scope, budget, identity, allowed actions, and escalation rules | Replaces unrestricted bot tokens and service accounts |
| **Decision** | Accept, reject, defer, repair, combine, split, quarantine, or deploy | Replaces “job succeeded” as the only meaningful output |

The finished product is essentially a **versioned evidence graph over a software system**.

## The main screen

The core UI should resemble an air-traffic-control system or a distributed-systems trace—not a list of CI jobs.

### Change graph

Each active intent appears as a node with:

- Goal and acceptance criteria.
- Risk and blast-radius score.
- Human owner, originating agent, and policy owner.
- Candidate implementations clustered beneath it.
- A current state: exploring, validating, blocked, repairing, merge-ready, deploying, monitoring, completed, or rejected.
- A live “evidence completeness” indicator, rather than a simple pass/fail badge.
- Merge-train position and base-branch freshness.

For example:

```text
Intent: Fix duplicate payment retry
Risk: High       Owner: Payments
State: Validating       Evidence: 82% sufficient

Candidate A: idempotency key lock
  ✓ Type and build
  ✓ 41 selected unit tests
  ✓ Payment contract suite
  ✓ Security diff scan
  ⟳ Isolated integration environment
  ○ Merge-train simulation

Candidate B: database uniqueness constraint
  ✓ Type and build
  ✕ Migration compatibility check
  → Rejected: rollout violates zero-downtime policy

Candidate C: queue-side deduplication
  ⏹ Superseded by Candidate A
```

The interface surfaces the **decision**, its confidence, its policy basis, the remaining uncertainty, and the next cheapest test that would materially change the outcome.

## The control plane

At the center is a durable control plane that owns policy, resource allocation, workflow state, provenance, and decisions.

It runs an event-sourced ledger:

```text
IntentCreated
CandidateProposed
CandidateClustered
PatchSubmitted
EvidenceRequested
RunnerLeased
ArtifactProduced
TestCompleted
FailureClassified
RepairAuthorized
CandidateSuperseded
MergeTrainCreated
IntegrationVerified
DeploymentStarted
CanaryObserved
RollbackTriggered
OutcomeRecorded
```

Every action is attributable to a human, agent, policy, or platform component. Every result includes immutable provenance:

- Source snapshot and merge base.
- Toolchain and dependency lockfiles.
- Build recipe and runner image digest.
- Environment and fixture versions.
- Test suite version and exact selection rationale.
- Agent model, prompt/versioned skill, permissions, and tool calls.
- Artifact digests and signed execution attestations.
- Policy version that made the accept/reject decision.

That makes the product auditable enough for high-stakes software, not just fast enough for demos.

## The scheduler as the brain

Traditional CI views work as a fixed job DAG:

$$
\text{commit} \rightarrow \text{workflow} \rightarrow \text{jobs} \rightarrow \text{green/red}
$$

The finished system executes a dynamic **evidence acquisition policy**:

$$
\text{intent} + \text{candidate} + \text{risk} + \text{repository state}
\rightarrow
\text{choose next action with maximum decision value}
$$

For every possible action—compile target, run test, build image, launch preview environment, run a code-review agent, fuzz an API, execute a canary—the scheduler estimates:

- Expected probability the action changes the integration decision.
- Risk reduction if the action passes.
- Cost in time, compute, licenses, and scarce environments.
- Cache and artifact reuse opportunity.
- Staleness probability due to concurrent merges.
- Dependency and merge-train implications.
- Policy-required evidence.

Conceptually:

$$
\text{next action} =
\arg\max_a
\frac{
\mathbb{E}[\text{decision information gained from } a]
\times
\text{risk reduction}
}{
\text{cost}(a) + \text{queue delay}(a) + \text{staleness risk}(a)
}
$$

The system therefore does not run “all checks on every branch.” It buys evidence in the most valuable order.

## Continuous concurrency management

A mature product treats agents as a continuous, bursty workload, not as occasional CI users.

It has a global view of all active work:

- 400 agents are attempting changes.
- 73 are working on overlapping concepts.
- 19 candidates touch the payment domain.
- 7 likely conflict on the same idempotency abstraction.
- 12 share 90% of a build graph and can share artifacts.
- 4 require a scarce preview environment.
- 1 is an incident hotfix with a hard latency SLO.

The platform automatically:

- Clusters semantically similar candidate changes.
- Identifies duplicates, alternatives, conflicts, prerequisites, and composable changes.
- Prevents agents from blindly editing the same owned surface.
- Tests a bounded set of diverse candidate strategies rather than every variation.
- Cancels stale, dominated, duplicate, or superseded candidate work.
- Combines compatible changes into an integration set.
- Re-prioritizes globally when an incident, release deadline, or merge-base change occurs.
- Limits each agent, team, repository, and domain to explicit compute and environment budgets.

This extends an emerging best practice: declare intended working sets before editing, so concurrent developers or agents can see active overlaps.  The finished product makes that declaration enforceable and operationally meaningful.[^3_2]

## Validation environments

Every meaningful validation runs inside an **environment lease**, not a shared staging environment.

A lease is:

```text
Lease ID: env_9f1...
Scope: Candidate A + Payments integration dependency set
TTL: 25 minutes
Snapshot: production-shaped sanitized data fixture v18
Runtime: checkout + ledger + gateway mock + tracing
Policy: no external payment egress; read-only production-like data
Budget: 8 vCPU, 32 GB RAM, 1 preview URL
Evidence target: retry-contract + idempotency-integration suite
```

The product automatically chooses among:

- Hermetic build/test sandboxes.
- Disposable Kubernetes namespaces.
- MicroVMs for untrusted agent-generated code.
- Per-change service overlays against real shared dependencies.
- Full ephemeral previews where necessary.
- Merge-train environments that validate the expected post-merge state.
- Controlled canary environments after deployment.

The core principle is **validation at the granularity of the change**, not at the granularity of a shared branch. Per-change isolated environments are increasingly recognized as necessary when coding agents generate changes faster than shared staging can absorb.[^3_3]

## Evidence instead of status checks

A completed system should not return merely:

```text
✓ CI passed
```

It returns an evidence dossier:

```text
Decision: Eligible for merge train
Confidence: High
Policy: payments/high-risk/v4

Evidence accepted:
✓ Hermetic build reproducible
✓ API compatibility preserved
✓ 44 selected unit tests passed
✓ 6 payment contract tests passed
✓ Idempotency race simulation passed, 10,000 schedules
✓ SAST and dependency policy passed
✓ Agent review found no unaddressed high-severity issue
✓ Replay on current merge base passed

Evidence intentionally deferred:
○ Full browser E2E suite
  Reason: no reachable UI/API dependency path; required at canary stage

Known uncertainty:
- Gateway-provider sandbox does not reproduce one production timeout mode
- Mitigation: 2% canary plus duplicate-charge invariant monitor

Required post-merge evidence:
- 2% canary for 30 minutes
- No duplicate-charge invariant violation
- Error rate within 0.2 percentage points of baseline
```

This is how you replace the false certainty of green CI with calibrated, policy-governed trust.

Required checks are still relevant as a compatibility boundary—GitHub-based agentic CI workflows commonly use them to prevent merge until all mandated checks pass.  But in the finished product, a GitHub check is just the external projection of a much richer decision engine.[^3_4]

## Native failure recovery

The system must be able to make a failure **actionable**, not merely observable.

When validation fails, it creates a Failure Case:

```text
Failure: FC-2398
Classification: Likely functional regression
Confidence: 0.93

Candidate: checkout retry idempotency-lock strategy
Reproduction:
  bazel test //payments/checkout:retry_contract_test

Causal signal:
  Fails on candidate and merge simulation
  Passes on base and two independent reruns
  Trace differs after `reserveCharge()` call

Suspected area:
  services/checkout/retry_handler.py:144-182

Allowed remediation:
  - Modify checkout and idempotency modules only
  - No schema migration
  - Maximum two repair attempts
  - Must preserve API behavior
  - Required revalidation: retry contract, race simulation, API compatibility

Decision:
  Repair agent dispatched
```

Possible responses are policy-controlled:

- Retry only for evidence of infrastructure transience.
- Quarantine a known flaky test while opening a tracked quality obligation.
- Rebase and invalidate only stale evidence after a merge-base advance.
- Dispatch a scoped repair agent for deterministic, attributable failures.
- Escalate to a human for ambiguous behavioral, security, privacy, or product-policy decisions.
- Reject or preserve a candidate as an experiment if it is dominated by another approach.

Agentic systems already commonly model agents as participants that write, test, debug, open PRs, and sometimes deploy changes; a finished platform makes their validation and repair loop bounded, observable, and governed.[^3_5]

## Integration and deployment

The final integration model is **transactional**, not “merge when green.”

A merge train creates a provisional post-merge world:

```text
main@N
  + Candidate A: idempotency fix
  + Candidate D: payment telemetry
  + Candidate K: dependency security update
  = Integration Set I-184
```

The system:

1. Resolves ordering and compatibility across the integration set.
2. Reuses valid evidence from candidates where the relevant dependency graph remains unchanged.
3. Runs only evidence invalidated by composition or changed merge base.
4. Builds and signs the integrated artifact.
5. Runs merge-train checks against the actual post-merge state.
6. Merges atomically or advances a protected integration branch.
7. Deploys according to policy: immediate, feature flag, staged rollout, or canary.
8. Watches production signals and invariants.
9. Attributes any regression to the integration set and, where possible, the causally responsible candidate.
10. Rolls back automatically within predefined blast-radius policies.

The platform’s terminal state is not “merged.” It is:

```text
Verified in production
```

or:

```text
Rolled back with causal investigation active
```


## Human role in the finished system

Humans do not approve every generated diff. That would simply move the bottleneck from CI queues to review queues.

Humans primarily define and revise:

- Product intent and acceptance criteria.
- Domain-specific invariants and risk policies.
- Ownership boundaries and protected surfaces.
- Budget limits for compute, agents, environments, and autonomous repair.
- Deployment and rollback thresholds.
- Escalation policy for ambiguity and unacceptable uncertainty.

They intervene on exceptions:

- A new product behavior whose correctness cannot be inferred from tests.
- High-impact security, privacy, compliance, or data-migration decisions.
- Ambiguous test-versus-implementation disputes.
- Risk levels beyond the authorized autonomy envelope.

For normal, well-specified work, the product autonomously gets to a policy-backed merge or a high-quality escalation packet.

## The finished-product promise

A customer should be able to say:

> “We can run thousands of software agents continuously. The system decides which work is worth validating, allocates compute intelligently, turns failures into bounded recovery tasks, integrates compatible work safely, and proves the result in production—without creating an unreviewable PR backlog or an unbounded cloud bill.”

That is not a next-generation CI vendor. It is the **autonomous integration layer for software production**: an evidence, scheduling, coordination, and governance system designed around an always-on population of coding agents.
<span style="display:none">[^3_10][^3_11][^3_12][^3_13][^3_14][^3_15][^3_6][^3_7][^3_8][^3_9]</span>

<div align="center">⁂</div>

[^3_1]: https://venturebeat.com/infrastructure/cursor-launches-origin-code-hosting-platform-as-github-outage-exposes-opening-in-ai-coding-race

[^3_2]: https://www.perforce.com/blog/vcs/p4-vs-git-for-ai-coding-agents

[^3_3]: https://www.signadot.com/blog/scaling-coding-agents-enterprise-kubernetes/

[^3_4]: https://developers.redhat.com/articles/2026/05/18/ci-cd-delivery-agentic-ai

[^3_5]: https://www.bunnyshell.com/guides/agentic-development/

[^3_6]: https://www.cognizant.com/us/en/ai-lab/blog/agentic-sdlc-multi-agent-software-delivery

[^3_7]: https://arxiv.org/html/2606.13175

[^3_8]: https://thenewstack.io/coding-agents-cicd-fix/

[^3_9]: https://www.linkedin.com/posts/joseph-widi_ainativesdlc-softwareengineering-engineeringleadership-activity-7485429862015397888-Igvj

[^3_10]: https://www.augmentcode.com/tools/autonomous-code-review-platforms-for-enterprise-teams

[^3_11]: https://www.reddit.com/r/cursor/comments/1vqypok/origin_cursors_code_hosting_platform_is_now_live/

[^3_12]: https://www.eesel.ai/blog/what-is-cursor-origin

[^3_13]: https://cursor.com/changelog/origin-code-hosting

[^3_14]: https://www.incredibuild.com/blog/agentic-ai-changing-software-development

[^3_15]: https://kingy.ai/blog/cursor-origin-vs-github/


---

# so how will the onboarding and installation and things like that look like?

The finished product should onboard like **Stripe + Datadog + GitHub App**, not like an enterprise CI migration. A team connects its existing developer stack, the platform learns passively, proposes a policy, then gradually becomes the authoritative integration decision-maker.

The key principle: **install in hours, gain autonomy over weeks, never require a rip-and-replace of GitHub/GitLab, existing CI, or deployment tooling.**

## The customer journey

| Phase | Customer experience | What the platform does |
| :-- | :-- | :-- |
| 1. Connect | Install GitHub App, select org/repos, confirm security posture | Creates tenant, receives webhook stream, maps repositories and identities |
| 2. Observe | No workflow changes and no merge blocking | Learns builds, tests, timing, flakes, ownership, dependency/test relationships, queue behavior |
| 3. Simulate | UI shows “what we would have run/canceled/prioritized” | Shadow-schedules validations and evaluates predicted vs actual outcomes |
| 4. Optimize | Teams opt into safe actions | Cancels redundant runs, prioritizes queues, shards tests, reuses valid artifacts, quarantines verified flakes |
| 5. Govern | Policy owners enable autonomous decisions per risk tier | Posts merge eligibility and evidence as a required Git check |
| 6. Integrate | Product owns merge-train and deployment evidence | Builds integration sets, runs post-merge validation, coordinates progressive delivery and recovery |

A mature team can stop at any stage. Someone can use it only for CI intelligence and queue scheduling; another can delegate merge and deployment authorization for low-risk services.

## Installation flow

### 1. Create an organization

An engineering-platform admin creates an organization and selects:

- Cloud-managed control plane or customer-hosted control plane
- Data residency and retention settings
- Identity provider: SAML/OIDC/SCIM, for larger organizations
- Default policy posture: observe-only, recommend-only, enforce for low risk, or enforce by domain
- Compute model: use existing CI runners, managed runners, customer Kubernetes, or hybrid

The first screen should explicitly state what is and is not copied:

```text
Source code storage: not required by default
Git metadata and diffs: required for selected repos
CI logs and test results: required for intelligence
Artifacts: metadata/digests by default; payloads optional
Secrets: never imported or persisted
Production telemetry: optional, scoped, and redacted
```


### 2. Install a GitHub App

For GitHub-first customers, this is the primary installation primitive—not a personal access token, OAuth token, or a bot with organization-admin access.

The admin clicks:

```text
Connect GitHub → Install App → Select repositories
```

They can choose:

- A pilot repository
- A repository group
- All current repositories
- New repositories automatically, subject to organization policy

GitHub Apps use scoped repository, organization, and account permissions; those permissions also determine which APIs and webhook events an app can access.  That maps well to a least-privilege onboarding model.[^4_1]

A typical initial permission request:


| GitHub permission | Access | Product purpose |
| :-- | --: | :-- |
| Metadata | Read | Identify repository, installation, default branch, and basic Git metadata |
| Contents | Read | Read source, config, dependency manifests, and changed paths |
| Pull requests | Read | Associate candidate changes with reviews, base branches, and merge state |
| Checks | Read \& write | Read check outcomes; publish evidence and merge-eligibility checks |
| Commit statuses | Read \& write | Compatibility with status-based protection rules |
| Actions | Read | Learn workflow/job results and durations |
| Workflows | Read | Parse CI topology; write only if the customer enables managed workflow installation |
| Deployments | Read | Correlate candidate/integration sets with deployments |
| Webhooks | Event subscription | Receive pushes, PR activity, check runs, workflow runs, deployments, and merge-queue signals |

The app should request **no write access to contents** during initial installation. It should not create branches, alter code, merge PRs, or modify workflow YAML until a customer separately enables those capabilities in policy.

The product receives signed GitHub webhooks—such as push, pull-request, check-run, workflow, deployment, and installation events—then validates, deduplicates, and appends them to its internal event ledger. GitHub’s event subscription requirements are tied to app permissions; Checks access, for example, controls relevant check-related webhook access.[^4_2]

### 3. Connect CI providers

The platform then asks:

> “Where does this repository currently validate changes?”

Customers select one or more:

- GitHub Actions
- Buildkite
- GitLab CI
- CircleCI
- Jenkins
- Bazel Remote Execution / Buildfarm
- Custom Kubernetes jobs
- A deployment platform such as Argo CD, Spinnaker, Harness, Vercel, or internal tooling

Each connector works at one of three depths:


| Mode | Setup | What the platform can do |
| :-- | :-- | :-- |
| Observe | Read-only API/webhook/API token with narrow scope | Learn job graph, duration, results, test outputs, queue delay, artifacts |
| Trigger | A generated webhook/API credential or reusable workflow | Request existing jobs with selected tests and parameters |
| Execute | Runner registration plus workload identity | Allocate and schedule ephemeral compute directly |

Most teams begin with **Observe**. This means no CI YAML modifications are required to get the initial value.

### 4. Install a small repository adapter

The product should support zero-config discovery, but offer a thin, versioned repository file for accurate semantics:

```yaml
# .integration/agent-control.yaml
version: v1

service:
  name: checkout
  tier: critical
  owners:
    - payments-platform

build:
  system: bazel
  targets:
    test: //...
    package: //services/checkout:image

validation:
  required:
    - static-analysis
    - payment-contract
  risk_overrides:
    database-migration:
      require:
        - migration-compatibility
        - full-integration
        - human-approval

environments:
  preview:
    template: payments-preview
    max_concurrent: 4

agent_policy:
  allowed_paths:
    - services/checkout/**
    - libs/idempotency/**
  prohibited_paths:
    - infrastructure/prod/**
  max_repair_attempts: 2
```

The repository adapter should be optional at first. The system can infer baseline mappings from:

- Build and dependency manifests
- CI workflow files
- Test reports and coverage output
- CODEOWNERS
- Deployment metadata
- Service catalog and IaC files
- Historical commit and failure data

But the adapter becomes the customer’s declarative source of truth for domain-specific invariants, risk policy, environment templates, and agent permissions.

## The first 30 days

### Days 0–2: Inventory

After connection, the system creates a “software delivery map”:

```text
Repositories: 46
Protected branches: main, release/*
CI providers: GitHub Actions, Buildkite
Median PR validation time: 28 min
p95 validation time: 96 min
Average queue delay: 11 min
Detected test suites: 3,782
Tests with possible flake signals: 184
Most expensive suite: payments-e2e
Shared staging environments: 2
Agent-generated PR rate: 31% of all PRs
```

It visualizes:

- Build/test topology
- Test duration and resource consumption
- Historical flake likelihood
- Test-to-module impact relationships
- Cross-service dependencies
- Queue bottlenecks and scarce environment contention
- Merge-base churn and PR staleness
- Agent versus human change throughput

No policy is enforced yet.

### Days 3–14: Shadow mode

The platform watches every real change, but does not modify execution.

For each PR or agent candidate it produces a counterfactual plan:

```text
Observed:
- 1,624 tests ran
- Duration: 43 min
- Cost: $18.20
- Result: pass

Platform simulation:
- Run 52 tests plus contract suite
- Expected duration: 8 min
- Expected cost: $3.10
- Full-suite confidence: 99.3%
- Deferred suites: browser-e2e, unrelated data-import tests
- Reason: no source or runtime dependency path from changed symbols
```

It measures itself rigorously:

- False-negative rate: cases where selected tests pass but the full suite finds a failure.
- False-positive rate: unnecessary tests/run volume.
- Time-to-decision reduction.
- Queue-delay reduction.
- Cache/artifact reuse rate.
- Flake classification precision.
- Cost avoided.
- Correlation between predicted and actual merge/deployment outcomes.

This is important because the platform earns trust by comparing its proposed decisions with the customer’s existing ground truth before it is allowed to block or merge anything.

### Days 15–30: Graduated authority

The product presents specific, reviewable policy changes:

```text
Recommendation: Enable low-risk selected-test policy

Scope:
- Repositories: docs-service, notification-service
- Change class: application code only
- Exclusions: migrations, auth, payments, dependencies, infrastructure
- Required evidence: build + typecheck + selected tests
- Fallback: full suite on prediction uncertainty > 0.02
- Rollback: revert to current CI workflow automatically

Observed shadow performance:
- 1,286 candidate changes analyzed
- 0 observed escaped test failures
- 71% median test-time reduction
- 62% fewer test-minutes
```

An engineering manager, staff engineer, or platform owner approves that policy. Only then does the product begin making decisions.

## Deployment choices

A finished product needs four installation modes.


| Mode | Control plane | Execution plane | Best fit |
| :-- | :-- | :-- | :-- |
| SaaS control, bring-your-CI | Vendor-hosted | Existing GitHub Actions/Buildkite/etc. | Fastest onboarding, most teams |
| SaaS control, managed runners | Vendor-hosted | Vendor-managed ephemeral workers | Teams seeking rapid capacity without Kubernetes |
| Hybrid | Vendor-hosted metadata/scheduler | Customer VPC/Kubernetes execution gateway | Security-conscious mid-market and enterprise |
| Self-hosted | Customer cloud/VPC | Customer Kubernetes or private compute | Regulated, air-gapped, sovereignty requirements |

The product control plane does not need production credentials just to optimize CI. For a hybrid design, a customer deploys one **Execution Gateway** in its VPC or cluster:

```text
Customer VPC
├── Execution Gateway
│   ├── Outbound-only mTLS connection to control plane
│   ├── Receives signed job leases
│   ├── Exchanges short-lived workload identities
│   ├── Starts isolated runners / Kubernetes jobs / microVMs
│   └── Uploads redacted logs, evidence, digests, and metrics
│
├── Private source mirror or GitHub Enterprise access
├── Internal package registries
├── Internal test infrastructure
└── Preview/staging network
```

No inbound firewall opening is required. The gateway has no standing production credential; it assumes narrowly scoped, short-lived identities only for a specific job or environment lease.

For GitHub Actions users, it can optionally use ARC as the runner substrate. ARC supports Kubernetes-backed autoscaling runner scale sets, but GitHub warns that Actions workflows execute arbitrary code, which is why isolated runners and careful deployment boundaries matter.  The platform uses ARC as a worker implementation detail, not as its control plane.[^4_3]

## Security onboarding

Security approval should be a first-class product flow, not a sales-engineering PDF.

### Trust center generated for each installation

The installer automatically gets a scoped architecture document:

```text
Tenant: Acme
Repositories: 12 selected
Source access: read-only
Write capabilities: checks/statuses only
Secrets stored: none
Credential model: GitHub App installation tokens, short-lived
Execution: customer VPC gateway
Log retention: 30 days, configurable
Artifact retention: digest-only
Production access: disabled
Data residency: US
Autonomous merge: disabled
Autonomous repair: disabled
```


### Secret model

- Never collect developer PATs.
- Never ingest CI secret values.
- Do not send secrets or full environment dumps to agent models.
- Redact logs before model use and before storage outside the customer boundary.
- Use OIDC federation and short-lived identities for cloud, registries, and deployments.
- Issue per-job credentials bound to repository, commit, environment, action, and expiry.
- Keep agent tools permissioned through an explicit **agent contract**, not through a broad CI token.


### Provenance model

Every artifact and validation decision carries provenance:

```text
Artifact: checkout@sha256:...
Built from: integration-set/I-184
Source: main@abc + candidates A,D,K
Builder image: sha256:...
Build recipe: v7
Dependencies: lockfile sha256:...
Validation evidence: EV-4482 through EV-4510
Policy decision: payments/high-risk/v4
```

GitHub artifact attestations offer a concrete compatibility mechanism: they create cryptographically signed provenance statements tying an artifact to its repository, commit, workflow, triggering event, and OIDC-backed build context.  The product should ingest and verify them where available, while producing its own vendor-neutral evidence attestations.[^4_4]

## How agents connect

The platform should expose an **Agent Integration API**, SDK, and MCP server. Any coding agent—Cursor, Claude Code, Codex, an internal agent, or a human-operated CLI—must obtain a scoped change lease before it begins substantial work.

```text
POST /v1/change-intents

goal: "Fix duplicate payment on retry timeout"
repository: acme/payments
base: main
expected_surfaces:
  - services/checkout/**
  - libs/idempotency/**
acceptance_criteria:
  - duplicate_charge_count == 0 in race simulation
risk: high
```

The response gives the agent:

```json
{
  "intent_id": "int_94f8",
  "lease_id": "lease_3ab1",
  "base_snapshot": "main@b734e",
  "worktree_or_branch": "agent/int_94f8/candidate_01",
  "allowed_paths": [
    "services/checkout/**",
    "libs/idempotency/**"
  ],
  "conflicts": [
    {
      "intent_id": "int_91c2",
      "relation": "overlapping",
      "owner": "payments-platform",
      "recommendation": "coordinate"
    }
  ],
  "required_evidence": [
    "payment-contract",
    "idempotency-race"
  ],
  "compute_budget": {
    "cpu_minutes": 120,
    "environment_minutes": 30,
    "repair_attempts": 2
  }
}
```

This changes agent behavior from:

```text
Make a branch → edit freely → open a PR → overwhelm CI
```

to:

```text
Declare intent → acquire scoped lease → implement → submit candidate
→ receive dynamic validation plan → repair or merge based on evidence
```

Human developers may use the same workflow through a CLI or IDE extension, but it should never feel mandatory for ordinary Git work.

## Daily developer experience

From a developer or agent’s perspective, the command surface should be small:

```bash
integration intent create \
  --goal "Prevent duplicate charges after retry timeout" \
  --risk high

integration status int_94f8

integration explain candidate_01

integration reproduce FC-2398

integration request-human-review int_94f8
```

The UI, CLI, IDE extension, Slack/Teams integration, GitHub Checks, and agent API all expose the same decision model.

A developer receives a message like:

> Candidate `retry-idempotency-lock` is blocked by one deterministic contract failure. The platform isolated the regression to `reserveCharge()` after timeout handling, prepared a reproduction command, and authorized one scoped repair attempt. It did not run the unrelated 1,400-test browser suite because policy classified that evidence as non-material for this change.

That is substantially better than a wall of red CI logs.

## What customers never need to do

A finished product should avoid asking customers to:

- Migrate repositories away from GitHub/GitLab.
- Replace CI systems on day one.
- Rewrite every workflow into a proprietary YAML format.
- Hand-label every test or dependency relationship.
- Give a vendor persistent cloud-admin or production credentials.
- Expose secrets, raw proprietary datasets, or unrestricted production logs to agents.
- Trust autonomous merges before the system has proven itself in shadow mode.
- Manually configure every agent, runner, test, environment, and policy before getting value.

The onboarding message is simple: **connect your existing SDLC, observe safely, prove the decisions, then grant authority incrementally.**
<span style="display:none">[^4_10][^4_11][^4_12][^4_13][^4_14][^4_15][^4_5][^4_6][^4_7][^4_8][^4_9]</span>

<div align="center">⁂</div>

[^4_1]: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app

[^4_2]: https://docs.github.com/webhooks/webhook-events-and-payloads

[^4_3]: https://docs.github.com/en/actions/how-tos/manage-runners/use-actions-runner-controller/deploy-runner-scale-sets

[^4_4]: https://docs.github.com/en/actions/concepts/security/artifact-attestations

[^4_5]: https://docs.github.com/en/actions/tutorials/use-actions-runner-controller/get-started

[^4_6]: https://docs.github.com/actions/security-guides/using-artifact-attestations-and-reusable-workflows-to-achieve-slsa-v1-build-level-3

[^4_7]: https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps

[^4_8]: https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds

[^4_9]: https://github.com/actions/actions-runner-controller/blob/master/docs/quickstart.md

[^4_10]: https://www.legitsecurity.com/blog/slsa-provenance-blog-series-part-2-deeper-dive-into-slsa-provenance

[^4_11]: https://github.com/actions/actions-runner-controller/discussions/2174

[^4_12]: https://github.com/orgs/community/discussions/42351

[^4_13]: https://docs.cloudposse.com/components/library/aws/eks/github-actions-runner/

[^4_14]: https://github.com/actions/actions-runner-controller/discussions/2835

[^4_15]: https://dev.to/kanywst/slsa-provenance-hands-on-generate-with-github-actions-verify-with-slsa-verifier-56ka


---

# will I be able to vibe code this full platform?

**You can vibe-code a credible prototype and much of an early product. You should not vibe-code the entire finished platform without deliberately engineering its security, correctness, and distributed-systems core.**

This is a hard infrastructure product: it executes arbitrary customer code, makes merge/deploy decisions, may access source code and CI results, and eventually becomes part of the software supply chain. Build isolation and provenance are not polish; SLSA explicitly requires isolated build environments and cryptographically attributable provenance for higher assurance.[^5_1][^5_2]

## What you can vibe-code

Given your backend, distributed-systems, Kafka/Flink, Redis, and AI-agent background, you can probably get a compelling vertical slice surprisingly far with agents.

You can use AI coding tools to build:


| Area | Vibe-code feasibility | Notes |
| :-- | :-- | :-- |
| GitHub App installation and OAuth-like installation flow | High | Standard webhook, installation-token, repo-selection flows |
| GitHub webhook ingestion | High | Validate signatures, deduplicate deliveries, write events to durable storage |
| Event timeline and basic control-plane UI | High | Changes, candidates, job state, queue views, cost/time dashboards |
| GitHub Checks/status reporting | High | Post a check with evidence summaries and links |
| PR/commit/workflow ingestion | High | Read existing check runs, Actions job status, changed files, PR metadata |
| Shadow-mode test-selection recommender | High | Begin with dependency/changed-file heuristics and historical correlations |
| Queue visualizer and prioritization simulator | High | Great demo: show queue delay, redundant work, and recommended cancellation |
| Agent intent API / MCP server | High | Intent declarations, scoped task leases, candidate registration |
| Failure clustering and log summarization | High | LLM plus deterministic log parsing and known signatures |
| Basic flaky-test detection | High | Historical pass/fail instability, rerun correlation, environment fingerprints |
| GitHub Action or CLI installer | High | Generate config, install adapters, register required metadata |
| A narrow repair loop | Medium | Constrain it to lint, type-check, deterministic unit tests, and merge conflicts |

A convincing demo could be:

1. An agent opens 30 overlapping PRs against a demo monorepo.
2. Your platform groups 20 as semantic duplicates or alternatives.
3. It uses the changed dependency graph to choose 40 tests instead of 1,500.
4. It schedules the highest-information checks first.
5. It detects a deterministic regression, creates a bounded repair task, and retests only invalidated evidence.
6. It posts one GitHub Check: **Eligible for Merge Train**, with a traceable explanation.

That can absolutely be built using strong coding agents plus careful product direction.

## What must be engineered

Do not allow an LLM-generated codebase to be the final authority for these components without deep review, testing, and ownership.

### Execution isolation

A runner executes code from the repository, which is effectively untrusted code. GitHub warns that self-hosted runners are not guaranteed to be ephemeral clean VMs and can be persistently compromised by untrusted workflow code; it advises against their use for public repositories in particular.[^5_3]

You need deliberate engineering around:

- Per-job ephemeral VMs or hardened containers.
- Namespace, filesystem, process, and network isolation.
- No credentials or signing keys accessible from build execution.
- Egress controls and explicit allowlists.
- Separate trust domains for public PRs, private branches, agents, and privileged release jobs.
- Supply-chain-safe cache isolation: no cache poisoning across tenants, repos, branches, or trust levels.
- Cleanup verification after every job.

This part should be built with proven primitives—Kubernetes, Firecracker, gVisor, cloud workload identity, OIDC, artifact registries—not invented from scratch.

### Auth and authorization

The agent can generate routes and RBAC code. It should not determine the authorization model.

You need explicit policies for:

- Tenant, organization, repository, environment, team, human, and agent identities.
- Read/write distinctions: viewing a repository versus posting a check versus merging a PR versus deploying production.
- Short-lived tokens scoped to one job and one action.
- Clear separation of the control plane, execution gateway, and runner identity.
- Audit logs that cannot be rewritten by a compromised worker.
- An agent permission envelope: allowed paths, commands, compute budget, repair iterations, and prohibited actions.


### Correctness of decisions

Your core value is not “we ran fewer tests.” It is:

> “We ran fewer tests without allowing unsafe changes to escape.”

That means you need a rigorous evaluation framework before selected-test decisions can block or authorize merges:

- Compare every shadow-mode recommendation against full-suite ground truth.
- Track escaped failures, coverage misses, false-negative rate, false-positive cost, and confidence calibration.
- Version every selection model and policy.
- Define hard fallbacks: uncertain prediction, risky paths, changes in auth/payments/schema/infra, or sparse history should widen validation automatically.
- Treat test selection as a risk-budget decision, not a classifier trying to maximize a benchmark score.


### State and scheduling

This is your natural technical territory, but it requires engineering discipline.

The scheduler needs:

- Idempotent event processing and at-least-once webhook delivery handling.
- Durable workflow state machines.
- Fencing tokens and leases to prevent duplicate or stale execution.
- Cancellation propagation that does not kill the wrong job.
- Fairness and quota models across tenants, teams, repositories, and agents.
- Backpressure when agent arrivals exceed compute or environment capacity.
- Artifact cache correctness keyed by all relevant inputs.
- Reconciliation loops for lost worker events and cloud-provider partial failures.

Agents can help write the components, but you must own the invariants.

### Merge and deployment authority

Never begin with autonomous merge or production deployment.

A system that can write code, run it, interpret failures, and deploy its own output needs layered controls. OWASP recommends isolated build nodes and secured SCM-to-CI communication; these are baseline controls, not advanced features.[^5_4]

Autonomous actions should advance through levels:

```text
Level 0: Observe and explain
Level 1: Recommend tests, prioritization, cancellations
Level 2: Trigger pre-approved validation
Level 3: Run bounded repair attempts in isolated branches
Level 4: Mark low-risk candidates merge-eligible
Level 5: Automatically merge protected low-risk changes
Level 6: Progressively deploy under strict runtime invariants
```

For every level, a human should be able to set scope, budgets, escalation rules, kill switches, and required approvals.

## A productive build strategy

The right distinction is:

- **Vibe-code the product surface, integrations, workflows, and experimentation velocity.**
- **Engineer the trust boundary, scheduler invariants, security model, and production authority.**

Use agents as a force multiplier, but make them work inside a strong engineering loop:

1. Write an architecture decision record and threat model first.
2. Define invariants in plain language and as executable tests.
3. Ask an agent to implement a small bounded component.
4. Require unit, integration, property-based, and fault-injection tests for stateful components.
5. Review security-sensitive diffs manually.
6. Run shadow mode against real or replayed CI event streams.
7. Enforce nothing until you have measured error rates and safe fallback behavior.

For example, an agent can implement “select tests based on changed files,” but you must independently define and test:

```text
Invariant:
A skipped test can never count as positive evidence.

Invariant:
If an output-affecting input changes, a prior artifact/test result cannot be reused.

Invariant:
A job lease may produce at most one accepted evidence record.

Invariant:
No job runner can access a credential broader than its declared action,
repository, environment, and TTL.

Invariant:
No agent repair may modify paths outside its granted contract.
```


## The honest answer

You can **vibe-code the first 50–70% of an impressive platform**, especially its UI, API, event ingestion, intelligence layer, simulation engine, GitHub integration, and constrained agent workflows.

The remaining 30–50% is where the company becomes real:

- Secure multi-tenancy.
- Isolated execution.
- Artifact integrity and provenance.
- Correct distributed scheduling.
- Safe policy evaluation.
- Trustworthy test-selection guarantees.
- High-availability control plane and recovery.
- Safe integration and deployment authority.

That portion is not “less creative” work. It is the product’s defensible core—and likely where your systems background gives you an advantage over teams that merely wrap coding agents.
<span style="display:none">[^5_10][^5_11][^5_12][^5_13][^5_14][^5_15][^5_5][^5_6][^5_7][^5_8][^5_9]</span>

<div align="center">⁂</div>

[^5_1]: https://slsa.dev/spec/v1.2/build-requirements

[^5_2]: https://slsa.dev/spec/v1.0/levels

[^5_3]: https://docs.github.com/en/actions/reference/security/secure-use

[^5_4]: https://cheatsheetseries.owasp.org/cheatsheets/CI_CD_Security_Cheat_Sheet.html

[^5_5]: https://slsa.dev/spec/v0.1/requirements

[^5_6]: https://slsa.dev/spec/v1.0/whats-new

[^5_7]: https://stackoverflow.com/questions/79107120/how-to-protect-github-actions-self-hosted-runner

[^5_8]: https://spacelift.io/blog/ci-cd-security

[^5_9]: https://www.kusari.dev/learning-center/build-provenance/

[^5_10]: https://www.sysdig.com/blog/how-threat-actors-are-using-self-hosted-github-actions-runners-as-backdoors

[^5_11]: https://github.com/orgs/community/discussions/57201

[^5_12]: https://www.linkedin.com/pulse/mitigating-owasp-top-10-cicd-security-risks-using-aws-daniel-begimher-y5gte

[^5_13]: https://distantjob.com/blog/ci-cd-pipeline-security-best-practices/

[^5_14]: https://docs.chainloop.dev/reference/slsa-provenance

[^5_15]: https://aquilax.ai/blog/github-actions-security-hardening

