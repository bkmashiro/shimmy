package runtime

import "net/http"

// Request represents an incoming request.
type Request struct {
	Path   string
	Method string
	Body   []byte
	Header http.Header
}

// Response represents an outgoing response.
type Response struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}
