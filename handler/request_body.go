package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/lambda-feedback/shimmy/internal/protocol"
)

func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body := http.MaxBytesReader(w, r.Body, protocol.DefaultMaxMessageBytes)
	defer body.Close()

	value, err := io.ReadAll(body)
	if err == nil {
		return value, nil
	}

	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return nil, protocol.ErrMessageTooLarge
	}
	return nil, err
}

func writeRequestBodyError(w http.ResponseWriter, err error) {
	if errors.Is(err, protocol.ErrMessageTooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "failed to read body", http.StatusBadRequest)
}
