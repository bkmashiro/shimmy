package runtime

import (
	"github.com/lambda-feedback/shimmy/models"
	"github.com/xeipuuv/gojsonschema"
	"go.uber.org/zap"
)

// validationType is the type of validation.
type validationType int

const (
	// validationTypeRequest is the type of validation for requests.
	validationTypeRequest validationType = iota

	// validationTypeResponse is the type of validation for responses.
	validationTypeResponse
)

func (t validationType) String() string {
	switch t {
	case validationTypeRequest:
		return "request"
	case validationTypeResponse:
		return "response"
	default:
		return "unknown"
	}
}

// validationError is an error that occurs during validation.
type validationError struct {
	Type   validationType
	Result *gojsonschema.Result
}

// newValidationError creates a new validation error.
func newValidationError(t validationType, result *gojsonschema.Result) *validationError {
	return &validationError{
		Type:   t,
		Result: result,
	}
}

func (e *validationError) Error() string {
	// TODO: error msg
	return ""
}

// validate validates the data against the schema for the given command.
func (r *RuntimeHandler) validate(
	t validationType,
	command models.Command,
	data []byte,
) error {
	log := r.log.With(
		zap.String("command", string(command)),
		zap.Stringer("type", t),
	)

	schema, ok := r.schemas[t]
	if !ok {
		log.Error("validation schema not found")
		return errSchemaNotFound
	}

	res, err := schema.Validate(command, data)
	if err != nil {
		log.Debug("validation failed", zap.Error(err))
		return errValidationFailed
	}

	if res.Valid() {
		return nil
	}

	r.log.Debug("invalid data", zap.Any("errors", res.Errors()))

	return newValidationError(t, res)
}
