package schema

import "testing"

func TestNewRequestSchema(t *testing.T) {
	_, err := NewRequestSchema()
	if err != nil {
		t.Errorf("NewRequestSchema() returned an error: %v", err)
	}
}

func TestNewResponseSchema(t *testing.T) {
	_, err := NewResponseSchema()
	if err != nil {
		t.Errorf("NewResponseSchema() returned an error: %v", err)
	}
}
