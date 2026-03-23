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

// SmartEdit tries all matching strategies to apply a FIND/REPLACE on content.
// Strategies are tried in order of decreasing precision:
//  1. Exact substring match
//  2. Trailing whitespace normalized
//  3. Fully trimmed (all leading+trailing whitespace per line)
//  4. Indentation-shifted (same content, different indent level)
//  5. Anchor-based (first+last unique lines locate the region)
//  6. Fuzzy scored (Levenshtein per line)
//  7. Fuzzy block (60% line match threshold)
func SmartEdit(content, find, replace string) EditResult {
	if strings.Contains(content, find) {
		return EditResult{
			Content:  strings.Replace(content, find, replace, 1),
			Strategy: "exact",
			OK:       true,
		}
	}
	if updated, ok := tryNormReplace(content, find, replace, normTrailing); ok {
		return EditResult{Content: updated, Strategy: "trailing", OK: true}
	}
	if updated, ok := tryNormReplace(content, find, replace, normFull); ok {
		return EditResult{Content: updated, Strategy: "trimmed", OK: true}
	}
	if updated, ok := tryIndentShifted(content, find, replace); ok {
		return EditResult{Content: updated, Strategy: "indent_shifted", OK: true}
	}
	if updated, ok := tryAnchor(content, find, replace); ok {
		return EditResult{Content: updated, Strategy: "anchor", OK: true}
	}
	if updated, ok := fuzzyScore(content, find, replace); ok {
		return EditResult{Content: updated, Strategy: "fuzzy_score", OK: true}
	}
	if updated, ok := fuzzyBlock(content, find, replace); ok {
		return EditResult{Content: updated, Strategy: "fuzzy_block", OK: true}
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

func tryNormReplace(content, find, replace string, norm func(string) string) (string, bool) {
	contentLines := strings.Split(content, "\n")
	nf := norm(find)
	findLines := strings.Split(nf, "\n")

	if nf == "" {
		return "", false
	}

	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		match := true
		for j := 0; j < len(findLines); j++ {
			if norm(contentLines[i+j]) != findLines[j] {
				match = false
				break
			}
		}
		if match {
			var result []string
			result = append(result, contentLines[:i]...)
			result = append(result, strings.Split(replace, "\n")...)
			result = append(result, contentLines[i+len(findLines):]...)
			return strings.Join(result, "\n"), true
		}
	}
	return "", false
}

// ─── Strategy 4: Indentation-shifted ─────────────────────────

func tryIndentShifted(content, find, replace string) (string, bool) {
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	n := len(findLines)

	if n == 0 || n > len(contentLines) {
		return "", false
	}

	dedentedFind := DedentLines(findLines)

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
			var result []string
			result = append(result, contentLines[:i]...)
			result = append(result, strings.Split(replace, "\n")...)
			result = append(result, contentLines[i+n:]...)
			return strings.Join(result, "\n"), true
		}
	}
	return "", false
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

func tryAnchor(content, find, replace string) (string, bool) {
	findLines := strings.Split(find, "\n")
	contentLines := strings.Split(content, "\n")
	n := len(findLines)

	if n < 3 {
		return "", false
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
		return "", false
	}

	firstIdx := -1
	for i, l := range contentLines {
		if strings.TrimSpace(l) == firstAnchor && firstIdx < 0 {
			firstIdx = i
		}
		if firstIdx >= 0 && strings.TrimSpace(l) == lastAnchor && i > firstIdx {
			span := i - firstIdx + 1
			if span >= n*7/10 && span <= n*13/10 {
				var result []string
				result = append(result, contentLines[:firstIdx]...)
				result = append(result, strings.Split(replace, "\n")...)
				result = append(result, contentLines[i+1:]...)
				return strings.Join(result, "\n"), true
			}
		}
	}
	return "", false
}

// ─── Strategy 6: Fuzzy scored (Levenshtein) ──────────────────

func fuzzyScore(content, find, replace string) (string, bool) {
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	n := len(findLines)

	if n == 0 || n > len(contentLines) {
		return "", false
	}

	bestIdx, bestDist := -1, -1

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
		}
	}

	if bestIdx < 0 || bestDist/n > 3 {
		return "", false
	}

	var result []string
	result = append(result, contentLines[:bestIdx]...)
	result = append(result, strings.Split(replace, "\n")...)
	result = append(result, contentLines[bestIdx+n:]...)
	return strings.Join(result, "\n"), true
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

func fuzzyBlock(content, find, replace string) (string, bool) {
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	n := len(findLines)

	if n == 0 || n > len(contentLines) {
		return "", false
	}

	bestIdx, bestScore := -1, 0

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
		}
	}

	threshold := (n * 6) / 10
	if threshold < 1 {
		threshold = 1
	}
	if bestScore < threshold {
		return "", false
	}

	var result []string
	result = append(result, contentLines[:bestIdx]...)
	result = append(result, strings.Split(replace, "\n")...)
	result = append(result, contentLines[bestIdx+n:]...)
	return strings.Join(result, "\n"), true
}
