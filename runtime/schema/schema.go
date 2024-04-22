package schema

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/lambda-feedback/shimmy/models"
	"github.com/xeipuuv/gojsonschema"
)

type Schema struct {
	schemas map[models.Command]*gojsonschema.Schema
}

func new(eval *gojsonschema.Schema, preview *gojsonschema.Schema) *Schema {
	return &Schema{
		schemas: map[models.Command]*gojsonschema.Schema{
			models.CommandEvaluate: eval,
			models.CommandPreview:  preview,
		},
	}
}

func (s *Schema) Get(command models.Command) (*gojsonschema.Schema, error) {
	schema, ok := s.schemas[command]
	if !ok {
		return nil, errors.New("schema not found")
	}

	return schema, nil
}

func (s *Schema) Validate(command models.Command, data []byte) (*gojsonschema.Result, error) {
	var schema *gojsonschema.Schema

	schema, err := s.Get(command)
	if err != nil {
		return nil, err
	}

	return schema.Validate(gojsonschema.NewBytesLoader(data))
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
