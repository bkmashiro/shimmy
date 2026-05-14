package runtime_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lambda-feedback/shimmy/runtime"
)

func TestMuEdContentKey(t *testing.T) {
	cases := []struct {
		t    runtime.MuEdSubmissionType
		want string
	}{
		{runtime.MuEdMath, "expression"},
		{runtime.MuEdText, "text"},
		{runtime.MuEdCode, "code"},
		{runtime.MuEdModel, "model"},
		{runtime.MuEdOther, "value"},
		{"UNKNOWN", "value"},
	}

	for _, tc := range cases {
		t.Run(string(tc.t), func(t *testing.T) {
			// contentKey is unexported; exercise it via BuildLegacyEvalRequest
			req := runtime.MuEdEvaluateRequest{
				Submission: runtime.MuEdSubmission{
					Type:    tc.t,
					Content: map[string]any{tc.want: "x"},
				},
				Task: &runtime.MuEdTask{
					ReferenceSolution: &runtime.MuEdSubmission{
						Type:    tc.t,
						Content: map[string]any{tc.want: "x"},
					},
				},
			}
			body, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
			require.NoError(t, err)
			assert.Equal(t, "x", body["response"])
		})
	}
}

func TestMuEdBuildLegacyEvalRequest(t *testing.T) {
	t.Run("MATH primary key", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdMath,
				Content: map[string]any{"expression": "x^2"},
			},
			Task: &runtime.MuEdTask{
				ReferenceSolution: &runtime.MuEdSubmission{
					Type:    runtime.MuEdMath,
					Content: map[string]any{"expression": "x^2"},
				},
			},
		}
		body, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "x^2", body["response"])
		assert.Equal(t, "x^2", body["answer"])
		assert.Equal(t, map[string]any{}, body["params"])
	})

	t.Run("TEXT primary key", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdText,
				Content: map[string]any{"text": "hello"},
			},
			Task: &runtime.MuEdTask{
				ReferenceSolution: &runtime.MuEdSubmission{
					Type:    runtime.MuEdText,
					Content: map[string]any{"text": "hello"},
				},
			},
		}
		body, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "hello", body["response"])
		assert.Equal(t, "hello", body["answer"])
	})

	t.Run("OTHER type uses value key", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdOther,
				Content: map[string]any{"value": "foo"},
			},
			Task: &runtime.MuEdTask{
				ReferenceSolution: &runtime.MuEdSubmission{
					Type:    runtime.MuEdOther,
					Content: map[string]any{"value": "bar"},
				},
			},
		}
		body, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "foo", body["response"])
		assert.Equal(t, "bar", body["answer"])
	})

	t.Run("missing primary key falls back to value", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdMath,
				Content: map[string]any{"value": "x^2"},
			},
			Task: &runtime.MuEdTask{
				ReferenceSolution: &runtime.MuEdSubmission{
					Type:    runtime.MuEdMath,
					Content: map[string]any{"value": "x^2"},
				},
			},
		}
		body, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "x^2", body["response"])
		assert.Equal(t, "x^2", body["answer"])
	})

	t.Run("missing both keys returns error", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdMath,
				Content: map[string]any{"unrelated": "x"},
			},
			Task: &runtime.MuEdTask{
				ReferenceSolution: &runtime.MuEdSubmission{
					Type:    runtime.MuEdMath,
					Content: map[string]any{"expression": "x"},
				},
			},
		}
		_, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.Error(t, err)
	})

	t.Run("nil task returns error", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdMath,
				Content: map[string]any{"expression": "x^2"},
			},
			Task: nil,
		}
		_, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.Error(t, err)
	})

	t.Run("nil reference solution returns error", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdMath,
				Content: map[string]any{"expression": "x^2"},
			},
			Task: &runtime.MuEdTask{ReferenceSolution: nil},
		}
		_, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.Error(t, err)
	})

	t.Run("params forwarded from configuration", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdMath,
				Content: map[string]any{"expression": "x"},
			},
			Task: &runtime.MuEdTask{
				ReferenceSolution: &runtime.MuEdSubmission{
					Type:    runtime.MuEdMath,
					Content: map[string]any{"expression": "x"},
				},
			},
			Configuration: &runtime.MuEdConfiguration{
				Params: map[string]any{"strict": true},
			},
		}
		body, err := runtime.MuEdBuildLegacyEvaluateRequest(req)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"strict": true}, body["params"])
	})
}

func TestMuEdBuildLegacyPreviewRequest(t *testing.T) {
	t.Run("extracts response only", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdMath,
				Content: map[string]any{"expression": "x^2"},
			},
		}
		body, err := runtime.MuEdBuildLegacyPreviewRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "x^2", body["response"])
		_, hasAnswer := body["answer"]
		assert.False(t, hasAnswer)
	})

	t.Run("params forwarded", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdText,
				Content: map[string]any{"text": "hi"},
			},
			Configuration: &runtime.MuEdConfiguration{
				Params: map[string]any{"lang": "en"},
			},
		}
		body, err := runtime.MuEdBuildLegacyPreviewRequest(req)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"lang": "en"}, body["params"])
	})

	t.Run("nil configuration gives empty params", func(t *testing.T) {
		req := runtime.MuEdEvaluateRequest{
			Submission: runtime.MuEdSubmission{
				Type:    runtime.MuEdCode,
				Content: map[string]any{"code": "print()"},
			},
			Configuration: nil,
		}
		body, err := runtime.MuEdBuildLegacyPreviewRequest(req)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{}, body["params"])
	})
}

func TestMuEdToEvalFeedback(t *testing.T) {
	t.Run("is_correct true gives awardedPoints 1", func(t *testing.T) {
		result := map[string]any{"is_correct": true, "feedback": "Well done"}
		fb := runtime.MuEdToEvaluateFeedback(result)
		require.Len(t, fb, 1)
		assert.Equal(t, 1.0, fb[0]["awardedPoints"])
		assert.Equal(t, "Well done", fb[0]["message"])
	})

	t.Run("is_correct false gives awardedPoints 0", func(t *testing.T) {
		result := map[string]any{"is_correct": false, "feedback": "Try again"}
		fb := runtime.MuEdToEvaluateFeedback(result)
		require.Len(t, fb, 1)
		assert.Equal(t, 0.0, fb[0]["awardedPoints"])
	})

	t.Run("matched_case mapped to matchedCase int", func(t *testing.T) {
		result := map[string]any{"is_correct": false, "matched_case": float64(2)}
		fb := runtime.MuEdToEvaluateFeedback(result)
		assert.Equal(t, 2, fb[0]["matchedCase"])
	})

	t.Run("responseLatex present", func(t *testing.T) {
		result := map[string]any{"is_correct": true, "response_latex": "x^{2}"}
		fb := runtime.MuEdToEvaluateFeedback(result)
		assert.Equal(t, "x^{2}", fb[0]["responseLatex"])
	})

	t.Run("responseLatex absent is null in JSON", func(t *testing.T) {
		result := map[string]any{"is_correct": true}
		fb := runtime.MuEdToEvaluateFeedback(result)
		assert.Nil(t, fb[0]["responseLatex"])

		raw, err := json.Marshal(fb)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"responseLatex":null`)
	})

	t.Run("responseSimplified present", func(t *testing.T) {
		result := map[string]any{"is_correct": true, "response_simplified": "x^2"}
		fb := runtime.MuEdToEvaluateFeedback(result)
		assert.Equal(t, "x^2", fb[0]["responseSimplified"])
	})

	t.Run("responseSimplified absent is null in JSON", func(t *testing.T) {
		result := map[string]any{"is_correct": true}
		fb := runtime.MuEdToEvaluateFeedback(result)
		assert.Nil(t, fb[0]["responseSimplified"])

		raw, err := json.Marshal(fb)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"responseSimplified":null`)
	})

	t.Run("tags mapped", func(t *testing.T) {
		result := map[string]any{"is_correct": true, "tags": []any{"algebra", "calculus"}}
		fb := runtime.MuEdToEvaluateFeedback(result)
		assert.Equal(t, []string{"algebra", "calculus"}, fb[0]["tags"])
	})
}

func TestMuEdToPreviewFeedback(t *testing.T) {
	result := map[string]any{"preview": map[string]any{"latex": "x^2"}}
	fb := runtime.MuEdToPreviewFeedback(result)
	require.Len(t, fb, 1)
	assert.Equal(t, result, fb[0]["preSubmissionFeedback"])
}
