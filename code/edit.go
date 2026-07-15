// Package code provides shared implementations for Code service RPCs.
//
// Plugins call into this library instead of reimplementing common logic.
// Language-specific behavior (e.g. which fixers to run) stays in the plugin.
package code

import "strings"

// EditResult holds the outcome of a smart edit attempt.
type EditResult struct {
	Content  string // the file after replacement
	Strategy string // which strategy matched
	OK       bool
}

// SmartEditOptions controls strategies that may replace text which is not
// equivalent to the FIND block. Approximate matching is disabled by default.
type SmartEditOptions struct {
	AllowFuzzy bool
}

type editAttempt struct {
	content   string
	matched   bool
	ambiguous bool
}

type editStrategy struct {
	name string
	try  func() editAttempt
}

// SmartEdit applies only exact/equivalent FIND strategies. Use
// SmartEditWithOptions with AllowFuzzy for explicit approximate recovery.
func SmartEdit(content, find, replace string) EditResult {
	return SmartEditWithOptions(content, find, replace, SmartEditOptions{})
}

// SmartEditWithOptions tries matching strategies in decreasing precision:
//  1. Exact substring match
//  2. Trailing whitespace normalized
//  3. Fully trimmed (all leading+trailing whitespace per line)
//  4. Indentation-shifted (same content, different indent level)
//
// When AllowFuzzy is true it additionally enables:
//  5. Anchor-based (first+last unique lines locate the region)
//  6. Fuzzy scored (Levenshtein per line)
//  7. Fuzzy block (60% line match threshold)
func SmartEditWithOptions(content, find, replace string, opts SmartEditOptions) EditResult {
	if find == "" {
		return EditResult{OK: false}
	}
	if count := strings.Count(content, find); count > 1 {
		return EditResult{Strategy: "ambiguous", OK: false}
	} else if count == 1 {
		return EditResult{
			Content:  strings.Replace(content, find, replace, 1),
			Strategy: "exact",
			OK:       true,
		}
	}
	strategies := []editStrategy{
		{"trailing", func() editAttempt { return tryNormReplace(content, find, replace, normTrailing) }},
		{"trimmed", func() editAttempt { return tryNormReplace(content, find, replace, normFull) }},
		{"indent_shifted", func() editAttempt { return tryIndentShifted(content, find, replace) }},
	}
	if opts.AllowFuzzy {
		strategies = append(strategies,
			editStrategy{"anchor", func() editAttempt { return tryAnchor(content, find, replace) }},
			editStrategy{"fuzzy_score", func() editAttempt { return fuzzyScore(content, find, replace) }},
			editStrategy{"fuzzy_block", func() editAttempt { return fuzzyBlock(content, find, replace) }},
		)
	}
	for _, strategy := range strategies {
		attempt := strategy.try()
		if attempt.ambiguous {
			return EditResult{Strategy: "ambiguous", OK: false}
		}
		if attempt.matched {
			return EditResult{Content: attempt.content, Strategy: strategy.name, OK: true}
		}
	}
	return EditResult{OK: false}
}

// ─── Normalizers ─────────────────────────────────────────────

func normTrailing(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}

func normFull(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}

// ─── Strategy 2/3: Normalized replace ────────────────────────

func tryNormReplace(content, find, replace string, norm func(string) string) editAttempt {
	contentLines := strings.Split(content, "\n")
	nf := norm(find)
	findLines := strings.Split(nf, "\n")

	if nf == "" {
		return editAttempt{}
	}

	matchIdx := -1
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		match := true
		for j := 0; j < len(findLines); j++ {
			if norm(contentLines[i+j]) != findLines[j] {
				match = false
				break
			}
		}
		if match {
			if matchIdx >= 0 {
				return editAttempt{ambiguous: true}
			}
			matchIdx = i
		}
	}
	return replaceLines(contentLines, matchIdx, len(findLines), replace)
}

// ─── Strategy 4: Indentation-shifted ─────────────────────────

func tryIndentShifted(content, find, replace string) editAttempt {
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	n := len(findLines)

	if n == 0 || n > len(contentLines) {
		return editAttempt{}
	}

	dedentedFind := DedentLines(findLines)

	matchIdx := -1
	for i := 0; i <= len(contentLines)-n; i++ {
		slice := contentLines[i : i+n]
		dedentedSlice := DedentLines(slice)

		match := true
		for j := 0; j < n; j++ {
			if dedentedSlice[j] != dedentedFind[j] {
				match = false
				break
			}
		}
		if match {
			if matchIdx >= 0 {
				return editAttempt{ambiguous: true}
			}
			matchIdx = i
		}
	}
	return replaceLines(contentLines, matchIdx, n, replace)
}

// DedentLines strips the minimum common leading whitespace from all non-empty lines.
func DedentLines(lines []string) []string {
	minIndent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if len(l) >= minIndent {
			out[i] = l[minIndent:]
		} else {
			out[i] = strings.TrimLeft(l, " \t")
		}
	}
	return out
}

// ─── Strategy 5: Anchor-based ────────────────────────────────

func tryAnchor(content, find, replace string) editAttempt {
	findLines := strings.Split(find, "\n")
	contentLines := strings.Split(content, "\n")
	n := len(findLines)

	if n < 3 {
		return editAttempt{}
	}

	firstAnchor, lastAnchor := "", ""
	for _, l := range findLines {
		if strings.TrimSpace(l) != "" {
			firstAnchor = strings.TrimSpace(l)
			break
		}
	}
	for i := len(findLines) - 1; i >= 0; i-- {
		if strings.TrimSpace(findLines[i]) != "" {
			lastAnchor = strings.TrimSpace(findLines[i])
			break
		}
	}

	if firstAnchor == "" || lastAnchor == "" || firstAnchor == lastAnchor {
		return editAttempt{}
	}

	matchStart, matchSpan := -1, 0
	for start, line := range contentLines {
		if strings.TrimSpace(line) != firstAnchor {
			continue
		}
		for end := start + 1; end < len(contentLines); end++ {
			span := end - start + 1
			if span > n*13/10 {
				break
			}
			if strings.TrimSpace(contentLines[end]) == lastAnchor {
				if span >= n*7/10 && span <= n*13/10 {
					if matchStart >= 0 {
						return editAttempt{ambiguous: true}
					}
					matchStart, matchSpan = start, span
				}
			}
		}
	}
	return replaceLines(contentLines, matchStart, matchSpan, replace)
}

// ─── Strategy 6: Fuzzy scored (Levenshtein) ──────────────────

func fuzzyScore(content, find, replace string) editAttempt {
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	n := len(findLines)

	if n == 0 || n > len(contentLines) {
		return editAttempt{}
	}

	bestIdx, bestDist := -1, -1
	tied := false

	for i := 0; i <= len(contentLines)-n; i++ {
		totalDist := 0
		abort := false
		for j := 0; j < n; j++ {
			cl := strings.TrimSpace(contentLines[i+j])
			fl := strings.TrimSpace(findLines[j])
			d := Levenshtein(cl, fl)
			totalDist += d
			maxLen := len(cl)
			if len(fl) > maxLen {
				maxLen = len(fl)
			}
			if maxLen > 0 && d > maxLen/3 {
				abort = true
				break
			}
		}
		if abort {
			continue
		}
		if bestDist < 0 || totalDist < bestDist {
			bestDist = totalDist
			bestIdx = i
			tied = false
		} else if totalDist == bestDist {
			tied = true
		}
	}

	if bestIdx < 0 || bestDist > n*3 {
		return editAttempt{}
	}
	if tied {
		return editAttempt{ambiguous: true}
	}
	return replaceLines(contentLines, bestIdx, n, replace)
}

// Levenshtein computes the edit distance between two strings.
func Levenshtein(a, b string) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// ─── Strategy 7: Fuzzy block (60% threshold) ─────────────────

func fuzzyBlock(content, find, replace string) editAttempt {
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	n := len(findLines)

	if n == 0 || n > len(contentLines) {
		return editAttempt{}
	}

	bestIdx, bestScore := -1, 0
	tied := false

	for i := 0; i <= len(contentLines)-n; i++ {
		score := 0
		for j := 0; j < n; j++ {
			if strings.TrimSpace(contentLines[i+j]) == strings.TrimSpace(findLines[j]) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
			tied = false
		} else if score == bestScore && score > 0 {
			tied = true
		}
	}

	threshold := (n*6 + 9) / 10 // ceil(60%); two lines require two matches.
	if threshold < 1 {
		threshold = 1
	}
	if bestScore < threshold {
		return editAttempt{}
	}
	if tied {
		return editAttempt{ambiguous: true}
	}
	return replaceLines(contentLines, bestIdx, n, replace)
}

func replaceLines(contentLines []string, start, count int, replace string) editAttempt {
	if start < 0 || count <= 0 {
		return editAttempt{}
	}
	var result []string
	result = append(result, contentLines[:start]...)
	result = append(result, strings.Split(replace, "\n")...)
	result = append(result, contentLines[start+count:]...)
	return editAttempt{content: strings.Join(result, "\n"), matched: true}
}
