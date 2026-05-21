package schema

import (
	"testing"
)

func TestResponseSchemaValidation(t *testing.T) {
	s, err := NewResponseSchema()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		data    map[string]any
		wantOK  bool
	}{
		{
			name: "normal success - is_correct=true",
			data: map[string]any{
				"command": "eval",
				"result":  map[string]any{"is_correct": true},
			},
			wantOK: true,
		},
		{
			name: "normal failure - is_correct=false",
			data: map[string]any{
				"command": "eval",
				"result":  map[string]any{"is_correct": false, "feedback": "wrong"},
			},
			wantOK: true,
		},
		{
			name: "python exception in result - error key, no is_correct",
			data: map[string]any{
				"command": "eval",
				"result": map[string]any{
					"error":      "ValueError: invalid literal for int()",
					"error_type": "ValueError",
					"lineno":     5,
					"traceback":  "Traceback...",
				},
			},
			wantOK: true,
		},
		{
			name: "missing is_correct without error - should fail",
			data: map[string]any{
				"command": "eval",
				"result":  map[string]any{"feedback": "ok"},
			},
			wantOK: false,
		},
		{
			name: "top-level error",
			data: map[string]any{
				"error": map[string]any{"message": "something went wrong"},
			},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := s.Validate(SchemaTypeEval, tt.data)
			if err != nil {
				t.Fatalf("Validate returned error: %v", err)
			}
			gotOK := res.Valid()
			if gotOK != tt.wantOK {
				t.Errorf("Valid()=%v, want %v; errors: %v", gotOK, tt.wantOK, res.Errors())
			}
		})
	}
}
