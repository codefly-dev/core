package code

import (
	"fmt"
	"strconv"
	"strings"
)

// FileDiff represents the structured diff of a single file.
type FileDiff struct {
	OldPath string
	NewPath string
	Hunks   []Hunk
}

// Hunk is a contiguous block of changes within a file diff.
type Hunk struct {
	OldStart int // 1-based starting line in the old version
	OldCount int
	NewStart int // 1-based starting line in the new version
	NewCount int
	Header   string // optional hunk header (function name, etc.)
	Lines    []DiffLine
}

// DiffLine is a single line within a hunk.
type DiffLine struct {
	Kind    DiffKind
	Content string
	OldLine int // 0 if the line doesn't exist in the old file
	NewLine int // 0 if the line doesn't exist in the new file
}

// DiffKind describes whether a line was added, removed, or unchanged.
type DiffKind int

const (
	DiffContext DiffKind = iota // unchanged context line
	DiffAdd                     // added line
	DiffRemove                  // removed line
)

func (k DiffKind) String() string {
	switch k {
	case DiffAdd:
		return "+"
	case DiffRemove:
		return "-"
	default:
		return " "
	}
}

// ParseUnifiedDiff parses the output of `git diff` (unified format) into
// structured FileDiff objects with line numbers on every line.
func ParseUnifiedDiff(raw string) []FileDiff {
	var diffs []FileDiff
	lines := strings.Split(raw, "\n")
	i := 0

	for i < len(lines) {
		// Find next file header.
		if !strings.HasPrefix(lines[i], "diff --git ") {
			i++
			continue
		}

		fd := FileDiff{}
		parts := strings.Fields(lines[i])
		if len(parts) >= 4 {
			fd.OldPath = strings.TrimPrefix(parts[2], "a/")
			fd.NewPath = strings.TrimPrefix(parts[3], "b/")
		}
		i++

		// Skip past file metadata lines (index, ---, +++).
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@") && !strings.HasPrefix(lines[i], "diff --git ") {
			i++
		}

		// Parse hunks.
		for i < len(lines) && strings.HasPrefix(lines[i], "@@") {
			hunk, nextI := parseHunk(lines, i)
			fd.Hunks = append(fd.Hunks, hunk)
			i = nextI
		}

		diffs = append(diffs, fd)
	}

	return diffs
}

func parseHunk(lines []string, start int) (Hunk, int) {
	h := Hunk{}
	header := lines[start]

	// Parse "@@ -oldStart,oldCount +newStart,newCount @@ header"
	at2 := strings.Index(header[2:], "@@")
	if at2 >= 0 {
		nums := header[3 : at2+2]
		h.Header = strings.TrimSpace(header[at2+4:])
		parseHunkRange(nums, &h)
	}

	i := start + 1
	oldLine := h.OldStart
	newLine := h.NewStart

	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff --git ") {
			break
		}

		dl := DiffLine{}
		if strings.HasPrefix(line, "+") {
			dl.Kind = DiffAdd
			dl.Content = line[1:]
			dl.NewLine = newLine
			newLine++
		} else if strings.HasPrefix(line, "-") {
			dl.Kind = DiffRemove
			dl.Content = line[1:]
			dl.OldLine = oldLine
			oldLine++
		} else if strings.HasPrefix(line, " ") {
			dl.Kind = DiffContext
			dl.Content = line[1:]
			dl.OldLine = oldLine
			dl.NewLine = newLine
			oldLine++
			newLine++
		} else if strings.HasPrefix(line, `\`) {
			// "\ No newline at end of file"
			i++
			continue
		} else {
			break
		}

		h.Lines = append(h.Lines, dl)
		i++
	}

	return h, i
}

func parseHunkRange(nums string, h *Hunk) {
	nums = strings.TrimSpace(nums)
	parts := strings.SplitN(nums, " ", 2)
	if len(parts) == 2 {
		parseRange(parts[0], &h.OldStart, &h.OldCount)
		parseRange(parts[1], &h.NewStart, &h.NewCount)
	}
}

func parseRange(s string, start, count *int) {
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "+")
	if comma := strings.Index(s, ","); comma >= 0 {
		*start, _ = strconv.Atoi(s[:comma])
		*count, _ = strconv.Atoi(s[comma+1:])
	} else {
		*start, _ = strconv.Atoi(s)
		*count = 1
	}
}

// Summary returns a concise description of the diff suitable for an LLM prompt.
func (fd *FileDiff) Summary() string {
	added, removed := 0, 0
	for _, h := range fd.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case DiffAdd:
				added++
			case DiffRemove:
				removed++
			}
		}
	}
	return fmt.Sprintf("%s: +%d/-%d (%d hunks)", fd.NewPath, added, removed, len(fd.Hunks))
}
