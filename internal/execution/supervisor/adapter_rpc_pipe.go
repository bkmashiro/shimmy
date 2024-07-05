package supervisor

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// headerPrefixPipe wraps another io.ReadWriteCloser and adds LSP-style headers
type headerPrefixPipe struct {
	stdio io.ReadWriteCloser
	mu    sync.Mutex
}

// Write writes data with an LSP-style header to the wrapped ReadWriteCloser
func (h *headerPrefixPipe) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	contentLength := len(p)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLength)

	if _, err := h.stdio.Write([]byte(header)); err != nil {
		return 0, err
	}

	return h.stdio.Write(p)
}

func (h *headerPrefixPipe) Read(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	reader := bufio.NewReader(h.stdio)

	// read headers
	headers := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}

		headers += line

		// Detect the end of headers with double CRLF
		if strings.HasSuffix(headers, "\r\n\r\n") {
			break
		}
	}
	headers = strings.TrimSpace(headers)

	// get content-length value
	var contentLength int
	lines := strings.Split(headers, "\r\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Content-Length:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				return 0, fmt.Errorf("malformed Content-Length header")
			}

			lengthStr := strings.TrimSpace(parts[1])

			if lengthValue, err := strconv.Atoi(lengthStr); err != nil {
				return 0, fmt.Errorf("invalid Content-Length value: %s", lengthStr)
			} else {
				contentLength = lengthValue
			}

			// found the content-length
			break

		}
	}

	if contentLength == 0 {
		return 0, fmt.Errorf("Content-Length header not found or zero")
	}

	if contentLength > len(p) {
		return 0, fmt.Errorf("buffer too small for content length: %d", contentLength)
	}

	// read content
	n, err := io.ReadFull(reader, p[:contentLength])
	if err == io.ErrUnexpectedEOF {
		return n, fmt.Errorf("unexpected EOF, expected %d bytes, got %d bytes", contentLength, n)
	}
	if err != nil {
		return n, err
	}

	return n, nil
}

// Close closes the wrapped ReadWriteCloser
func (p *headerPrefixPipe) Close() error {
	return p.stdio.Close()
}
