package github

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v37/github"
	"google.golang.org/protobuf/types/known/timestamppb"

	gatewayv1 "github.com/codefly-dev/core/generated/go/mind/gateway/v1"
)

// PullRequestStatus returns the provider-authoritative status, checks, reviews,
// and blocking reason for one pull request.
func (s *Server) PullRequestStatus(ctx context.Context, repository *gatewayv1.ForgeRepository, number int64) (*gatewayv1.ForgePullRequestStatus, error) {
	owner, repo, err := s.resolveForgeRepository(repository)
	if err != nil {
		return nil, err
	}
	if number <= 0 {
		return nil, fmt.Errorf("pull request number is required")
	}
	pr, _, err := s.client.PullRequests.Get(ctx, owner, repo, int(number))
	if err != nil {
		return nil, fmt.Errorf("get pull request: %w", err)
	}
	headSHA := pr.GetHead().GetSHA()
	baseBranch := pr.GetBase().GetRef()

	requiredContexts := map[string]bool{}
	requiredApprovals := 0
	protection, _, err := s.client.Repositories.GetBranchProtection(ctx, owner, repo, baseBranch)
	if err != nil && !githubNotFound(err) {
		return nil, fmt.Errorf("get branch protection: %w", err)
	}
	if err == nil {
		if protection.RequiredStatusChecks != nil {
			for _, contextName := range protection.RequiredStatusChecks.Contexts {
				requiredContexts[contextName] = true
			}
		}
		if protection.RequiredPullRequestReviews != nil {
			requiredApprovals = protection.RequiredPullRequestReviews.RequiredApprovingReviewCount
		}
	}

	checkRuns, err := s.listCheckRuns(ctx, owner, repo, headSHA)
	if err != nil {
		return nil, err
	}
	statuses, err := s.listCommitStatuses(ctx, owner, repo, headSHA)
	if err != nil {
		return nil, err
	}
	reviews, err := s.listReviews(ctx, owner, repo, int(number))
	if err != nil {
		return nil, err
	}

	checks := make([]*gatewayv1.ForgeCheck, 0, len(checkRuns)+len(statuses))
	observedContexts := map[string]bool{}
	checkRunNames := map[string]bool{}
	for _, check := range checkRuns {
		name := check.GetName()
		observedContexts[name] = true
		checkRunNames[name] = true
		checks = append(checks, &gatewayv1.ForgeCheck{
			Id:         strconv.FormatInt(check.GetID(), 10),
			Name:       name,
			State:      check.GetStatus(),
			Conclusion: check.GetConclusion(),
			Required:   requiredContexts[name],
			Url:        firstNonEmpty(check.GetHTMLURL(), check.GetDetailsURL()),
		})
	}
	for _, status := range statuses {
		name := status.GetContext()
		if checkRunNames[name] {
			// The Checks API already reported this context; the legacy
			// commit-status mirror would double-count it (and a pending mirror
			// could mask a passing check run). Prefer the check run.
			continue
		}
		observedContexts[name] = true
		state, conclusion := commitStatusState(status.GetState())
		checks = append(checks, &gatewayv1.ForgeCheck{
			Id:         strconv.FormatInt(status.GetID(), 10),
			Name:       name,
			State:      state,
			Conclusion: conclusion,
			Required:   requiredContexts[name],
			Url:        status.GetTargetURL(),
		})
	}
	for contextName := range requiredContexts {
		if !observedContexts[contextName] {
			checks = append(checks, &gatewayv1.ForgeCheck{Name: contextName, State: "queued", Required: true})
		}
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].GetName() == checks[j].GetName() {
			return checks[i].GetId() < checks[j].GetId()
		}
		return checks[i].GetName() < checks[j].GetName()
	})

	forgeReviews := make([]*gatewayv1.ForgeReview, 0, len(reviews))
	approvalStanding := map[string]string{}
	for _, review := range reviews {
		author := review.GetUser().GetLogin()
		state := strings.ToLower(review.GetState())
		// Only APPROVED, CHANGES_REQUESTED, and DISMISSED change an author's
		// standing; COMMENTED and PENDING leave the prior verdict intact.
		// Reviews arrive chronologically, so the last decisive one wins — a
		// stale approval later withdrawn must not count.
		switch state {
		case "approved", "changes_requested", "dismissed":
			approvalStanding[author] = state
		}
		forgeReview := &gatewayv1.ForgeReview{
			Id:     strconv.FormatInt(review.GetID(), 10),
			Author: author,
			State:  state,
		}
		if review.SubmittedAt != nil {
			forgeReview.SubmittedAt = timestamppb.New(*review.SubmittedAt)
		}
		forgeReviews = append(forgeReviews, forgeReview)
	}
	approvals := 0
	for _, standing := range approvalStanding {
		if standing == "approved" {
			approvals++
		}
	}

	status := &gatewayv1.ForgePullRequestStatus{
		Repository: &gatewayv1.ForgeRepository{Provider: "github", Owner: owner, Name: repo},
		Number:     number,
		State:      pullRequestState(pr),
		HeadSha:    headSHA,
		BaseBranch: baseBranch,
		Mergeable:  pr.GetMergeable(),
		Checks:     checks,
		Reviews:    forgeReviews,
		Url:        pr.GetHTMLURL(),
		ObservedAt: timestamppb.New(time.Now().UTC()),
	}
	status.BlockingReason = pullRequestBlockingReason(pr, checks, approvals, requiredApprovals)
	return status, nil
}

// listCheckRuns returns every "latest" check run for a ref, following
// pagination so large PRs cannot silently drop required checks.
func (s *Server) listCheckRuns(ctx context.Context, owner, repo, ref string) ([]*gh.CheckRun, error) {
	opts := &gh.ListCheckRunsOptions{Filter: gh.String("latest"), ListOptions: gh.ListOptions{PerPage: 100}}
	var all []*gh.CheckRun
	for {
		page, resp, err := s.client.Checks.ListCheckRunsForRef(ctx, owner, repo, ref, opts)
		if err != nil {
			return nil, fmt.Errorf("list check runs: %w", err)
		}
		all = append(all, page.CheckRuns...)
		if resp.NextPage == 0 {
			return all, nil
		}
		opts.Page = resp.NextPage
	}
}

// listCommitStatuses returns every commit-status context for a ref, following
// pagination.
func (s *Server) listCommitStatuses(ctx context.Context, owner, repo, ref string) ([]*gh.RepoStatus, error) {
	opts := &gh.ListOptions{PerPage: 100}
	var all []*gh.RepoStatus
	for {
		combined, resp, err := s.client.Repositories.GetCombinedStatus(ctx, owner, repo, ref, opts)
		if err != nil {
			return nil, fmt.Errorf("get commit status: %w", err)
		}
		all = append(all, combined.Statuses...)
		if resp.NextPage == 0 {
			return all, nil
		}
		opts.Page = resp.NextPage
	}
}

// listReviews returns every review on a PR, following pagination so approval
// counting sees each author's latest verdict.
func (s *Server) listReviews(ctx context.Context, owner, repo string, number int) ([]*gh.PullRequestReview, error) {
	opts := &gh.ListOptions{PerPage: 100}
	var all []*gh.PullRequestReview
	for {
		page, resp, err := s.client.PullRequests.ListReviews(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, fmt.Errorf("list reviews: %w", err)
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			return all, nil
		}
		opts.Page = resp.NextPage
	}
}

// MergePullRequest enforces the requested check policy and returns the merge
// result stamped from GitHub's API response.
func (s *Server) MergePullRequest(ctx context.Context, req *gatewayv1.ForgeMergePullRequestRequest) (*gatewayv1.ForgeMergePullRequestResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("merge request is required")
	}
	if req.GetMethod() == gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_UNSPECIFIED {
		return nil, fmt.Errorf("merge method is required")
	}
	if req.GetCheckPolicy() == gatewayv1.ForgeCheckPolicy_FORGE_CHECK_POLICY_UNSPECIFIED {
		return nil, fmt.Errorf("check policy is required")
	}
	status, err := s.PullRequestStatus(ctx, req.GetRepository(), req.GetNumber())
	if err != nil {
		return nil, err
	}
	if req.GetCheckPolicy() == gatewayv1.ForgeCheckPolicy_FORGE_CHECK_POLICY_REQUIRE_PASSING && status.GetBlockingReason() != "" {
		return &gatewayv1.ForgeMergePullRequestResponse{
			Success: false,
			Error:   status.GetBlockingReason(),
			Status:  status,
		}, nil
	}
	owner, repo, err := s.resolveForgeRepository(req.GetRepository())
	if err != nil {
		return nil, err
	}
	result, _, err := s.client.PullRequests.Merge(ctx, owner, repo, int(req.GetNumber()), req.GetCommitMessage(), &gh.PullRequestOptions{
		CommitTitle: req.GetCommitTitle(),
		SHA:         status.GetHeadSha(),
		MergeMethod: forgeMergeMethod(req.GetMethod()),
	})
	if err != nil {
		return nil, fmt.Errorf("merge pull request: %w", err)
	}
	if !result.GetMerged() {
		return &gatewayv1.ForgeMergePullRequestResponse{
			Success: false,
			Error:   result.GetMessage(),
			Status:  status,
		}, nil
	}
	status.State = "merged"
	status.Mergeable = false
	status.BlockingReason = ""
	status.ObservedAt = timestamppb.New(time.Now().UTC())
	eventID := fmt.Sprintf("forge:github:%s/%s:pull_request:%d:merged", owner, repo, req.GetNumber())
	return &gatewayv1.ForgeMergePullRequestResponse{
		Success: true,
		Status:  status,
		Act: &gatewayv1.ActReceipt{
			EventId:             eventID,
			Kind:                "forge.pull_request.merged",
			Target:              fmt.Sprintf("%s/%s#%d", owner, repo, req.GetNumber()),
			State:               "merged",
			Revision:            result.GetSHA(),
			AuthoritativeSource: "github-api",
			AuthoritativeUrl:    status.GetUrl(),
			ObservedAt:          status.GetObservedAt(),
		},
	}, nil
}

// RequestReview requests users and teams and returns the provider-accepted set.
func (s *Server) RequestReview(ctx context.Context, req *gatewayv1.ForgeRequestReviewRequest) (*gatewayv1.ForgeRequestReviewResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("review request is required")
	}
	if req.GetNumber() <= 0 {
		return nil, fmt.Errorf("pull request number is required")
	}
	if len(req.GetReviewers()) == 0 && len(req.GetTeamReviewers()) == 0 {
		return nil, fmt.Errorf("at least one reviewer or team reviewer is required")
	}
	owner, repo, err := s.resolveForgeRepository(req.GetRepository())
	if err != nil {
		return nil, err
	}
	pr, _, err := s.client.PullRequests.RequestReviewers(ctx, owner, repo, int(req.GetNumber()), gh.ReviewersRequest{
		Reviewers:     req.GetReviewers(),
		TeamReviewers: req.GetTeamReviewers(),
	})
	if err != nil {
		return nil, fmt.Errorf("request reviewers: %w", err)
	}
	reviewers := make([]string, 0, len(pr.RequestedReviewers))
	for _, reviewer := range pr.RequestedReviewers {
		reviewers = append(reviewers, reviewer.GetLogin())
	}
	teams := make([]string, 0, len(pr.RequestedTeams))
	for _, team := range pr.RequestedTeams {
		teams = append(teams, team.GetSlug())
	}
	sort.Strings(reviewers)
	sort.Strings(teams)
	observedAt := timestamppb.New(time.Now().UTC())
	identityParts := append(append([]string(nil), reviewers...), teams...)
	eventID := fmt.Sprintf("forge:github:%s/%s:pull_request:%d:review_requested:%s", owner, repo, req.GetNumber(), strings.Join(identityParts, ","))
	return &gatewayv1.ForgeRequestReviewResponse{
		Success:       true,
		Reviewers:     reviewers,
		TeamReviewers: teams,
		Act: &gatewayv1.ActReceipt{
			EventId:             eventID,
			Kind:                "forge.pull_request.review_requested",
			Target:              fmt.Sprintf("%s/%s#%d", owner, repo, req.GetNumber()),
			State:               "requested",
			AuthoritativeSource: "github-api",
			AuthoritativeUrl:    pr.GetHTMLURL(),
			ObservedAt:          observedAt,
		},
	}, nil
}

func (s *Server) resolveForgeRepository(repository *gatewayv1.ForgeRepository) (string, string, error) {
	if provider := strings.TrimSpace(repository.GetProvider()); provider != "" && provider != "github" {
		return "", "", fmt.Errorf("github toolbox cannot serve provider %q", provider)
	}
	return s.resolveRepo(map[string]any{
		"owner": strings.TrimSpace(repository.GetOwner()),
		"repo":  strings.TrimSpace(repository.GetName()),
	})
}

func githubNotFound(err error) bool {
	response, ok := err.(*gh.ErrorResponse)
	return ok && response.Response != nil && response.Response.StatusCode == http.StatusNotFound
}

func pullRequestState(pr *gh.PullRequest) string {
	if pr.GetMerged() {
		return "merged"
	}
	return strings.ToLower(pr.GetState())
}

func pullRequestBlockingReason(pr *gh.PullRequest, checks []*gatewayv1.ForgeCheck, approvals, requiredApprovals int) string {
	if pr.GetMerged() {
		return ""
	}
	if strings.ToLower(pr.GetState()) != "open" {
		return "pull request is not open"
	}
	if pr.Mergeable == nil {
		// GitHub computes mergeability asynchronously and reports null until it
		// finishes. Block (fail-closed) but say so precisely instead of
		// claiming a conflict that may not exist.
		return "pull request mergeability is still being computed by the provider; retry shortly"
	}
	if !pr.GetMergeable() {
		if state := strings.TrimSpace(pr.GetMergeableState()); state != "" {
			return "pull request is not mergeable: " + state
		}
		return "pull request is not mergeable"
	}
	for _, check := range checks {
		if !check.GetRequired() {
			continue
		}
		if check.GetState() != "completed" || !successfulConclusion(check.GetConclusion()) {
			return fmt.Sprintf("required check %q is %s/%s", check.GetName(), check.GetState(), check.GetConclusion())
		}
	}
	if approvals < requiredApprovals {
		return fmt.Sprintf("requires %d approving review(s); has %d", requiredApprovals, approvals)
	}
	return ""
}

func commitStatusState(state string) (string, string) {
	switch strings.ToLower(state) {
	case "success":
		return "completed", "success"
	case "failure", "error":
		return "completed", strings.ToLower(state)
	default:
		return "in_progress", ""
	}
}

func successfulConclusion(conclusion string) bool {
	switch strings.ToLower(conclusion) {
	case "success", "neutral", "skipped":
		return true
	default:
		return false
	}
}

func forgeMergeMethod(method gatewayv1.ForgeMergeMethod) string {
	switch method {
	case gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_SQUASH:
		return "squash"
	case gatewayv1.ForgeMergeMethod_FORGE_MERGE_METHOD_REBASE:
		return "rebase"
	default:
		return "merge"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
