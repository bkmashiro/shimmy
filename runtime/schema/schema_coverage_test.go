package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchema_Get_RequestSchema exercises the Get method on request schemas.
func TestSchema_Get_RequestSchema(t *testing.T) {
	s, err := NewRequestSchema()
	require.NoError(t, err)

	tests := []struct {
		name       string
		schemaType SchemaType
		wantErr    bool
	}{
		{"eval schema", SchemaTypeEval, false},
		{"preview schema", SchemaTypePreview, false},
		{"unknown schema type", SchemaType(999), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := s.Get(tc.schemaType)
			if tc.wantErr {
				assert.Error(t, err)
				assert.Nil(t, schema)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, schema)
			}
		})
	}
}

// TestSchema_Get_ResponseSchema exercises the Get method on response schemas.
func TestSchema_Get_ResponseSchema(t *testing.T) {
	s, err := NewResponseSchema()
	require.NoError(t, err)

	tests := []struct {
		name       string
		schemaType SchemaType
		wantErr    bool
	}{
		{"eval schema", SchemaTypeEval, false},
		{"preview schema", SchemaTypePreview, false},
		{"health schema", SchemaTypeHealth, false},
		{"unknown schema type", SchemaType(999), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := s.Get(tc.schemaType)
			if tc.wantErr {
				assert.Error(t, err)
				assert.Nil(t, schema)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, schema)
			}
		})
	}
}

// TestSchema_Validate_ValidEvalRequest tests validating a valid eval request.
func TestSchema_Validate_ValidEvalRequest(t *testing.T) {
	s, err := NewRequestSchema()
	require.NoError(t, err)

	validEval := map[string]any{
		"response": "hello",
		"answer":   "hello",
	}

	result, err := s.Validate(SchemaTypeEval, validEval)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid())
}

// TestSchema_Validate_InvalidEvalRequest tests validating an invalid eval request.
func TestSchema_Validate_InvalidEvalRequest(t *testing.T) {
	s, err := NewRequestSchema()
	require.NoError(t, err)

	// missing required fields
	invalidEval := map[string]any{}

	result, err := s.Validate(SchemaTypeEval, invalidEval)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Valid())
}

// TestSchema_Validate_ValidPreviewRequest tests validating a valid preview request.
func TestSchema_Validate_ValidPreviewRequest(t *testing.T) {
	s, err := NewRequestSchema()
	require.NoError(t, err)

	validPreview := map[string]any{
		"response": "hello",
	}

	result, err := s.Validate(SchemaTypePreview, validPreview)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid())
}

// TestSchema_Validate_UnknownSchemaType tests Validate with an unknown schema type.
func TestSchema_Validate_UnknownSchemaType(t *testing.T) {
	s, err := NewRequestSchema()
	require.NoError(t, err)

	_, err = s.Validate(SchemaType(999), map[string]any{})
	assert.Error(t, err)
}

// TestSchema_Validate_ValidEvalResponse tests validating a valid eval response.
func TestSchema_Validate_ValidEvalResponse(t *testing.T) {
	s, err := NewResponseSchema()
	require.NoError(t, err)

	validResponse := map[string]any{
		"command": "eval",
		"result": map[string]any{
			"is_correct": true,
			"feedback":   "Well done!",
		},
	}

	result, err := s.Validate(SchemaTypeEval, validResponse)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid())
}

// TestSchema_Validate_ValidPreviewResponse tests validating a valid preview response.
func TestSchema_Validate_ValidPreviewResponse(t *testing.T) {
	s, err := NewResponseSchema()
	require.NoError(t, err)

	validPreview := map[string]any{
		"command": "preview",
		"result": map[string]any{
			"preview": map[string]any{
				"latex": "x^2",
			},
		},
	}

	result, err := s.Validate(SchemaTypePreview, validPreview)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid())
}

// TestSchema_Validate_ValidHealthResponse tests validating a valid health response.
func TestSchema_Validate_ValidHealthResponse(t *testing.T) {
	s, err := NewResponseSchema()
	require.NoError(t, err)

	validHealth := map[string]any{
		"command": "healthcheck",
		"result": map[string]any{
			"tests_passed": true,
		},
	}

	result, err := s.Validate(SchemaTypeHealth, validHealth)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}
