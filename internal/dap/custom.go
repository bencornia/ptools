package dap

import (
	"strings"

	godap "github.com/google/go-dap"

	"github.com/bencornia/ptools/internal/diagram"
)

// prdb's own requests, beyond the protocol's, for a client that shows the
// trace's diagram beside the debugger. Steps count from 1, as the debugger
// shows them.

// DiagramRequest asks for the trace's diagram, as SVG, and the current
// step.
type DiagramRequest struct {
	godap.Request
}

type DiagramResponse struct {
	godap.Response
	Body DiagramBody `json:"body"`
}

type DiagramBody struct {
	SVG  string `json:"svg"`
	Step int    `json:"step"`
}

// StepRequest asks for the current step.
type StepRequest struct {
	godap.Request
}

type StepResponse struct {
	godap.Response
	Body StepBody `json:"body"`
}

type StepBody struct {
	Step int `json:"step"`
}

// GotoStepRequest moves the replay to a step, which then stops there.
type GotoStepRequest struct {
	godap.Request
	Arguments StepBody `json:"arguments"`
}

type GotoStepResponse struct {
	godap.Response
}

// codec decodes the protocol's messages and prdb's own requests.
var codec = func() *godap.Codec {
	c := godap.NewCodec()
	c.RegisterRequest("prdbDiagram",
		func() godap.Message { return &DiagramRequest{} },
		func() godap.Message { return &DiagramResponse{} })
	c.RegisterRequest("prdbStep",
		func() godap.Message { return &StepRequest{} },
		func() godap.Message { return &StepResponse{} })
	c.RegisterRequest("prdbGoto",
		func() godap.Message { return &GotoStepRequest{} },
		func() godap.Message { return &GotoStepResponse{} })
	return c
}()

func (s *server) diagram(req *DiagramRequest) {
	var svg strings.Builder
	if err := diagram.SVG(&svg, s.session, diagram.Options{Title: "Trace diagram"}); err != nil {
		s.fail(req, err.Error())
		return
	}
	s.send(&DiagramResponse{Response: response(req), Body: DiagramBody{SVG: svg.String(), Step: s.session.Pos + 1}})
}

func (s *server) gotoStep(req *GotoStepRequest) {
	step := req.Arguments.Step
	if step < 1 || step > len(s.session.Steps) {
		s.fail(req, "no such step")
		return
	}
	s.send(&GotoStepResponse{Response: response(req)})
	s.session.Pos = step - 1
	reason := "goto"
	if s.session.Current().Kind == "AssertionFailure" {
		reason = "exception"
	}
	s.stopped(reason)
}
