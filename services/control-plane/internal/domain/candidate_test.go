package domain

import (
	"errors"
	"testing"
	"time"
)

func candidateFixture(t *testing.T) *Candidate {
	t.Helper()
	c, err := NewCandidate(NewID(PrefixCandidate), "org_test", NewID(PrefixIntent),
		"agent:org_test", "bundle://p", headSHA("a"), headSHA("b"), []string{"svc/**"}, 20000,
		time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func headSHA(seed string) string {
	out := make([]byte, 40)
	for i := range out {
		out[i] = seed[i%len(seed)]
		if out[i] == 0 {
			out[i] = 'a'
		}
	}
	return string(out)
}

func TestCandidateLifecycle(t *testing.T) {
	c := candidateFixture(t)
	for _, step := range []struct {
		trigger string
		want    CandidateState
	}{
		{"validation.planned", CandPlanned},
		{"validation.admitted", CandValidating},
		{"decision.eligible", CandEligible},
	} {
		if err := c.Apply(step.trigger); err != nil {
			t.Fatalf("apply %s: %v", step.trigger, err)
		}
		if c.State != step.want {
			t.Fatalf("after %s want %s got %s", step.trigger, step.want, c.State)
		}
	}
}

func TestCandidateRejectsIdenticalSHAs(t *testing.T) {
	same := headSHA("a")
	_, err := NewCandidate("cand_x", "org_test", "int_x", "agent", "ref", same, same, nil, 0, time.Now().UTC())
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("want ErrValidationFailed, got %v", err)
	}
}

func TestCandidateSupersedeAndCancelFromPreTerminalOnly(t *testing.T) {
	preTerminal := []CandidateState{CandSubmitted, CandPlanned, CandValidating, CandRepairing, CandBlockedRepresentative}
	for _, from := range preTerminal {
		for _, ev := range []string{"candidate.superseded", "candidate.cancelled"} {
			c := candidateFixture(t)
			c.State = from
			if err := c.Apply(ev); err != nil {
				t.Errorf("%s --%s--> error %v", from, ev, err)
			}
			if !c.State.Terminal() {
				t.Errorf("%s --%s--> non-terminal %s", from, ev, c.State)
			}
		}
	}
	for _, term := range []CandidateState{CandEligible, CandRejected, CandSuperseded, CandCancelled} {
		c := candidateFixture(t)
		c.State = term
		if err := c.Apply("candidate.cancelled"); !errors.Is(err, ErrPostTerminal) {
			t.Errorf("terminal %s: want ErrPostTerminal got %v", term, err)
		}
	}
}

func TestCandidateIllegalMatrix(t *testing.T) {
	illegal := []struct {
		from CandidateState
		ev   string
	}{
		{CandSubmitted, "decision.eligible"},
		{CandSubmitted, "validation.admitted"},
		{CandPlanned, "repair.authorized"},
		{CandRepairing, "decision.eligible"},
		{CandBlockedRepresentative, "validation.admitted"},
	}
	for _, tc := range illegal {
		c := candidateFixture(t)
		c.State = tc.from
		err := c.Apply(tc.ev)
		if err == nil {
			t.Errorf("%s --%s-->: expected error", tc.from, tc.ev)
			continue
		}
		if !errors.Is(err, ErrIllegalTransition) && !errors.Is(err, ErrUnknownEvent) && !errors.Is(err, ErrPostTerminal) {
			t.Errorf("%s --%s-->: want illegal/unknown/post-terminal, got %v", tc.from, tc.ev, err)
		}
	}
}
