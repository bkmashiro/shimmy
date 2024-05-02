package schema

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/xeipuuv/gojsonschema"
)

type SchemaType int

const (
	SchemaTypeEval SchemaType = iota
	SchemaTypePreview
)

type Schema struct {
	schemas map[SchemaType]*gojsonschema.Schema
}

func new(eval *gojsonschema.Schema, preview *gojsonschema.Schema) *Schema {
	return &Schema{
		schemas: map[SchemaType]*gojsonschema.Schema{
			SchemaTypeEval:    eval,
			SchemaTypePreview: preview,
		},
	}
}

func (s *Schema) Get(schemaType SchemaType) (*gojsonschema.Schema, error) {
	schema, ok := s.schemas[schemaType]
	if !ok {
		return nil, errors.New("schema not found")
	}

	return schema, nil
}

func (s *Schema) Validate(schemaType SchemaType, data map[string]any) (*gojsonschema.Result, error) {
	var schema *gojsonschema.Schema

	schema, err := s.Get(schemaType)
	if err != nil {
		return nil, err
	}

	return schema.Validate(gojsonschema.NewGoLoader(data))
}

//go:embed request-eval.json
var evalRequest json.RawMessage
var evalRequestLoader = gojsonschema.NewBytesLoader(evalRequest)

//go:embed request-preview.json
var previewRequest json.RawMessage
var previewRequestLoader = gojsonschema.NewBytesLoader(previewRequest)

func NewRequestSchema() (*Schema, error) {
	evalSchema, err := gojsonschema.NewSchema(evalRequestLoader)
	if err != nil {
		return nil, err
	}

	previewSchema, err := gojsonschema.NewSchema(previewRequestLoader)
	if err != nil {
		return nil, err
	}

	return new(evalSchema, previewSchema), nil
}

//go:embed response-eval.json
var evalResponse json.RawMessage
var evalResponseLoader = gojsonschema.NewBytesLoader(evalResponse)

//go:embed response-preview.json
var previewResponse json.RawMessage
var previewResponseLoader = gojsonschema.NewBytesLoader(previewResponse)

func NewResponseSchema() (*Schema, error) {
	evalSchema, err := gojsonschema.NewSchema(evalResponseLoader)
	if err != nil {
		return nil, err
	}

	previewSchema, err := gojsonschema.NewSchema(previewResponseLoader)
	if err != nil {
		return nil, err
	}

	return new(evalSchema, previewSchema), nil
}
