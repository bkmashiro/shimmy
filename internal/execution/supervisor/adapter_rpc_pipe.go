package supervisor

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/lambda-feedback/shimmy/internal/protocol"
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
	if contentLength > protocol.DefaultMaxMessageBytes {
		return 0, fmt.Errorf(
			"%w: frame is %d bytes, limit is %d",
			protocol.ErrMessageTooLarge,
			contentLength,
			protocol.DefaultMaxMessageBytes,
		)
	}
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

	// Scan lines until we find Content-Length:, skipping bounded stray output.
	var contentLength int
	headerBytes := 0
	for {
		line, err := readFrameHeaderLine(h.reader, &headerBytes)
		if err != nil {
			return 0, err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "Content-Length") && !strings.HasPrefix(line, "Content-Length:") {
			return 0, fmt.Errorf("malformed Content-Length header")
		}
		if !strings.HasPrefix(line, "Content-Length:") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return 0, fmt.Errorf("malformed Content-Length header")
		}
		v, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, fmt.Errorf("invalid Content-Length value: %s", parts[1])
		}
		if v < 0 {
			return 0, fmt.Errorf("invalid Content-Length value: %d", v)
		}
		if v > protocol.DefaultMaxMessageBytes {
			return 0, fmt.Errorf(
				"%w: frame is %d bytes, limit is %d",
				protocol.ErrMessageTooLarge,
				v,
				protocol.DefaultMaxMessageBytes,
			)
		}
		contentLength = v
		break
	}

	// Drain remaining header lines until the blank separator.
	for {
		line, err := readFrameHeaderLine(h.reader, &headerBytes)
		if err != nil {
			return 0, err
		}
		if strings.TrimRight(line, "\r\n") == "" {
			break
		}
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

func readFrameHeaderLine(reader *bufio.Reader, totalBytes *int) (string, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if *totalBytes+len(fragment) > protocol.MaxFrameHeaderBytes {
			return "", fmt.Errorf(
				"frame header exceeds maximum size of %d bytes",
				protocol.MaxFrameHeaderBytes,
			)
		}
		*totalBytes += len(fragment)
		line = append(line, fragment...)

		switch err {
		case nil:
			return string(line), nil
		case bufio.ErrBufferFull:
			continue
		default:
			return "", err
		}
	}
}

// Close closes the wrapped ReadWriteCloser
func (p *headerPrefixPipe) Close() error {
	return p.stdio.Close()
}
