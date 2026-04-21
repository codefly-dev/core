package code

import "sync"

// TrigramIndex provides sub-linear text search by maintaining posting lists
// of trigrams (3-byte sequences) to file IDs. A search query extracts trigrams,
// intersects their posting lists, and only scans the resulting candidate files.
//
// Typical speedup: 10-100x on large repos vs linear scan.
type TrigramIndex struct {
	mu      sync.RWMutex
	posting map[trigram]map[uint32]struct{} // trigram -> set of file IDs
	fileIDs map[string]uint32              // path -> numeric ID
	idFiles map[uint32]string              // numeric ID -> path
	nextID  uint32
}

// trigram is a 3-byte sequence used as an index key.
type trigram [3]byte

// NewTrigramIndex creates an empty trigram index.
func NewTrigramIndex() *TrigramIndex {
	return &TrigramIndex{
		posting: make(map[trigram]map[uint32]struct{}),
		fileIDs: make(map[string]uint32),
		idFiles: make(map[uint32]string),
	}
}

// AddFile indexes the content of a file. If the file was previously indexed,
// it is removed first (full re-index of that file).
func (idx *TrigramIndex) AddFile(path string, content []byte) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Remove old entry if exists.
	if id, ok := idx.fileIDs[path]; ok {
		idx.removeFileID(id)
	}

	id := idx.nextID
	idx.nextID++
	idx.fileIDs[path] = id
	idx.idFiles[id] = path

	// Extract lowercase trigrams for case-insensitive matching.
	// Storing lowercase means case-insensitive queries work naturally.
	// Case-sensitive queries get slightly more candidates but regex catches extras.
	lower := toLowerASCII(content)
	seen := make(map[trigram]struct{})
	for i := 0; i+2 < len(lower); i++ {
		t := trigram{lower[i], lower[i+1], lower[i+2]}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		if idx.posting[t] == nil {
			idx.posting[t] = make(map[uint32]struct{})
		}
		idx.posting[t][id] = struct{}{}
	}
}

// RemoveFile removes a file from the index.
func (idx *TrigramIndex) RemoveFile(path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	id, ok := idx.fileIDs[path]
	if !ok {
		return
	}
	idx.removeFileID(id)
	delete(idx.fileIDs, path)
	delete(idx.idFiles, id)
}

// removeFileID removes a file ID from all posting lists. Must be called with mu held.
func (idx *TrigramIndex) removeFileID(id uint32) {
	for t, files := range idx.posting {
		delete(files, id)
		if len(files) == 0 {
			delete(idx.posting, t)
		}
	}
}

// Query returns candidate file paths that contain ALL trigrams from the pattern.
// For short patterns (<3 bytes), returns all indexed files (no filtering possible).
// The candidates are a superset of actual matches — callers must verify with regex.
func (idx *TrigramIndex) Query(pattern string) []string {
	trigrams := extractQueryTrigrams(pattern)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(trigrams) == 0 {
		// Can't filter — return all files.
		result := make([]string, 0, len(idx.idFiles))
		for _, path := range idx.idFiles {
			result = append(result, path)
		}
		return result
	}

	// Start with the smallest posting list for efficiency.
	var smallest map[uint32]struct{}
	for _, t := range trigrams {
		list := idx.posting[t]
		if list == nil {
			return nil // trigram not in any file = no matches
		}
		if smallest == nil || len(list) < len(smallest) {
			smallest = list
		}
	}

	// Intersect: keep only IDs present in ALL posting lists.
	candidates := make(map[uint32]struct{}, len(smallest))
	for id := range smallest {
		candidates[id] = struct{}{}
	}

	for _, t := range trigrams {
		list := idx.posting[t]
		for id := range candidates {
			if _, ok := list[id]; !ok {
				delete(candidates, id)
			}
		}
		if len(candidates) == 0 {
			return nil
		}
	}

	// Resolve IDs to paths.
	result := make([]string, 0, len(candidates))
	for id := range candidates {
		if path, ok := idx.idFiles[id]; ok {
			result = append(result, path)
		}
	}
	return result
}

// Size returns the number of indexed files.
func (idx *TrigramIndex) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.fileIDs)
}

// extractQueryTrigrams extracts unique trigrams from a search pattern.
// For literal patterns, all trigrams are extracted.
// For regex patterns, extracts trigrams from literal fragments only.
func extractQueryTrigrams(pattern string) []trigram {
	// Extract literal runs (sequences without regex metacharacters).
	literals := extractLiterals(pattern)

	seen := make(map[trigram]struct{})
	var result []trigram
	for _, lit := range literals {
		// Lowercase to match the index (which stores lowercase trigrams).
		lower := toLowerASCII([]byte(lit))
		for i := 0; i+2 < len(lower); i++ {
			t := trigram{lower[i], lower[i+1], lower[i+2]}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			result = append(result, t)
		}
	}
	return result
}

// extractLiterals splits a regex pattern into literal fragments.
// Skips regex metacharacters and character classes.
func extractLiterals(pattern string) []string {
	var literals []string
	var current []byte
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		switch ch {
		case '\\':
			// Escaped character — the next byte is literal.
			if i+1 < len(pattern) {
				next := pattern[i+1]
				// Only treat as literal if it's an escaped metachar.
				if isRegexMeta(next) {
					current = append(current, next)
					i += 2
					continue
				}
			}
			// Regex escape sequence (\d, \w, etc.) — break literal run.
			if len(current) > 0 {
				literals = append(literals, string(current))
				current = current[:0]
			}
			i += 2
		case '.', '*', '+', '?', '|', '^', '$', '(', ')', '{', '}':
			if len(current) > 0 {
				literals = append(literals, string(current))
				current = current[:0]
			}
			i++
		case '[':
			// Skip character class.
			if len(current) > 0 {
				literals = append(literals, string(current))
				current = current[:0]
			}
			for i < len(pattern) && pattern[i] != ']' {
				i++
			}
			i++ // skip ']'
		default:
			current = append(current, ch)
			i++
		}
	}
	if len(current) > 0 {
		literals = append(literals, string(current))
	}
	return literals
}

// toLowerASCII lowercases ASCII bytes in-place. Non-ASCII bytes are unchanged.
// Avoids strings.ToLower allocation for the common case of ASCII source code.
func toLowerASCII(data []byte) []byte {
	out := make([]byte, len(data))
	for i, b := range data {
		if b >= 'A' && b <= 'Z' {
			out[i] = b + 32
		} else {
			out[i] = b
		}
	}
	return out
}

func isRegexMeta(ch byte) bool {
	switch ch {
	case '.', '*', '+', '?', '|', '^', '$', '(', ')', '{', '}', '[', ']', '\\':
		return true
	}
	return false
}
