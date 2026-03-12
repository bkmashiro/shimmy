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
	stdio  io.ReadWriteCloser
	rmu    sync.Mutex    // guards Read path
	wmu    sync.Mutex    // guards Write path
	reader *bufio.Reader // persistent; avoids discarding pre-buffered bytes
	buf    []byte        // overflow from a read where contentLength > len(p)
}

// Write writes data with an LSP-style header to the wrapped ReadWriteCloser
func (h *headerPrefixPipe) Write(p []byte) (int, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()

	contentLength := len(p)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", contentLength)

	if _, err := h.stdio.Write([]byte(header)); err != nil {
		return 0, err
	}

	return h.stdio.Write(p)
}

func (h *headerPrefixPipe) Read(p []byte) (int, error) {
	h.rmu.Lock()
	defer h.rmu.Unlock()

	// Return leftover bytes from a previous oversized message first
	if len(h.buf) > 0 {
		n := copy(p, h.buf)
		h.buf = h.buf[n:]
		return n, nil
	}

	if h.reader == nil {
		h.reader = bufio.NewReader(h.stdio)
	}

	// read headers
	headers := ""
	for {
		line, err := h.reader.ReadString('\n')
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

	// Read exactly contentLength bytes
	content := make([]byte, contentLength)
	n, err := io.ReadFull(h.reader, content)
	if err == io.ErrUnexpectedEOF {
		return 0, fmt.Errorf("unexpected EOF, expected %d bytes, got %d bytes", contentLength, n)
	}
	if err != nil {
		return 0, err
	}

	// Copy into p; if message exceeds p, buffer the remainder
	copied := copy(p, content)
	if copied < contentLength {
		h.buf = content[copied:]
	}
	return copied, nil
}

// Close closes the wrapped ReadWriteCloser
func (p *headerPrefixPipe) Close() error {
	return p.stdio.Close()
}
