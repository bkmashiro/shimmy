package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/log"
	"net/http"
	"strings"

	"github.com/lambda-feedback/shimmy/runtime/schema"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var (
	errInvalidMethod    = errors.New("invalid method")
	errSchemaNotFound   = errors.New("schema not found")
	errCommandNotFound  = errors.New("command not found")
	errInvalidCommand   = errors.New("invalid command")
	errValidationFailed = errors.New("validation failed")
)

var wellKnownErrors = map[error]int{
	errInvalidMethod:    http.StatusMethodNotAllowed,
	errSchemaNotFound:   http.StatusInternalServerError,
	errCommandNotFound:  http.StatusNotFound,
	errInvalidCommand:   http.StatusBadRequest,
	errValidationFailed: http.StatusBadRequest,
}

// HandlerParams defines the dependencies for the runtime handler.
type HandlerParams struct {
	fx.In

	Runtime Runtime

	Log *zap.Logger
}

type CaseWarning struct {
	Message string `json:"message"`
	Case    int    `json:"case"`
}

type CaseResult struct {
	IsCorrect bool
	Feedback  string
	Warning   *CaseWarning
}

// Handler is the interface for handling runtime requests.
type Handler interface {
	Handle(ctx context.Context, request Request) Response
}

// RuntimeHandler is a runtime handler that uses a runtime to handle requests.
type RuntimeHandler struct {
	runtime Runtime

	schemas map[validationType]*schema.Schema

	log *zap.Logger
}

// NewRuntimeHandler creates a new runtime handler.
func NewRuntimeHandler(params HandlerParams) (Handler, error) {
	requestSchema, err := schema.NewRequestSchema()
	if err != nil {
		return nil, err
	}

	responseSchema, err := schema.NewResponseSchema()
	if err != nil {
		return nil, err
	}

	schemas := map[validationType]*schema.Schema{
		validationTypeRequest:  requestSchema,
		validationTypeResponse: responseSchema,
	}

	return &RuntimeHandler{
		runtime: params.Runtime,
		schemas: schemas,
		log:     params.Log.Named("runtime_handler"),
	}, nil
}

// Handle handles a runtime request.
func (h *RuntimeHandler) Handle(ctx context.Context, req Request) Response {
	data, err := h.handle(ctx, req)
	if err != nil {
		return newErrorResponse(err)
	}

	return newResponse(http.StatusOK, data)
}

func (h *RuntimeHandler) handle(ctx context.Context, req Request) ([]byte, error) {
	log := h.log.With(
		zap.String("path", req.Path),
		zap.String("method", req.Method),
	)

	if req.Method != http.MethodPost {
		log.Debug("invalid method")
		return nil, errInvalidMethod
	}

	commandStr, ok := h.getCommand(req)
	if !ok {
		log.Debug("missing command")
		return nil, errCommandNotFound
	}

	log = log.With(zap.String("command", commandStr))

	// Parse the raw command string into a Command type
	command, ok := ParseCommand(commandStr)
	if !ok {
		log.Debug("invalid command")
		return nil, errInvalidCommand
	}

	resData, err := SendCommand(req, command, h, ctx)
	if err != nil {
		log.Debug("invalid command")
		return nil, errInvalidCommand
	}

	var reqBody map[string]any
	err = json.Unmarshal(req.Body, &reqBody)
	if err != nil {
		log.Error("failed to unmarshal request data", zap.Error(err))
		return nil, err
	}

	var respBody map[string]any
	err = json.Unmarshal(resData, &respBody)
	result, ok := respBody["result"].(map[string]interface{})
	if !ok {
		log.Error("failed to unmarshal response data", zap.Error(err))
		return nil, err
	}

	params, ok := reqBody["params"].(map[string]interface{})
	cases, ok := params["cases"].([]interface{})

	if result["is_correct"] == false {

		if ok && len(cases) > 0 {
			match, warnings := GetCaseFeedback(respBody, params, params["cases"].([]interface{}), req, command, h, ctx)

			if warnings != nil {
				result["warnings"] = warnings
			}

			if match != nil {
				result["feedback"] = match["feedback"]
				result["matched_case"] = match["id"]

				mark, exists := match["mark"].(map[string]interface{})
				if exists {
					result["is_correct"] = mark
				}
			}
		}
	}

	resData, err = json.Marshal(respBody)
	if err != nil {
		log.Error("failed to marshal response data", zap.Error(err))
		return nil, err
	}

	// Return the response data
	return resData, nil
}

func SendCommand(req Request, command Command, h *RuntimeHandler, ctx context.Context) ([]byte, error) {
	var reqData map[string]any

	// Parse the request data into a map
	if err := json.Unmarshal(req.Body, &reqData); err != nil {
		log.Debug("failed to unmarshal request data", zap.Error(err))
		return nil, err
	}

	// Validate the request data against the request schema
	if err := h.validate(validationTypeRequest, command, reqData); err != nil {
		return nil, err
	}

	// Create a new message with the parsed command and request data
	requestMsg := NewRequestMessage(command, reqData)

	// Let the runtime handle the message
	responseMsg, err := h.runtime.Handle(ctx, requestMsg)
	if err != nil {
		log.Error("failed to handle message", zap.Error(err))
		return nil, err
	}

	// Validate the response data against the response schema
	if err = h.validate(validationTypeResponse, command, responseMsg); err != nil {
		log.Error("failed to validate response data", zap.Error(err))
		return nil, err
	}

	resData, err := json.Marshal(responseMsg)
	if err != nil {
		log.Error("failed to marshal response data", zap.Error(err))
		return nil, err
	}

	return resData, nil
}

func GetCaseFeedback(
	response any,
	params map[string]any,
	cases []interface{},
	req Request,
	command Command,
	h *RuntimeHandler,
	ctx context.Context,
) (map[string]any, []CaseWarning) {
	// Simulate find_first_matching_case
	matches, feedback, warnings := FindFirstMatchingCase(params, cases, req, command, h, ctx)

	if len(matches) == 0 {
		return nil, warnings
	}

	matchID := matches[0]
	match := cases[matchID].(map[string]interface{})
	match["id"] = matchID

	matchParams, ok := match["params"].(map[string]any)
	if ok && matchParams["override_eval_feedback"] == true {
		matchFeedback := match["feedback"].(string)
		evalFeedback := feedback[0]
		match["feedback"] = matchFeedback + "<br />" + evalFeedback
	}

	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for i, id := range matches {
			ids[i] = fmt.Sprintf("%d", id)
		}
		warning := CaseWarning{
			Message: fmt.Sprintf("Cases %s were matched. Only the first one's feedback was returned", strings.Join(ids, ", ")),
		}
		warnings = append(warnings, warning)
	}

	return match, warnings
}

func FindFirstMatchingCase(params map[string]any, cases []interface{}, req Request, command Command, h *RuntimeHandler,
	ctx context.Context) ([]int, []string, []CaseWarning) {

	var matches []int
	var feedback []string
	var warnings []CaseWarning

	for index, c := range cases {
		result := EvaluateCase(params, c.(map[string]interface{}), index, req, command, h, ctx)

		if result.Warning != nil {
			warnings = append(warnings, *result.Warning)
		}

		if result.IsCorrect {
			matches = append(matches, index)
			feedback = append(feedback, result.Feedback)
			break
		}
	}

	return matches, feedback, warnings
}

func EvaluateCase(params map[string]any, caseData map[string]any, index int, req Request, command Command,
	h *RuntimeHandler, ctx context.Context) CaseResult {
	// Check for required fields
	if _, hasAnswer := caseData["answer"]; !hasAnswer {
		return CaseResult{
			Warning: &CaseWarning{
				Case:    index,
				Message: "Missing answer field",
			},
		}
	}
	if _, hasFeedback := caseData["feedback"]; !hasFeedback {
		return CaseResult{
			Warning: &CaseWarning{
				Case:    index,
				Message: "Missing feedback field",
			},
		}
	}

	// Merge params with case-specific params
	combinedParams := make(map[string]any)
	for k, v := range params {
		combinedParams[k] = v
	}
	if caseParams, ok := caseData["params"].(map[string]any); ok {
		for k, v := range caseParams {
			combinedParams[k] = v
		}
	}

	// Try evaluation
	defer func() {
		if r := recover(); r != nil {
			// Catch panic as generic error
			caseData["warning"] = &CaseWarning{
				Case:    index,
				Message: "An exception was raised while executing the evaluation function.",
			}
		}
	}()

	var reqBody map[string]interface{}
	err := json.Unmarshal(req.Body, &reqBody)
	if err != nil {
		return CaseResult{
			Warning: &CaseWarning{
				Case:    index,
				Message: err.Error(),
			},
		}
	}

	reqBody["answer"] = caseData["answer"]
	reqBody["params"] = combinedParams

	req.Body, err = json.Marshal(reqBody)
	if err != nil {
		return CaseResult{
			Warning: &CaseWarning{
				Case:    index,
				Message: err.Error(),
			},
		}
	}

	resData, err := SendCommand(req, command, h, ctx)
	if err != nil {
		return CaseResult{
			Warning: &CaseWarning{
				Case:    index,
				Message: err.Error(),
			},
		}
	}

	var respBody map[string]any
	err = json.Unmarshal(resData, &respBody)
	result, ok := respBody["result"].(map[string]interface{})
	if !ok {
		log.Error("failed to unmarshal response data", zap.Error(err))
		return CaseResult{
			Warning: &CaseWarning{
				Case:    index,
				Message: "failed to unmarshal response data",
			},
		}
	}

	isCorrect, _ := result["is_correct"].(bool)
	feedback, _ := result["feedback"].(string)

	return CaseResult{
		IsCorrect: isCorrect,
		Feedback:  feedback,
	}
}

// getCommand tries to extract the command from the request.
func (s *RuntimeHandler) getCommand(req Request) (string, bool) {
	if commandStr := req.Header.Get("command"); commandStr != "" {
		return commandStr, true
	}

	pathElements := strings.Split(strings.TrimPrefix(req.Path, "/"), "/")
	if len(pathElements) == 1 && pathElements[0] != "" {
		return pathElements[0], true
	}

	// if no command could be extracted from the request,
	// fall back to the `eval` command
	return "eval", true
}
