package domain

import (
	"errors"
	"testing"
	"time"
)

func intentFixture(t *testing.T) *Intent {
	t.Helper()
	deadline := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	return NewIntent(NewID(PrefixIntent), "org_test", IntentDeclared{
		Goal:           "g",
		Repo:           "acme/payments",
		BaseRef:        "main",
		BaseSnapshot:   "main@b734e",
		OwnedSurfaces:  []string{"services/**"},
		RiskClass:      RiskHigh,
		Origin:         OriginAgentAPI,
		ResolvedPolicy: PolicyRef{PolicyID: "pol_default", Version: 1},
		ComputeBudget:  BudgetValues{CPUMinutes: 120, EnvironmentMinutes: 30, RepairAttempts: 2},
		Deadline:       &deadline,
	}, time.Now().UTC())
}

func TestIntentHappyPath(t *testing.T) {
	i := intentFixture(t)
	steps := []struct {
		trigger string
		want    IntentState
	}{
		{"validation.planned", IntentValidating},
		{"decision.eligible", IntentMergeReady},
		{"deploy.authorized", IntentDeploying},
		{"deploy.executed", IntentMonitoring},
		{"post_merge.satisfied", IntentCompleted},
	}
	for _, step := range steps {
		if err := i.Apply(step.trigger); err != nil {
			t.Fatalf("apply %s: %v", step.trigger, err)
		}
		if i.State != step.want {
			t.Fatalf("state after %s = %s, want %s", step.trigger, i.State, step.want)
		}
	}
	if !i.State.Terminal() || i.ClosedAt == nil {
		t.Fatal("completed intent must be terminal with closed_at")
	}
}

func TestIntentBlockedRepairLoop(t *testing.T) {
	i := intentFixture(t)
	if err := i.Apply("validation.planned"); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{"failure.blocked", "repair.authorized", "candidate.resubmitted", "failure.blocked"} {
		if err := i.Apply(trigger); err != nil {
			t.Fatalf("apply %s: %v", trigger, err)
		}
	}
	if i.State != IntentBlocked {
		t.Fatalf("want blocked, got %s", i.State)
	}
	if err := i.Apply("intent.rejected"); err != nil {
		t.Fatal(err)
	}
	if i.State != IntentRejected || !i.State.Terminal() {
		t.Fatalf("want rejected terminal, got %s", i.State)
	}
}

// Table-driven legality matrix: every aggregate × trigger × current state.
func TestIntentTransitionMatrix(t *testing.T) {
	all := []IntentState{
		IntentExploring, IntentValidating, IntentBlocked, IntentRepairing,
		IntentMergeReady, IntentDeploying, IntentMonitoring, IntentCompleted, IntentRejected,
	}
	type move struct {
		from  IntentState
		event string
		to    IntentState
		legal bool
	}
	mk := func(from IntentState, event string, to IntentState, legal bool) move {
		return move{from, event, to, legal}
	}
	var moves []move
	add := func(event string, froms []IntentState, to IntentState) {
		for _, f := range all {
			legal := false
			for _, lf := range froms {
				if lf == f {
					legal = true
				}
			}
			moves = append(moves, mk(f, event, to, legal))
		}
	}
	preTerminal := []IntentState{IntentExploring, IntentValidating, IntentBlocked, IntentRepairing, IntentMergeReady, IntentDeploying, IntentMonitoring}
	add("validation.planned", []IntentState{IntentExploring}, IntentValidating)
	add("failure.blocked", []IntentState{IntentValidating}, IntentBlocked)
	add("repair.authorized", []IntentState{IntentValidating, IntentBlocked}, IntentRepairing)
	add("candidate.resubmitted", []IntentState{IntentRepairing, IntentBlocked}, IntentValidating)
	add("decision.eligible", []IntentState{IntentValidating}, IntentMergeReady)
	add("deploy.authorized", []IntentState{IntentMergeReady}, IntentDeploying)
	add("deploy.executed", []IntentState{IntentDeploying}, IntentMonitoring)
	add("post_merge.satisfied", []IntentState{IntentMonitoring}, IntentCompleted)
	add("intent.rejected", preTerminal, IntentRejected)

	for _, m := range moves {
		i := intentFixture(t)
		i.State = m.from
		err := i.Apply(m.event)
		if m.legal && err != nil {
			t.Errorf("%s --%s--> %s: unexpected error %v", m.from, m.event, m.to, err)
		}
		if !m.legal && err == nil {
			t.Errorf("%s --%s--> %s: expected illegal transition error", m.from, m.event, m.to)
		}
	}
}

// I-08: every event on a terminal aggregate must be logged-and-ignored.
func TestIntentPostTerminalIgnored(t *testing.T) {
	for _, term := range []IntentState{IntentCompleted, IntentRejected} {
		i := intentFixture(t)
		i.State = term
		for _, ev := range []string{"validation.planned", "decision.eligible", "intent.rejected", "unknown.event"} {
			err := i.Apply(ev)
			if !errors.Is(err, ErrPostTerminal) {
				t.Errorf("terminal %s event %s: want ErrPostTerminal, got %v", term, ev, err)
			}
			if i.State != term {
				t.Errorf("terminal %s mutated by %s", term, ev)
			}
		}
	}
}

func TestIntentUnknownEvent(t *testing.T) {
	i := intentFixture(t)
	err := i.Apply("nonexistent.trigger")
	if !errors.Is(err, ErrUnknownEvent) {
		t.Fatalf("want ErrUnknownEvent, got %v", err)
	}
}
