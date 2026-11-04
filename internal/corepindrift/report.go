package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// report is everything one run concluded.
type report struct {
	latest    string
	now       time.Time
	warnAfter time.Duration
	failAfter time.Duration
	// scanned is every go.mod found, including those not pinning core, so the
	// reader can tell "nothing is behind" from "nothing was looked at".
	scanned  int
	findings []finding
}

func (r report) failed() bool {
	for _, found := range r.findings {
		if found.level == levelFail {
			return true
		}
	}
	return false
}

// worst is the highest level any finding reached.
func (r report) worst() level {
	worst := levelOK
	for _, found := range r.findings {
		if found.level > worst {
			worst = found.level
		}
	}
	return worst
}

// counts tallies findings by level.
func (r report) counts() (ok, warn, failing int) {
	for _, found := range r.findings {
		switch found.level {
		case levelFail:
			failing++
		case levelWarn:
			warn++
		default:
			ok++
		}
	}
	return ok, warn, failing
}

// sorted returns the findings worst-first, then by the oldest clock, then by
// path. The reader's first line is the thing that needs doing most.
func (r report) sorted() []finding {
	out := make([]finding, len(r.findings))
	copy(out, r.findings)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].level != out[j].level {
			return out[i].level > out[j].level
		}
		if out[i].staleFor != out[j].staleFor {
			return out[i].staleFor > out[j].staleFor
		}
		return out[i].goMod.file < out[j].goMod.file
	})
	return out
}

// remedyGroup is one instruction and every file it applies to.
type remedyGroup struct {
	remedy string
	files  []string
}

// remedies groups the findings that need action by the action they need, worst
// group first.
func (r report) remedies() []remedyGroup {
	var groups []remedyGroup
	index := map[string]int{}
	for _, found := range r.sorted() {
		if found.level == levelOK || found.remedy == "" {
			continue
		}
		at, seen := index[found.remedy]
		if !seen {
			index[found.remedy] = len(groups)
			groups = append(groups, remedyGroup{remedy: found.remedy})
			at = len(groups) - 1
		}
		groups[at].files = append(groups[at].files, found.goMod.file)
	}
	return groups
}

// render writes the log output.
//
// Every finding carries its own numbers — the pinned version, core's newest
// release, how far behind, how long it has been behind, and what to change —
// because a red check whose message does not say what to change is how a gate
// gets ignored into uselessness (codefly-dev/core#891). The same numbers go
// out as GitHub annotations, so they appear on the pull request's Files view
// against the go.mod line rather than only in a log somebody has to open.
func render(out io.Writer, r report) {
	fmt.Fprintf(out, "core pin drift — %s\n", corePath)
	fmt.Fprintf(out, "  core's newest release : %s\n", r.latest)
	fmt.Fprintf(out, "  measured at           : %s\n", r.now.Format(time.RFC3339))
	fmt.Fprintf(out, "  thresholds            : warn at %s behind, fail at %s behind "+
		"(on how long an update has been AVAILABLE, not on how many)\n",
		humanDuration(r.warnAfter), humanDuration(r.failAfter))
	fmt.Fprintf(out, "  go.mod files          : %d found, %d pinning core\n\n",
		r.scanned, len(r.findings))

	for _, found := range r.sorted() {
		fmt.Fprintf(out, "  [%-4s] %s\n", found.level, found.goMod.file)
		fmt.Fprintf(out, "         %s\n", wrap(found.describe(), 9))
	}

	// The remedies are grouped rather than repeated under each finding: a
	// repository with eleven modules on three pins gets three instructions,
	// not eleven copies of the same paragraph. The annotations below still
	// carry the remedy individually, because each one is read on its own in
	// the Files view with no footer in sight.
	if remedies := r.remedies(); len(remedies) > 0 {
		fmt.Fprintf(out, "\nWhat to do\n")
		for _, group := range remedies {
			fmt.Fprintf(out, "\n  %s\n", wrap(group.remedy, 2))
			for _, file := range group.files {
				fmt.Fprintf(out, "    - %s\n", file)
			}
		}
	}

	fmt.Fprintln(out)
	for _, found := range r.sorted() {
		if found.level == levelOK {
			continue
		}
		annotate(out, found)
	}

	ok, warn, failing := r.counts()
	fmt.Fprintf(out, "%d up to date, %d warning, %d failing (of %d modules pinning %s)\n",
		ok, warn, failing, len(r.findings), corePath)

	switch r.worst() {
	case levelFail:
		fmt.Fprintf(out,
			"\nFAIL: at least one module has been behind core for %s or more, or is "+
				"pinned to something core cannot resolve.\n"+
				"Core owns the Work Context every service verifies on every call, so a "+
				"verification rule tightened in core is true here only once these pins "+
				"move. Core's newest release is %s.\n",
			humanDuration(r.failAfter), r.latest)
	case levelWarn:
		fmt.Fprintf(out,
			"\nWARNING ONLY — this check passes. Nothing has been behind for %s yet, "+
				"which is when it starts failing. Core's newest release is %s.\n",
			humanDuration(r.failAfter), r.latest)
	default:
		fmt.Fprintf(out, "\nEvery module is on core %s.\n", r.latest)
	}
}

// describe is the finding's one-line statement with its numbers in it.
func (f finding) describe() string {
	parts := []string{f.detail}
	if f.goMod.indirect && f.kind != kindExempt {
		parts = append(parts, "(an indirect requirement)")
	}
	if f.unverified != "" {
		parts = append(parts, "CAUTION: "+f.unverified)
	}
	if f.degraded != nil {
		parts = append(parts, fmt.Sprintf(
			"CAUTION: this go.mod could only be read loosely (%v), so a replace "+
				"directive in it was not seen and this verdict may be about a core "+
				"this module does not build against", f.degraded))
	}
	return strings.Join(parts, " ")
}

// annotate emits a GitHub workflow command so the finding lands on the go.mod
// line in the Files view. A `file=` annotation with no matching line still
// renders in the Checks summary, so a module whose line could not be read is
// not silently dropped.
func annotate(out io.Writer, f finding) {
	command := "warning"
	if f.level == levelFail {
		command = "error"
	}
	title := fmt.Sprintf("core pin %s", f.kind)
	message := f.describe()
	if f.remedy != "" {
		message += " — " + f.remedy
	}
	location := "file=" + f.goMod.file
	if f.goMod.line > 0 {
		location += fmt.Sprintf(",line=%d", f.goMod.line)
	}
	fmt.Fprintf(out, "::%s %s,title=%s::%s\n",
		command, location, escapeProperty(title), escapeData(message))
}

// escapeData and escapeProperty apply GitHub's workflow-command escaping. A
// raw newline would end the command and drop everything after it, which is how
// a multi-line failure message turns into a one-word annotation.
func escapeData(value string) string {
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, "\r", "%0D")
	return strings.ReplaceAll(value, "\n", "%0A")
}

func escapeProperty(value string) string {
	value = escapeData(value)
	value = strings.ReplaceAll(value, ":", "%3A")
	return strings.ReplaceAll(value, ",", "%2C")
}

// writeStepSummary appends the findings as a Markdown table, so the numbers
// are on the run's summary page rather than inside a log.
func writeStepSummary(path string, r report) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	builder := &strings.Builder{}
	fmt.Fprintf(builder, "## core pin drift\n\n")
	fmt.Fprintf(builder, "Core's newest release is **%s**. Failing at %s behind; "+
		"warning from %s.\n\n", r.latest, humanDuration(r.failAfter), humanDuration(r.warnAfter))
	fmt.Fprintf(builder, "| | module | pinned | behind | behind for | what to do |\n")
	fmt.Fprintf(builder, "|---|---|---|---|---|---|\n")
	for _, found := range r.sorted() {
		mark := map[level]string{levelOK: "✅", levelWarn: "⚠️", levelFail: "❌"}[found.level]
		behind := fmt.Sprintf("%d", found.behind)
		if found.kind == kindExempt {
			behind = "—"
		}
		stale := "—"
		if !found.since.IsZero() {
			stale = humanDuration(found.staleFor)
		}
		remedy := found.remedy
		if found.level == levelOK {
			remedy = found.detail
		}
		fmt.Fprintf(builder, "| %s | `%s` | `%s` | %s | %s | %s |\n",
			mark, found.goMod.file, found.pin, behind, stale, cell(remedy))
	}
	_, err = file.WriteString(builder.String())
	return err
}

// cell makes a string safe for one Markdown table cell.
func cell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.Join(strings.Fields(value), " ")
}

// wrap re-flows a message to fit a terminal, indenting continuation lines so a
// multi-sentence finding stays readable in the Actions log.
func wrap(text string, indent int) string {
	const width = 76
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		if len(lines[last])+1+len(word) > width {
			lines = append(lines, word)
			continue
		}
		lines[last] += " " + word
	}
	return strings.Join(lines, "\n"+strings.Repeat(" ", indent))
}
