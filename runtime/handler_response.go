package runtime

import (
	"encoding/json"
	"net/http"
)

func getErrorStatusCode(err error) int {
	if status, ok := wellKnownErrors[err]; ok {
		return status
	}

	if _, ok := err.(*validationError); ok {
		return http.StatusUnprocessableEntity
	}

	return http.StatusInternalServerError
}

func newErrorResponse(err error) Response {
	statusCode := getErrorStatusCode(err)

	errResponse := ErrorResponse{
		Message: err.Error(),
	}

	// TODO: probably attach validation errors here

	body, err := json.Marshal(errResponse)
	if err != nil {
		return Response{StatusCode: http.StatusInternalServerError}
	}

	return newResponse(statusCode, body)
}

func newResponse(status int, body []byte) Response {
	header := make(http.Header)
	header.Add("Content-Type", "application/json")

	return Response{
		StatusCode: status,
		Body:       body,
		Header:     header,
	}
}
