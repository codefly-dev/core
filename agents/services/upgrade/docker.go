package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// Docker reports a tag bump for a stock image (e.g. postgres:16 → 16.4)
// by querying the Docker Hub registry for available tags. No actual
// pull or rebuild — Docker-only agents (postgres, redis, etc.) read
// the chosen tag from settings, so the only state to update is the
// agent settings file. This function returns the change set; the
// caller (the agent's Builder.Upgrade) is responsible for persisting
// the new tag in service settings.
//
// Strategy: if the current tag looks like a major (e.g. "16"), bump
// to the latest minor within that major. If it looks like a full
// semver (e.g. "16.2.1"), bump within the same major unless
// IncludeMajor.
//
// curl is used directly so we don't pull in a Docker registry SDK;
// missing curl → return empty result.
func Docker(ctx context.Context, image string, opts Options) (*Result, error) {
	if image == "" || !have("curl") {
		return &Result{}, nil
	}
	repo, current := splitImage(image)
	if repo == "" {
		return &Result{}, nil
	}

	tags, err := dockerHubTags(ctx, repo)
	if err != nil || len(tags) == 0 {
		return &Result{}, nil //nolint:nilerr // soft-fail by design
	}

	target := pickBumpTag(current, tags, opts.IncludeMajor)
	if target == "" || target == current {
		return &Result{}, nil
	}
	change := &builderv0.UpgradeChange{
		Package: repo, From: current, To: target,
	}
	return &Result{Changes: []*builderv0.UpgradeChange{change}}, nil
}

// splitImage splits "postgres:16" → ("library/postgres", "16").
// Bare names go under library/. Repos with a / are passed through.
func splitImage(image string) (repo, tag string) {
	parts := strings.SplitN(image, ":", 2)
	repo = parts[0]
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	if len(parts) == 2 {
		tag = parts[1]
	} else {
		tag = "latest"
	}
	return
}

// dockerHubTags lists tags from the Docker Hub v2 registry for repo.
// Pagination capped at 100 tags — sufficient for picking the next
// stable release of well-known images.
func dockerHubTags(ctx context.Context, repo string) ([]string, error) {
	url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/tags/?page_size=100&ordering=last_updated", repo)
	out, err := runCmd(ctx, "", "curl", "-fsSL", url)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	tags := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		tags = append(tags, r.Name)
	}
	return tags, nil
}

// pickBumpTag picks the highest tag in tags that's a valid bump from
// current. If includeMajor is false, restrict to the same numeric
// leading component as current.
//
// "Highest" is a string-aware semver compare: "16.4.0" > "16.3.5".
// Tags that don't parse as semver (e.g. "alpine", "16-bookworm") are
// excluded — too risky to bump across distro variants automatically.
func pickBumpTag(current string, tags []string, includeMajor bool) string {
	currentMajor := majorOf(current)
	type tv struct {
		raw string
		v   [3]int
	}
	var candidates []tv
	for _, t := range tags {
		v, ok := parseSemverPrefix(t)
		if !ok {
			continue
		}
		if !includeMajor && currentMajor != "" && fmt.Sprintf("%d", v[0]) != currentMajor {
			continue
		}
		candidates = append(candidates, tv{t, v})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return semverLess(candidates[j].v, candidates[i].v) // desc
	})
	return candidates[0].raw
}

// parseSemverPrefix accepts "16", "16.2", "16.2.1" and returns [16,2,1].
// Anything with a non-numeric trailing component ("16-alpine") is rejected.
func parseSemverPrefix(s string) ([3]int, bool) {
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		if p == "" {
			return [3]int{}, false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return [3]int{}, false
			}
			n = n*10 + int(c-'0')
		}
		v[i] = n
	}
	return v, true
}

func semverLess(a, b [3]int) bool {
	for i := range 3 {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

