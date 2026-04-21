package code

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// NativeJJ implements VCSProvider for Jujutsu (jj) repositories.
// All operations go through the jj CLI — there is no Go library for jj.
type NativeJJ struct {
	dir    string // workspace root
	binary string // path to jj binary
}

// OpenNativeJJ returns a NativeJJ if dir contains a .jj directory and jj is installed.
// Returns nil if jj is not available or dir is not a jj repo.
func OpenNativeJJ(dir string) *NativeJJ {
	if _, err := os.Stat(filepath.Join(dir, ".jj")); err != nil {
		return nil
	}
	binary, err := exec.LookPath("jj")
	if err != nil {
		return nil
	}
	return &NativeJJ{dir: dir, binary: binary}
}

func (j *NativeJJ) Log(ctx context.Context, maxCount int, ref, path, since string) ([]*GitCommitInfo, error) {
	if maxCount <= 0 {
		maxCount = 50
	}

	// jj log with a template that outputs fields separated by null bytes.
	// Fields: commit_id, short_commit_id, author, date, description
	tmpl := `commit_id ++ "\x00" ++ change_id.shortest(8) ++ "\x00" ++ author.email() ++ "\x00" ++ committer.timestamp().format("%Y-%m-%dT%H:%M:%S%:z") ++ "\x00" ++ description.first_line() ++ "\n"`

	args := []string{"log", "--no-pager", "--no-graph",
		"--limit", strconv.Itoa(maxCount),
		"--template", tmpl,
	}
	if ref != "" {
		args = append(args, "-r", ref)
	}

	out, err := j.run(ctx, args...)
	if err != nil {
		return nil, err
	}

	var commits []*GitCommitInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 5)
		if len(parts) < 5 {
			continue
		}
		commits = append(commits, &GitCommitInfo{
			Hash:      parts[0],
			ShortHash: parts[1],
			Author:    parts[2],
			Date:      parts[3],
			Message:   parts[4],
		})
	}
	return commits, nil
}

func (j *NativeJJ) Diff(ctx context.Context, baseRef, headRef, path string, contextLines int, statOnly bool) (string, []*GitDiffFileInfo, error) {
	args := []string{"diff", "--no-pager"}
	if baseRef != "" {
		if headRef != "" {
			args = append(args, "--from", baseRef, "--to", headRef)
		} else {
			args = append(args, "--from", baseRef)
		}
	}
	if path != "" {
		args = append(args, "--", path)
	}
	if statOnly {
		args = append(args, "--stat")
	}
	if contextLines > 0 {
		args = append(args, fmt.Sprintf("--context=%d", contextLines))
	}

	out, err := j.run(ctx, args...)
	if err != nil {
		return "", nil, err
	}

	var files []*GitDiffFileInfo
	if statOnly {
		files = parseJJDiffStat(out)
		return "", files, nil
	}
	return out, nil, nil
}

func (j *NativeJJ) Show(ctx context.Context, ref, path string) (string, bool, error) {
	if ref == "" {
		ref = "@" // jj working copy
	}

	args := []string{"file", "show", "--revision", ref, path}
	out, err := j.run(ctx, args...)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "No such path") || strings.Contains(errStr, "not found") {
			return "", false, nil
		}
		return "", false, err
	}
	return out, true, nil
}

func (j *NativeJJ) Blame(ctx context.Context, path string, startLine, endLine int32) ([]*GitBlameLine, error) {
	// jj doesn't have native blame yet. If this is a colocated repo (has .git too),
	// fall back to git blame.
	if _, err := os.Stat(filepath.Join(j.dir, ".git")); err == nil {
		return gitBlameExec(ctx, j.dir, path, startLine, endLine)
	}
	return nil, fmt.Errorf("jj blame not available (no colocated .git repo)")
}

func (j *NativeJJ) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, j.binary, args...)
	cmd.Dir = j.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("jj %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// parseJJDiffStat parses jj diff --stat output.
// Format: " file.go | 5 +++--"
func parseJJDiffStat(output string) []*GitDiffFileInfo {
	var files []*GitDiffFileInfo
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		path := strings.TrimSpace(parts[0])
		if path == "" {
			continue
		}
		// Count + and - in the stat visualization
		stat := parts[1]
		var adds, dels int32
		for _, ch := range stat {
			if ch == '+' {
				adds++
			} else if ch == '-' {
				dels++
			}
		}
		status := "modified"
		if adds > 0 && dels == 0 {
			status = "added"
		} else if adds == 0 && dels > 0 {
			status = "deleted"
		}
		files = append(files, &GitDiffFileInfo{
			Path: path, Additions: adds, Deletions: dels, Status: status,
		})
	}
	return files
}

// gitBlameExec runs git blame as a fallback for jj colocated repos.
func gitBlameExec(ctx context.Context, dir, path string, startLine, endLine int32) ([]*GitBlameLine, error) {
	args := []string{"blame", "--porcelain"}
	if startLine > 0 {
		end := endLine
		if end <= 0 {
			end = startLine + 1000
		}
		args = append(args, fmt.Sprintf("-L%d,%d", startLine, end))
	}
	args = append(args, "--", path)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git blame: %s", strings.TrimSpace(stderr.String()))
	}

	return parseGitBlameOutput(stdout.String()), nil
}

// parseGitBlameOutput parses git blame --porcelain output into GitBlameLine entries.
func parseGitBlameOutput(output string) []*GitBlameLine {
	var result []*GitBlameLine
	var currentHash, currentAuthor, currentDate string
	lineNum := int32(0)
	for _, line := range strings.Split(output, "\n") {
		if len(line) >= 40 && !strings.HasPrefix(line, "\t") {
			parts := strings.Fields(line)
			if len(parts) >= 3 && len(parts[0]) == 40 {
				currentHash = parts[0]
				if n, err := strconv.Atoi(parts[2]); err == nil {
					lineNum = int32(n)
				}
			}
		}
		if strings.HasPrefix(line, "author ") {
			currentAuthor = strings.TrimPrefix(line, "author ")
		}
		if strings.HasPrefix(line, "author-time ") {
			currentDate = strings.TrimPrefix(line, "author-time ")
		}
		if strings.HasPrefix(line, "\t") {
			result = append(result, &GitBlameLine{
				Hash: currentHash, Author: currentAuthor, Date: currentDate,
				Line: lineNum, Content: strings.TrimPrefix(line, "\t"),
			})
		}
	}
	return result
}
