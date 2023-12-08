package communicate_test

import (
	"fmt"
	"testing"

	"github.com/codefly-dev/core/agents/communicate"

	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
)

type seqHandler struct{}

func (s seqHandler) Process(req *agentsv1.InformationRequest) (*agentsv1.Answer, error) {
	switch req.Question.Value.(type) {
	case *agentsv1.Question_Confirm:
		return &agentsv1.Answer{
			Value: &agentsv1.Answer_Confirm{
				Confirm: &agentsv1.ConfirmAnswer{
					Confirmed: false,
				},
			},
		}, nil
	case *agentsv1.Question_Input:
		return &agentsv1.Answer{
			Value: &agentsv1.Answer_Input{
				Input: &agentsv1.InputAnswer{
					Answer: &agentsv1.InputAnswer_StringValue{
						StringValue: "working",
					},
				},
			},
		}, nil

	case *agentsv1.Question_Selection:
		return &agentsv1.Answer{
			Value: &agentsv1.Answer_Selection{
				Selection: &agentsv1.SelectionAnswer{
					Selected: []string{"option 1"},
				},
			},
		}, nil
	}
	return nil, fmt.Errorf("unknown question type: %v", req.Question.Value)
}

var _ communicate.QuestionHandler = &seqHandler{}

func TestSequence(t *testing.T) {
	//logger := shared.NewLogger().With("communicate_test.TestSequence")
	//logger.SetLevel(shared.DebugLevel)
	//
	//// The client asks for 3 things
	//client := communicate.NewClientContext(communicate.Create, logger)
	//err := client.NewSequence(
	//	client.NewConfirm(&agentsv1.Message{Name: "confirm"}, true),
	//	client.NewStringInput(&agentsv1.Message{Name: "input"}, "this is the default value"),
	//	client.NewSelection(&agentsv1.Message{Name: "selection"},
	//		&agentsv1.Message{Name: "option 1"},
	//		&agentsv1.Message{Name: "option 2"},
	//		&agentsv1.Message{Name: "option 3"}),
	//)
	//assert.NoError(t, err)
	//
	//// The server engage with the client
	//server := communicate.NewServerContext(communicate.Create, logger)
	//server.Handler = &seqHandler{}
	//
	//expectedTypes := []any{new(agentsv1.Question_Confirm), new(agentsv1.Question_Input), new(agentsv1.Question_Selection)}
	//
	//// We will do server -> client until the the server is happy
	//var answer *agentsv1.Answer
	//for step := 0; ; step++ {
	//	logger.Debugf("step: %v", step)
	//	// Communicate message to send to the client based on previous answer
	//	eng, err := server.Communicate(answer)
	//	assert.NoError(t, err)
	//	request, err := client.postLoad(eng)
	//	assert.NoError(t, err)
	//	if request == nil {
	//		logger.Debugf("client is done at step %v", step)
	//		break
	//	}
	//	assert.IsType(t, expectedTypes[step], request.Question.Value)
	//	// This is how the server will answer the thing
	//	answer, err = server.postLoad(request)
	//	assert.NoError(t, err)
	//}
	//
	//// The client state should be complete
	//assert.Equal(t, false, client.Confirm(0).Confirmed)
	//assert.Equal(t, "working", client.Input(1).GetStringValue())
	//assert.Equal(t, []string{"option 1"}, client.Selection(2).Selected)
}
