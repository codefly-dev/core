package tui

import (
	"strings"
	"testing"
	"time"
)

// base is a fixed reference time so render assertions are deterministic without
// touching the wall clock.
var base = time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

func newTestPlan(t *testing.T) planView {
	t.Helper()
	restoreColorEnv(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("CODEFLY_COLOR", "never")
	configureColorProfile()
	p := newPlanView()
	p.setPlan([]string{"saas/store", "saas/vault", "saas/frontend"})
	return p
}

func TestPlanNextFrontierBeforeAnythingStarts(t *testing.T) {
	p := newTestPlan(t)
	line := p.nowNextLine(base)
	if !strings.Contains(line, "next: store → vault → frontend") {
		t.Fatalf("expected full next frontier, got %q", line)
	}
	if ready, total := p.counts(); ready != 0 || total != 3 {
		t.Fatalf("expected 0/3, got %d/%d", ready, total)
	}
}

func TestPlanActiveServiceLeavesNextAndCountsReady(t *testing.T) {
	p := newTestPlan(t)
	p.setReady("saas/store", 5432, base)
	p.setState("saas/vault", StateInitializing, base)

	line := p.nowNextLine(base.Add(3 * time.Second))
	if !strings.Contains(line, "now: vault initializing 00:03") {
		t.Fatalf("expected vault active in now, got %q", line)
	}
	if strings.Contains(line, "store") {
		t.Fatalf("ready service should not appear in now/next, got %q", line)
	}
	if !strings.Contains(line, "next: frontend") {
		t.Fatalf("expected frontend pending, got %q", line)
	}
	if ready, _ := p.counts(); ready != 1 {
		t.Fatalf("expected 1 ready, got %d", ready)
	}
}

func TestPlanFlagsSlowService(t *testing.T) {
	p := newTestPlan(t)
	p.setState("saas/vault", StateInitializing, base)

	// Within threshold: not slow.
	if got := p.tally(base.Add(5 * time.Second)); strings.Contains(got, "slow") {
		t.Fatalf("did not expect slow within threshold, got %q", got)
	}
	// Past threshold: flagged slow in both header and footer.
	now := base.Add(slowAfter + time.Second)
	if got := p.tally(now); !strings.Contains(got, "vault slow") {
		t.Fatalf("expected vault slow in tally, got %q", got)
	}
	if got := p.nowNextLine(now); !strings.Contains(got, "⚠") {
		t.Fatalf("expected slow warning glyph in now line, got %q", got)
	}
}

func TestPlanReportsFailure(t *testing.T) {
	p := newTestPlan(t)
	p.setFailed("saas/vault", base)
	if got := p.tally(base); !strings.Contains(got, "vault failed") {
		t.Fatalf("expected vault failed in tally, got %q", got)
	}
}

func TestPlanPhaseElapsedResetsOnTransition(t *testing.T) {
	p := newTestPlan(t)
	p.setState("saas/vault", StateLoading, base)
	// Transition to Init 4s later — elapsed should restart from the new phase.
	p.setState("saas/vault", StateInitializing, base.Add(4*time.Second))
	line := p.nowNextLine(base.Add(6 * time.Second))
	if !strings.Contains(line, "initializing 00:02") {
		t.Fatalf("expected per-phase elapsed of 00:02, got %q", line)
	}
}
