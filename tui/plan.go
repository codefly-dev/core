package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// slowAfter is how long a service may sit in a single non-terminal phase
// before the live status flags it as slow. This is the signal that turns a
// silent stall (e.g. vault waiting 30s on a health probe) into a visible
// "⚠ slow" instead of an unexplained pause behind the origin's spinner.
const slowAfter = 15 * time.Second

// svcStatus is the live state of one managed service.
type svcStatus struct {
	state ServiceState
	since time.Time // when it entered the current phase
	port  int
}

// planView tracks every service in a run and renders the compact "now / next"
// header line and the footer tally. It is the per-service half of the TUI that
// the single-service header/log pane never showed: which dependency is being
// worked on right now, which are still pending, and which are slow.
type planView struct {
	order  []string              // dependency order, origin last
	status map[string]*svcStatus // keyed by Unique, e.g. "saas/vault"
}

func newPlanView() planView {
	return planView{status: map[string]*svcStatus{}}
}

// setPlan seeds the known services (dependency order, origin last) so the
// "next" frontier can be shown before anything has started.
func (p *planView) setPlan(services []string) {
	p.order = append(p.order[:0], services...)
}

// setState records a phase transition, stamping the time only when the phase
// actually changes so per-phase elapsed stays monotonic across re-emits.
func (p *planView) setState(service string, state ServiceState, now time.Time) {
	s := p.status[service]
	if s == nil {
		s = &svcStatus{}
		p.status[service] = s
		if !p.known(service) {
			p.order = append(p.order, service)
		}
	}
	if s.state != state || s.since.IsZero() {
		s.state = state
		s.since = now
	}
}

func (p *planView) setReady(service string, port int, now time.Time) {
	p.setState(service, StateRunning, now)
	if port > 0 {
		p.status[service].port = port
	}
}

func (p *planView) setFailed(service string, now time.Time) {
	p.setState(service, StateFailed, now)
}

func (p *planView) known(service string) bool {
	return slices.Contains(p.order, service)
}

// active reports whether a state is a non-terminal phase still in flight.
func active(state ServiceState) bool {
	switch state {
	case StateLoading, StateInitializing, StateStarting, StateTesting, StateStopping:
		return true
	}
	return false
}

func (p *planView) counts() (ready, total int) {
	total = len(p.order)
	for _, s := range p.status {
		if s.state == StateRunning {
			ready++
		}
	}
	return ready, total
}

// short trims the module prefix from a Unique ("saas/vault" -> "vault") for a
// less noisy live status; the full Unique still appears in the log pane.
func short(unique string) string {
	if i := strings.LastIndex(unique, "/"); i >= 0 && i+1 < len(unique) {
		return unique[i+1:]
	}
	return unique
}

func fmtElapsed(d time.Duration) string {
	d = max(d.Truncate(time.Second), 0)
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%02d:%02d", m, s)
}

// nowNextLine renders "now: <active services>  ·  next: a → b". Returns "" when
// nothing is in flight and nothing is pending (e.g. everything is ready), so
// the caller can drop the line entirely.
func (p *planView) nowNextLine(now time.Time) string {
	if len(p.order) == 0 {
		return ""
	}
	s := Styles()

	var nowParts []string
	for _, svc := range p.order {
		st := p.status[svc]
		if st == nil || !active(st.state) {
			continue
		}
		seg := fmt.Sprintf("%s %s", short(svc), strings.ToLower(st.state.String()))
		if st.port > 0 {
			seg += fmt.Sprintf("(%d)", st.port)
		}
		seg += " " + fmtElapsed(now.Sub(st.since))
		if now.Sub(st.since) > slowAfter {
			seg = s.LogWarn.Render("⚠ " + seg)
		} else {
			seg = s.Spinner.Render(seg)
		}
		nowParts = append(nowParts, seg)
	}

	var next []string
	for _, svc := range p.order {
		if p.status[svc] == nil { // never started -> pending
			next = append(next, short(svc))
		}
	}

	var b strings.Builder
	if len(nowParts) > 0 {
		b.WriteString(s.Muted.Render("now: "))
		b.WriteString(strings.Join(nowParts, ", "))
	}
	if len(next) > 0 {
		if b.Len() > 0 {
			b.WriteString(s.Muted.Render("  ·  "))
		}
		b.WriteString(s.Muted.Render("next: " + strings.Join(next, " → ")))
	}
	return b.String()
}

// tally renders the footer summary: "N/M ready · <svc> slow · <svc> failed".
func (p *planView) tally(now time.Time) string {
	if len(p.order) == 0 {
		return ""
	}
	ready, total := p.counts()
	parts := []string{fmt.Sprintf("%d/%d ready", ready, total)}
	for _, svc := range p.order {
		st := p.status[svc]
		if st != nil && active(st.state) && now.Sub(st.since) > slowAfter {
			parts = append(parts, short(svc)+" slow")
			break
		}
	}
	for _, svc := range p.order {
		if st := p.status[svc]; st != nil && st.state == StateFailed {
			parts = append(parts, short(svc)+" failed")
			break
		}
	}
	return strings.Join(parts, " · ")
}
