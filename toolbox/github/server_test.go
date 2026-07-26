package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v89/github"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	gatewayv1 "github.com/codefly-dev/core/generated/go/mind/gateway/v1"
)

func TestNormalizeWebhookVerifiesAndProducesReconciliableMerge(t *testing.T) {
	payload := []byte(`{
		"action":"closed",
		"number":42,
		"pull_request":{"number":42,"merged":true,"html_url":"https://github.test/o/r/pull/42","head":{"sha":"abc"}},
		"repository":{"name":"r","owner":{"login":"o"}},
		"sender":{"login":"octocat"}
	}`)
	secret := "webhook-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	signature := fmt.Sprintf("sha256=%x", mac.Sum(nil))
	server := New("/tmp/x", "", "test")
	event, err := server.NormalizeWebhook(&gatewayv1.ForgeNormalizeWebhookRequest{
		Provider: "github", EventType: "pull_request", DeliveryId: "delivery-1",
		Payload: payload, Signature: signature, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := event.GetEventId(), "forge:github:o/r:pull_request:42:merged"; got != want {
		t.Fatalf("event id = %q, want %q", got, want)
	}
	if event.GetState() != "merged" || event.GetRef() != "forge:github:o/r:pull_request:42" {
		t.Fatalf("event = %+v", event)
	}
	if _, err := server.NormalizeWebhook(&gatewayv1.ForgeNormalizeWebhookRequest{
		Provider: "github", EventType: "pull_request", Payload: payload,
		Signature: "sha256=invalid", Secret: secret,
	}); err == nil {
		t.Fatal("invalid signature was accepted")
	}
}

func TestIdentity(t *testing.T) {
	id, err := New("/tmp/x", "", "test").Identity(context.Background(), &toolboxv0.IdentityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "github" {
		t.Errorf("name = %q, want github", id.Name)
	}
}

func TestParseGitHubRemote(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:codefly-dev/gortk.git":    {"codefly-dev", "gortk"},
		"https://github.com/codefly-dev/core.git": {"codefly-dev", "core"},
		"https://github.com/owner/repo":           {"owner", "repo"},
	}
	for url, want := range cases {
		o, r, err := parseGitHubRemote(url)
		if err != nil || o != want[0] || r != want[1] {
			t.Errorf("%s -> (%q,%q,%v), want %v", url, o, r, err, want)
		}
	}
	if _, _, err := parseGitHubRemote("https://gitlab.com/x/y"); err == nil {
		t.Error("expected error for non-github remote")
	}
}

func TestToolsWellFormed(t *testing.T) {
	tools := New("/tmp/x", "", "test").Tools()
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(tools))
	}
	for _, tool := range tools {
		if tool.Name == "" || tool.Handler == nil || tool.InputSchema == nil {
			t.Errorf("tool %q missing name/handler/schema", tool.Name)
		}
		if len(tool.Examples) == 0 {
			t.Errorf("tool %q has no examples", tool.Name)
		}
	}
}

func TestPullRequestStatusReturnsRequiredChecksAndReviews(t *testing.T) {
	s := githubServerForTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/pulls/42":
			writeJSON(t, w, map[string]any{
				"number": 42, "state": "open", "mergeable": true, "mergeable_state": "clean",
				"html_url": "https://github.test/o/r/pull/42",
				"head":     map[string]any{"sha": "abc123"},
				"base":     map[string]any{"ref": "main"},
			})
		case "/repos/o/r/branches/main/protection":
			writeJSON(t, w, map[string]any{
				"required_status_checks":        map[string]any{"strict": true, "checks": []map[string]any{{"context": "test", "app_id": 123}}},
				"required_pull_request_reviews": map[string]any{"required_approving_review_count": 1},
			})
		case "/repos/o/r/commits/abc123/check-runs":
			writeJSON(t, w, map[string]any{"total_count": 1, "check_runs": []map[string]any{{
				"id": 7, "name": "test", "status": "completed", "conclusion": "success", "html_url": "https://github.test/check/7",
			}}})
		case "/repos/o/r/commits/abc123/status":
			writeJSON(t, w, map[string]any{"state": "success", "statuses": []any{}})
		case "/repos/o/r/pulls/42/reviews":
			writeJSON(t, w, []map[string]any{{"id": 9, "state": "APPROVED", "user": map[string]any{"login": "alice"}, "submitted_at": "2026-07-25T12:00:00Z"}})
		default:
			http.NotFound(w, r)
		}
	})

	status, err := s.PullRequestStatus(t.Context(), &gatewayv1.ForgeRepository{Provider: "github", Owner: "o", Name: "r"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if status.GetBlockingReason() != "" || !status.GetMergeable() {
		t.Fatalf("status unexpectedly blocked: %+v", status)
	}
	if len(status.GetChecks()) != 1 || !status.GetChecks()[0].GetRequired() {
		t.Fatalf("required checks = %+v", status.GetChecks())
	}
	if len(status.GetReviews()) != 1 || status.GetReviews()[0].GetState() != "approved" {
		t.Fatalf("reviews = %+v", status.GetReviews())
	}
}

func TestMergePullRequestReturnsStableAuthoritativeAct(t *testing.T) {
	s := githubServerForTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r/pulls/42" && r.Method == http.MethodGet:
			writeJSON(t, w, map[string]any{
				"number": 42, "state": "open", "mergeable": true, "mergeable_state": "clean",
				"html_url": "https://github.test/o/r/pull/42",
				"head":     map[string]any{"sha": "abc123"},
				"base":     map[string]any{"ref": "main"},
			})
		case r.URL.Path == "/repos/o/r/branches/main/protection":
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]any{"message": "Not Found"})
		case r.URL.Path == "/repos/o/r/commits/abc123/check-runs":
			writeJSON(t, w, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.URL.Path == "/repos/o/r/commits/abc123/status":
			writeJSON(t, w, map[string]any{"state": "success", "statuses": []any{}})
		case r.URL.Path == "/repos/o/r/pulls/42/reviews":
			writeJSON(t, w, []any{})
		case r.URL.Path == "/repos/o/r/pulls/42/merge" && r.Method == http.MethodPut:
			writeJSON(t, w, map[string]any{"sha": "merged456", "merged": true, "message": "Pull Request successfully merged"})
		default:
			http.NotFound(w, r)
		}
	})

	result, err := s.MergePullRequest(t.Context(), &gatewayv1.ForgeMergePullRequestRequest{
		Repository:  &gatewayv1.ForgeRepository{Provider: "github", Owner: "o", Name: "r"},
		Number:      42,
		Method:      gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_SQUASH,
		CheckPolicy: gatewayv1.ForgeCheckPolicy_FORGE_CHECK_POLICY_REQUIRE_PASSING,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.GetSuccess() || result.GetAct().GetRevision() != "merged456" {
		t.Fatalf("merge result = %+v", result)
	}
	if got, want := result.GetAct().GetEventId(), "forge:github:o/r:pull_request:42:merged"; got != want {
		t.Fatalf("event id = %q, want %q", got, want)
	}
}

func TestRequestReviewReturnsProviderAcceptedReviewers(t *testing.T) {
	s := githubServerForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls/42/requested_reviewers" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"html_url":            "https://github.test/o/r/pull/42",
			"requested_reviewers": []map[string]any{{"login": "alice"}},
			"requested_teams":     []map[string]any{{"slug": "platform"}},
		})
	})
	result, err := s.RequestReview(t.Context(), &gatewayv1.ForgeRequestReviewRequest{
		Repository:    &gatewayv1.ForgeRepository{Provider: "github", Owner: "o", Name: "r"},
		Number:        42,
		Reviewers:     []string{"alice"},
		TeamReviewers: []string{"platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.GetSuccess() || len(result.GetReviewers()) != 1 || len(result.GetTeamReviewers()) != 1 {
		t.Fatalf("review result = %+v", result)
	}
}

func TestNormalizeWebhookStatusPendingIsInProgress(t *testing.T) {
	payload := []byte(`{
		"id":55,"sha":"abc","state":"pending","context":"ci/test",
		"target_url":"https://github.test/status",
		"repository":{"name":"r","owner":{"login":"o"}}
	}`)
	secret := "webhook-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	signature := fmt.Sprintf("sha256=%x", mac.Sum(nil))
	event, err := New("/tmp/x", "", "test").NormalizeWebhook(&gatewayv1.ForgeNormalizeWebhookRequest{
		Provider: "github", EventType: "status", Payload: payload, Signature: signature, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.GetState() != "in_progress" || event.GetConclusion() != "" {
		t.Fatalf("pending status normalized as state=%q conclusion=%q, want in_progress/empty", event.GetState(), event.GetConclusion())
	}
	if got, want := event.GetEventId(), "forge:github:o/r:status:55:pending"; got != want {
		t.Fatalf("event id = %q, want %q", got, want)
	}
}

func TestPullRequestStatusPrefersCheckRunOverStatusMirror(t *testing.T) {
	s := githubServerForTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/pulls/42":
			writeJSON(t, w, map[string]any{
				"number": 42, "state": "open", "mergeable": true, "mergeable_state": "clean",
				"head": map[string]any{"sha": "abc123"}, "base": map[string]any{"ref": "main"},
			})
		case "/repos/o/r/branches/main/protection":
			writeJSON(t, w, map[string]any{"required_status_checks": map[string]any{"contexts": []string{"ci"}}})
		case "/repos/o/r/commits/abc123/check-runs":
			writeJSON(t, w, map[string]any{"total_count": 1, "check_runs": []map[string]any{{
				"id": 7, "name": "ci", "status": "completed", "conclusion": "success",
			}}})
		case "/repos/o/r/commits/abc123/status":
			// Legacy mirror of the same context, still pending; must not win.
			writeJSON(t, w, map[string]any{"state": "pending", "statuses": []map[string]any{{
				"id": 100, "context": "ci", "state": "pending",
			}}})
		case "/repos/o/r/pulls/42/reviews":
			writeJSON(t, w, []any{})
		default:
			http.NotFound(w, r)
		}
	})
	status, err := s.PullRequestStatus(t.Context(), &gatewayv1.ForgeRepository{Owner: "o", Name: "r"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.GetChecks()) != 1 {
		t.Fatalf("expected the duplicate context to collapse to one check, got %+v", status.GetChecks())
	}
	if got := status.GetChecks()[0]; got.GetName() != "ci" || got.GetState() != "completed" {
		t.Fatalf("kept check = %+v, want the completed check run", got)
	}
	if status.GetBlockingReason() != "" {
		t.Fatalf("passing check run should not be blocked by its pending mirror: %q", status.GetBlockingReason())
	}
}

func TestPullRequestStatusBlocksWhileMergeabilityComputing(t *testing.T) {
	s := githubServerForTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/pulls/42":
			// No "mergeable" key: GitHub is still computing it (null).
			writeJSON(t, w, map[string]any{
				"number": 42, "state": "open", "mergeable_state": "unknown",
				"head": map[string]any{"sha": "abc123"}, "base": map[string]any{"ref": "main"},
			})
		case "/repos/o/r/branches/main/protection":
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]any{"message": "Not Found"})
		case "/repos/o/r/commits/abc123/check-runs":
			writeJSON(t, w, map[string]any{"total_count": 0, "check_runs": []any{}})
		case "/repos/o/r/commits/abc123/status":
			writeJSON(t, w, map[string]any{"state": "success", "statuses": []any{}})
		case "/repos/o/r/pulls/42/reviews":
			writeJSON(t, w, []any{})
		default:
			http.NotFound(w, r)
		}
	})
	status, err := s.PullRequestStatus(t.Context(), &gatewayv1.ForgeRepository{Owner: "o", Name: "r"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := status.GetBlockingReason(), "pull request mergeability is still being computed by the provider; retry shortly"; got != want {
		t.Fatalf("blocking reason = %q, want %q", got, want)
	}
}

func TestPullRequestStatusPaginatesReviewsAndCountsLatestVerdict(t *testing.T) {
	s := githubServerForTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/pulls/42":
			writeJSON(t, w, map[string]any{
				"number": 42, "state": "open", "mergeable": true, "mergeable_state": "clean",
				"head": map[string]any{"sha": "abc123"}, "base": map[string]any{"ref": "main"},
			})
		case "/repos/o/r/branches/main/protection":
			writeJSON(t, w, map[string]any{"required_pull_request_reviews": map[string]any{"required_approving_review_count": 1}})
		case "/repos/o/r/commits/abc123/check-runs":
			writeJSON(t, w, map[string]any{"total_count": 0, "check_runs": []any{}})
		case "/repos/o/r/commits/abc123/status":
			writeJSON(t, w, map[string]any{"state": "success", "statuses": []any{}})
		case "/repos/o/r/pulls/42/reviews":
			if r.URL.Query().Get("page") == "2" {
				// alice's later verdict withdraws the page-1 approval.
				writeJSON(t, w, []map[string]any{{"id": 2, "state": "CHANGES_REQUESTED", "user": map[string]any{"login": "alice"}, "submitted_at": "2026-07-25T13:00:00Z"}})
				return
			}
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			writeJSON(t, w, []map[string]any{{"id": 1, "state": "APPROVED", "user": map[string]any{"login": "alice"}, "submitted_at": "2026-07-25T12:00:00Z"}})
		default:
			http.NotFound(w, r)
		}
	})
	status, err := s.PullRequestStatus(t.Context(), &gatewayv1.ForgeRepository{Owner: "o", Name: "r"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.GetReviews()) != 2 {
		t.Fatalf("expected both review pages fetched, got %d reviews", len(status.GetReviews()))
	}
	if got, want := status.GetBlockingReason(), "requires 1 approving review(s); has 0"; got != want {
		t.Fatalf("blocking reason = %q, want %q (stale approval must not count)", got, want)
	}
}

func TestPrViewRequiresNumber(t *testing.T) {
	s := New("/tmp/x", "", "test")
	// owner/repo provided so resolveRepo succeeds; number missing -> error.
	resp := s.prView(context.Background(), &toolboxv0.CallToolRequest{
		Arguments: mustStruct(map[string]any{"owner": "o", "repo": "r"}),
	})
	if resp.Error == "" {
		t.Error("expected error when number is missing")
	}
}

func githubServerForTest(t *testing.T, handler http.HandlerFunc) *Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	baseURL := server.URL + "/"
	client, err := gh.NewClient(
		gh.WithHTTPClient(server.Client()),
		gh.WithURLs(&baseURL, &baseURL),
	)
	if err != nil {
		t.Fatal(err)
	}
	s := New("/tmp/x", "", "test")
	s.client = client
	return s
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
