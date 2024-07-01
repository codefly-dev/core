package communicate_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/agents/communicate"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// We mimic the behavior of a agent
// Create method with a sequence of questions
type agentTest struct {
	*communicate.Server
}

type dataCreate struct {
	results []string
}

func (s *agentTest) Create(_ context.Context, req *builderv0.CreateRequest) (*dataCreate, error) {
	if !s.Server.Ready(shared.TypeOf[builderv0.CreateRequest]()) {
		return nil, fmt.Errorf("not ready")
	}
	return &dataCreate{}, nil
}

func TestSequenceWithoutCommunication(t *testing.T) {
	ctx := context.Background()
	// Create a new sequence
	server := communicate.NewServer(ctx)
	sequence := agentTest{Server: server}
	_, ok := server.RequiresCommunication(communicate.Channel[builderv0.CreateRequest]())
	require.False(t, ok)
	resp, err := sequence.Create(ctx, &builderv0.CreateRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 0, len(resp.results))
}

func (s *agentTest) createSequence() *communicate.Sequence {
	return communicate.NewSequence(
		communicate.NewStringInput(&agentv0.Message{Name: "one"}, ""),
	)
}

func (s *agentTest) createBiggerSequence() *communicate.Sequence {
	return communicate.NewSequence(
		communicate.NewStringInput(&agentv0.Message{Name: "one"}, ""),
		communicate.NewStringInput(&agentv0.Message{Name: "two"}, ""),
		communicate.NewStringInput(&agentv0.Message{Name: "three"}, ""),
		communicate.NewStringInput(&agentv0.Message{Name: "four"}, ""),
	)
}

func (s *agentTest) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return s.Server.Communicate(ctx, req)
}

type clientHandler struct{}

func (*clientHandler) Answer(_ context.Context, question *agentv0.Question) (*agentv0.Answer, error) {
	return &agentv0.Answer{
		Value: &agentv0.Answer_Input{
			Input: &agentv0.InputAnswer{
				Answer: &agentv0.InputAnswer_StringValue{StringValue: "working"},
			},
		},
	}, nil
}

type clientHandlerRepeater struct{}

func (*clientHandlerRepeater) Answer(_ context.Context, question *agentv0.Question) (*agentv0.Answer, error) {
	return &agentv0.Answer{
		Value: &agentv0.Answer_Input{
			Input: &agentv0.InputAnswer{
				Answer: &agentv0.InputAnswer_StringValue{StringValue: question.Message.Name},
			},
		},
	}, nil
}

func TestSequenceWithCommunication(t *testing.T) {
	ctx := context.Background()
	// Create a new agent
	server := communicate.NewServer(ctx)
	agent := agentTest{Server: server}

	err := server.Register(ctx, communicate.New[builderv0.CreateRequest](agent.createSequence()))
	require.NoError(t, err)
	_, ok := server.RequiresCommunication(communicate.Channel[builderv0.CreateRequest]())
	require.True(t, ok)
	_, err = agent.Create(ctx, &builderv0.CreateRequest{})
	require.Error(t, err)

	answerProvider := &clientHandler{}
	clientSession := communicate.NewClientSession(communicate.Channel[builderv0.CreateRequest](), answerProvider)

	eng, err := clientSession.Engage(ctx, nil)
	require.NoError(t, err)
	require.True(t, eng.Mode == agentv0.Engage_START)

	// Send that to the server
	res, err := server.Communicate(ctx, eng)
	require.NoError(t, err)
	// We should have the confirmation question
	require.NotNil(t, res.Question.GetInput())

	// generator handles this
	eng, err = clientSession.Engage(ctx, res)
	require.NoError(t, err)
	require.Equal(t, "one", eng.Stage)
	require.Equal(t, "working", eng.Answer.GetInput().GetStringValue())

	// sent that to the server
	res, err = server.Communicate(ctx, eng)
	require.NoError(t, err)
	// we are done
	require.True(t, res.Done)

	// we will send that back to the generator
	eng, err = clientSession.Engage(ctx, res)
	require.NoError(t, err)
	require.True(t, eng.Mode == agentv0.Engage_END)

	// we got the info back
	session, err := server.Done(ctx, communicate.Channel[builderv0.CreateRequest]())
	require.NoError(t, err)
	value, err := session.GetInputString("one")
	require.NoError(t, err)
	require.Equal(t, "working", value)
}

func TestSequenceWithCommunicationFlow(t *testing.T) {
	ctx := context.Background()
	// Create a new agent
	server := communicate.NewServer(ctx)
	agent := agentTest{Server: server}

	err := server.Register(ctx, communicate.New[builderv0.CreateRequest](agent.createBiggerSequence()))
	require.NoError(t, err)

	answerProvider := &clientHandlerRepeater{}

	err = communicate.Do[builderv0.CreateRequest](ctx, server, answerProvider)
	require.NoError(t, err)

	session, err := server.Done(ctx, communicate.Channel[builderv0.CreateRequest]())
	require.NoError(t, err)
	for _, v := range []string{"one", "two", "three", "four"} {
		value, err := session.GetInputString(v)
		require.NoError(t, err)
		require.Equal(t, v, value)
	}
}
