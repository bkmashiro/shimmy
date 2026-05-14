package runtime

import "fmt"

type MuEdSubmissionType string

const (
	MuEdMath  MuEdSubmissionType = "MATH"
	MuEdText  MuEdSubmissionType = "TEXT"
	MuEdCode  MuEdSubmissionType = "CODE"
	MuEdModel MuEdSubmissionType = "MODEL"
	MuEdOther MuEdSubmissionType = "OTHER"
)

type MuEdSubmission struct {
	Type    MuEdSubmissionType `json:"type"`
	Content map[string]any     `json:"content"`
}

type MuEdTask struct {
	ReferenceSolution *MuEdSubmission `json:"referenceSolution"`
}

type MuEdConfiguration struct {
	Params map[string]any `json:"params"`
}

type MuEdPreSubmissionFeedback struct {
	Enabled bool `json:"enabled"`
}

type MuEdEvaluateRequest struct {
	Submission            MuEdSubmission             `json:"submission"`
	Task                  *MuEdTask                  `json:"task"`
	Configuration         *MuEdConfiguration         `json:"configuration"`
	PreSubmissionFeedback *MuEdPreSubmissionFeedback `json:"preSubmissionFeedback"`
}

func muEdContentKey(t MuEdSubmissionType) string {
	switch t {
	case MuEdMath:
		return "expression"
	case MuEdText:
		return "text"
	case MuEdCode:
		return "code"
	case MuEdModel:
		return "model"
	default:
		return "value"
	}
}

func muEdExtractContent(content map[string]any, t MuEdSubmissionType) (any, error) {
	key := muEdContentKey(t)
	if val, ok := content[key]; ok {
		return val, nil
	}
	if t != MuEdOther {
		if val, ok := content["value"]; ok {
			return val, nil
		}
	}
	return nil, fmt.Errorf("could not extract content for submission type %s", t)
}

func muEdExtractParams(req MuEdEvaluateRequest) map[string]any {
	if req.Configuration != nil && req.Configuration.Params != nil {
		return req.Configuration.Params
	}
	return map[string]any{}
}

// MuEdBuildLegacyEvaluateRequest builds {response, answer, params} for the evaluate command.
func MuEdBuildLegacyEvaluateRequest(req MuEdEvaluateRequest) (map[string]any, error) {
	response, err := muEdExtractContent(req.Submission.Content, req.Submission.Type)
	if err != nil {
		return nil, fmt.Errorf("submission: %w", err)
	}

	if req.Task == nil || req.Task.ReferenceSolution == nil {
		return nil, fmt.Errorf("task.referenceSolution is required for evaluation")
	}

	sol := req.Task.ReferenceSolution
	answer, err := muEdExtractContent(sol.Content, sol.Type)
	if err != nil {
		return nil, fmt.Errorf("referenceSolution: %w", err)
	}

	return map[string]any{
		"response": response,
		"answer":   answer,
		"params":   muEdExtractParams(req),
	}, nil
}

// MuEdBuildLegacyPreviewRequest builds {response, params} for the preview command.
func MuEdBuildLegacyPreviewRequest(req MuEdEvaluateRequest) (map[string]any, error) {
	response, err := muEdExtractContent(req.Submission.Content, req.Submission.Type)
	if err != nil {
		return nil, fmt.Errorf("submission: %w", err)
	}

	return map[string]any{
		"response": response,
		"params":   muEdExtractParams(req),
	}, nil
}

// MuEdToEvaluateFeedback transforms a legacy result map into a muEd Feedback array.
// responseLatex and responseSimplified are always present in the output (null when absent).
func MuEdToEvaluateFeedback(result map[string]any) []map[string]any {
	feedback := map[string]any{
		"responseLatex":      nil,
		"responseSimplified": nil,
	}

	if isCorrect, ok := result["is_correct"].(bool); ok {
		pts := 0.0
		if isCorrect {
			pts = 1.0
		}
		feedback["awardedPoints"] = pts
	}

	if msg, ok := result["feedback"].(string); ok {
		feedback["message"] = msg
	}

	if mc, ok := result["matched_case"].(float64); ok {
		feedback["matchedCase"] = int(mc)
	}

	if rl, ok := result["response_latex"].(string); ok {
		feedback["responseLatex"] = rl
	}

	if rs, ok := result["response_simplified"].(string); ok {
		feedback["responseSimplified"] = rs
	}

	if tags, ok := result["tags"].([]any); ok {
		strTags := make([]string, 0, len(tags))
		for _, t := range tags {
			if s, ok := t.(string); ok {
				strTags = append(strTags, s)
			}
		}
		feedback["tags"] = strTags
	}

	return []map[string]any{feedback}
}

// MuEdToPreviewFeedback wraps a legacy preview result as [{"preSubmissionFeedback": result}].
func MuEdToPreviewFeedback(result map[string]any) []map[string]any {
	return []map[string]any{
		{"preSubmissionFeedback": result},
	}
}
