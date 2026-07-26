package github

import (
	"fmt"
	"strings"
	"time"

	gh "github.com/google/go-github/v37/github"
	"google.golang.org/protobuf/types/known/timestamppb"

	gatewayv1 "github.com/codefly-dev/core/generated/go/mind/gateway/v1"
)

// NormalizeWebhook verifies GitHub's signature and converts provider payloads
// into the forge contract used by every caller.
func (s *Server) NormalizeWebhook(req *gatewayv1.ForgeNormalizeWebhookRequest) (*gatewayv1.ForgeEvent, error) {
	if req.GetProvider() != "github" {
		return nil, fmt.Errorf("github toolbox cannot normalize provider %q", req.GetProvider())
	}
	if strings.TrimSpace(req.GetSecret()) == "" {
		return nil, fmt.Errorf("github webhook secret is required")
	}
	if err := gh.ValidateSignature(req.GetSignature(), req.GetPayload(), []byte(req.GetSecret())); err != nil {
		return nil, fmt.Errorf("validate github webhook signature: %w", err)
	}
	event, err := gh.ParseWebHook(req.GetEventType(), req.GetPayload())
	if err != nil {
		return nil, err
	}
	observedAt := timestamppb.New(time.Now().UTC())
	switch value := event.(type) {
	case *gh.PullRequestEvent:
		repository, owner, name, err := webhookRepository(value.Repo)
		if err != nil || value.PullRequest == nil || value.GetNumber() <= 0 {
			return nil, fmt.Errorf("normalize pull_request webhook: invalid repository or pull request")
		}
		state := value.GetAction()
		eventID := fmt.Sprintf("forge:github:%s/%s:pull_request:%d:%s", owner, name, value.GetNumber(), state)
		if value.PullRequest.GetMerged() {
			state = "merged"
			eventID = fmt.Sprintf("forge:github:%s/%s:pull_request:%d:merged", owner, name, value.GetNumber())
		}
		return &gatewayv1.ForgeEvent{
			EventId: eventID, Kind: gatewayv1.ForgeEventKind_FORGE_EVENT_KIND_PULL_REQUEST,
			Ref: forgePullRequestRef(owner, name, int64(value.GetNumber())), Repository: repository,
			Number: int64(value.GetNumber()), Revision: value.PullRequest.GetHead().GetSHA(), State: state,
			Author: value.GetSender().GetLogin(), AuthoritativeUrl: value.PullRequest.GetHTMLURL(),
			AuthoritativeSource: "github-webhook", ObservedAt: observedAt,
		}, nil

	case *gh.CheckRunEvent:
		repository, owner, name, err := webhookRepository(value.Repo)
		if err != nil || value.CheckRun == nil {
			return nil, fmt.Errorf("normalize check_run webhook: invalid repository or check run")
		}
		number := int64(0)
		ref := forgeRevisionRef(owner, name, value.CheckRun.GetHeadSHA())
		if pulls := value.CheckRun.PullRequests; len(pulls) != 0 && pulls[0] != nil {
			number = int64(pulls[0].GetNumber())
			ref = forgePullRequestRef(owner, name, number)
		}
		state := value.CheckRun.GetStatus()
		conclusion := value.CheckRun.GetConclusion()
		eventID := fmt.Sprintf("forge:github:%s/%s:check:%d:%s:%s", owner, name, value.CheckRun.GetID(), state, conclusion)
		return &gatewayv1.ForgeEvent{
			EventId: eventID, Kind: gatewayv1.ForgeEventKind_FORGE_EVENT_KIND_CHECK,
			Ref: ref, Repository: repository, Number: number, Revision: value.CheckRun.GetHeadSHA(),
			State: state, CheckName: value.CheckRun.GetName(), Conclusion: conclusion,
			AuthoritativeUrl: value.CheckRun.GetHTMLURL(), AuthoritativeSource: "github-webhook", ObservedAt: observedAt,
		}, nil

	case *gh.StatusEvent:
		repository, owner, name, err := webhookRepository(value.Repo)
		if err != nil || value.GetSHA() == "" {
			return nil, fmt.Errorf("normalize status webhook: invalid repository or revision")
		}
		// A commit status can still be pending; map it the same way the status
		// snapshot does so an unfinished status is not reported as completed.
		// The raw provider state keys the event id to keep transitions distinct.
		rawState := value.GetState()
		state, conclusion := commitStatusState(rawState)
		eventID := fmt.Sprintf("forge:github:%s/%s:status:%d:%s", owner, name, value.GetID(), rawState)
		return &gatewayv1.ForgeEvent{
			EventId: eventID, Kind: gatewayv1.ForgeEventKind_FORGE_EVENT_KIND_CHECK,
			Ref: forgeRevisionRef(owner, name, value.GetSHA()), Repository: repository, Revision: value.GetSHA(),
			State: state, CheckName: value.GetContext(), Conclusion: conclusion,
			AuthoritativeUrl: value.GetTargetURL(), AuthoritativeSource: "github-webhook", ObservedAt: observedAt,
		}, nil

	case *gh.PullRequestReviewEvent:
		repository, owner, name, err := webhookRepository(value.Repo)
		if err != nil || value.PullRequest == nil || value.Review == nil || value.PullRequest.GetNumber() <= 0 {
			return nil, fmt.Errorf("normalize pull_request_review webhook: invalid repository, pull request, or review")
		}
		number := int64(value.PullRequest.GetNumber())
		state := strings.ToLower(value.Review.GetState())
		eventID := fmt.Sprintf("forge:github:%s/%s:review:%d:%s", owner, name, value.Review.GetID(), state)
		return &gatewayv1.ForgeEvent{
			EventId: eventID, Kind: gatewayv1.ForgeEventKind_FORGE_EVENT_KIND_REVIEW,
			Ref: forgePullRequestRef(owner, name, number), Repository: repository, Number: number,
			Revision: value.PullRequest.GetHead().GetSHA(), State: state, Author: value.Review.GetUser().GetLogin(),
			AuthoritativeUrl: value.Review.GetHTMLURL(), AuthoritativeSource: "github-webhook", ObservedAt: observedAt,
		}, nil

	default:
		return nil, fmt.Errorf("github webhook %q is not a consequential forge event", req.GetEventType())
	}
}

func webhookRepository(repository *gh.Repository) (*gatewayv1.ForgeRepository, string, string, error) {
	owner, name := repository.GetOwner().GetLogin(), repository.GetName()
	if owner == "" || name == "" {
		return nil, "", "", fmt.Errorf("github webhook repository identity is incomplete")
	}
	return &gatewayv1.ForgeRepository{Provider: "github", Owner: owner, Name: name}, owner, name, nil
}

func forgePullRequestRef(owner, repository string, number int64) string {
	return fmt.Sprintf("forge:github:%s/%s:pull_request:%d", owner, repository, number)
}

func forgeRevisionRef(owner, repository, revision string) string {
	return fmt.Sprintf("forge:github:%s/%s:revision:%s", owner, repository, revision)
}
