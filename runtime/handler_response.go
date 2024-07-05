package runtime

import (
	"encoding/json"
	"net/http"
)

// getErrorStatusCode returns the status code for the given error.
func getErrorStatusCode(err error) int {
	if status, ok := wellKnownErrors[err]; ok {
		return status
	}

	if err, ok := err.(*validationError); ok && err.Type == validationTypeRequest {
		return http.StatusUnprocessableEntity
	}

	return http.StatusInternalServerError
}

// newErrorResponse creates a new error response.
func newErrorResponse(err error) Response {
	statusCode := getErrorStatusCode(err)

	type responseError struct {
		Message string              `json:"message"`
		Error   string              `json:"error_thrown,omitempty"`
		Fields  map[string][]string `json:"fields,omitempty"`
	}

	responseErr := responseError{
		Message: err.Error(),
		Fields:  make(map[string][]string),
	}

	if validationErr, ok := err.(*validationError); ok {
		for _, err := range validationErr.Result.Errors() {
			responseErr.Fields[err.Field()] = append(responseErr.Fields[err.Field()], err.Description())
		}
	}

	body, err := json.Marshal(struct {
		Error responseError `json:"error"`
	}{
		Error: responseErr,
	})
	if err != nil {
		return Response{StatusCode: http.StatusInternalServerError}
	}

	return newResponse(statusCode, body)
}

// newResponse creates a new response.
func newResponse(status int, body []byte) Response {
	header := make(http.Header)
	header.Add("Content-Type", "application/json")

	return Response{
		StatusCode: status,
		Body:       body,
		Header:     header,
	}
}
