package communicate_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/agents/communicate"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"

	factoryv1 "github.com/codefly-dev/core/generated/go/services/factory/v1"
)

// We mimic the behavior of a agent
// Create method with a sequence of questions
type agentTest struct {
	*communicate.Server
}

type dataCreate struct {
	results []string
}

func (s *agentTest) Create(ctx context.Context, req *factoryv1.CreateRequest) (*dataCreate, error) {
	if !s.Server.Ready(shared.TypeOf[factoryv1.CreateRequest]()) {
		return nil, fmt.Errorf("not ready")
	}
	return &dataCreate{}, nil
}

func TestSequenceWithoutCommunication(t *testing.T) {
	ctx := wool.NewContext()
	// Create a new sequence
	server := communicate.NewServer(ctx)
	sequence := agentTest{Server: server}
	_, ok := server.RequiresCommunication(communicate.Channel[factoryv1.CreateRequest]())
	assert.False(t, ok)
	resp, err := sequence.Create(ctx, &factoryv1.CreateRequest{})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 0, len(resp.results))
}

func (s *agentTest) createSequence() *communicate.Sequence {
	return communicate.NewSequence(
		communicate.NewStringInput(&agentv1.Message{Name: "one"}, ""),
	)
}

func (s *agentTest) createBiggerSequence() *communicate.Sequence {
	return communicate.NewSequence(
		communicate.NewStringInput(&agentv1.Message{Name: "one"}, ""),
		communicate.NewStringInput(&agentv1.Message{Name: "two"}, ""),
		communicate.NewStringInput(&agentv1.Message{Name: "three"}, ""),
		communicate.NewStringInput(&agentv1.Message{Name: "four"}, ""),
	)
}

func (s *agentTest) Communicate(ctx context.Context, req *agentv1.Engage) (*agentv1.InformationRequest, error) {
	return s.Server.Communicate(ctx, req)
}

type clientHandler struct{}

func (*clientHandler) Answer(ctx context.Context, question *agentv1.Question) (*agentv1.Answer, error) {
	return &agentv1.Answer{
		Value: &agentv1.Answer_Input{
			Input: &agentv1.InputAnswer{
				Answer: &agentv1.InputAnswer_StringValue{StringValue: "working"},
			},
		},
	}, nil
}

type clientHandlerRepeater struct{}

func (*clientHandlerRepeater) Answer(ctx context.Context, question *agentv1.Question) (*agentv1.Answer, error) {
	return &agentv1.Answer{
		Value: &agentv1.Answer_Input{
			Input: &agentv1.InputAnswer{
				Answer: &agentv1.InputAnswer_StringValue{StringValue: question.Message.Name},
			},
		},
	}, nil
}

func TestSequenceWithCommunication(t *testing.T) {
	ctx := wool.NewContext()
	// Create a new agent
	server := communicate.NewServer(ctx)
	agent := agentTest{Server: server}

	err := server.Register(ctx, communicate.New[factoryv1.CreateRequest](agent.createSequence()))
	assert.NoError(t, err)
	_, ok := server.RequiresCommunication(communicate.Channel[factoryv1.CreateRequest]())
	assert.True(t, ok)
	_, err = agent.Create(ctx, &factoryv1.CreateRequest{})
	assert.Error(t, err)

	answerProvider := &clientHandler{}
	clientSession := communicate.NewClientSession(communicate.Channel[factoryv1.CreateRequest](), answerProvider)

	eng, err := clientSession.Engage(ctx, nil)
	assert.NoError(t, err)
	assert.True(t, eng.Mode == agentv1.Engage_START)

	// Send that to the server
	res, err := server.Communicate(ctx, eng)
	assert.NoError(t, err)
	// We should have the confirmation question
	assert.NotNil(t, res.Question.GetInput())

	// generator handles this
	eng, err = clientSession.Engage(ctx, res)
	assert.NoError(t, err)
	assert.Equal(t, "one", eng.Stage)
	assert.Equal(t, "working", eng.Answer.GetInput().GetStringValue())

	// sent that to the server
	res, err = server.Communicate(ctx, eng)
	assert.NoError(t, err)
	// we are done
	assert.True(t, res.Done)

	// we will send that back to the generator
	eng, err = clientSession.Engage(ctx, res)
	assert.NoError(t, err)
	assert.True(t, eng.Mode == agentv1.Engage_END)

	// we got the info back
	session, err := server.Done(ctx, communicate.Channel[factoryv1.CreateRequest]())
	assert.NoError(t, err)
	value, err := session.GetInputString("one")
	assert.NoError(t, err)
	assert.Equal(t, "working", value)
}

func TestSequenceWithCommunicationFlow(t *testing.T) {
	ctx := wool.NewContext()
	// Create a new agent
	server := communicate.NewServer(ctx)
	agent := agentTest{Server: server}

	err := server.Register(ctx, communicate.New[factoryv1.CreateRequest](agent.createBiggerSequence()))
	assert.NoError(t, err)

	answerProvider := &clientHandlerRepeater{}

	err = communicate.Do[factoryv1.CreateRequest](ctx, server, answerProvider)
	assert.NoError(t, err)

	session, err := server.Done(ctx, communicate.Channel[factoryv1.CreateRequest]())
	for _, v := range []string{"one", "two", "three", "four"} {
		value, err := session.GetInputString(v)
		assert.NoError(t, err)
		assert.Equal(t, v, value)
	}
}
