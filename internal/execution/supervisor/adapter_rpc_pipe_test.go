package supervisor

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderPrefixReadWriteCloser_WriteAndRead(t *testing.T) {
	buf := newRwc()
	rwc := &headerPrefixPipe{stdio: buf}

	// Test data to write
	data := []byte("Hello, World!")

	// Write the data
	n, err := rwc.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)

	// Read the data
	readBuffer := make([]byte, len(data))
	n, err = rwc.Read(readBuffer)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, readBuffer)
}

func TestHeaderPrefixReadWriteCloser_ReadIncompleteHeader(t *testing.T) {
	buf := newRwc()
	rwc := &headerPrefixPipe{stdio: buf}

	// Write data with a correct Content-Length header
	data := []byte("Test")
	header := "Content-Length: 4\r\n\r\n"
	message := append([]byte(header), data...)
	buf.Write(message[:len(message)-1]) // Write incomplete header

	// Attempt to read the data should fail
	readBuffer := make([]byte, len(data))
	_, err := rwc.Read(readBuffer)
	assert.Error(t, err)
}

func TestHeaderPrefixReadWriteCloser_Close(t *testing.T) {
	buf := newRwc()
	rwc := &headerPrefixPipe{stdio: buf}

	// Close the rwc
	err := rwc.Close()
	assert.NoError(t, err)
}

func TestHeaderPrefixPipe_LargePayload(t *testing.T) {
	buf := newRwc()
	rwc := &headerPrefixPipe{stdio: buf}

	// Write a payload larger than a typical 512-byte buffer
	data := []byte(strings.Repeat("x", 8192))

	n, err := rwc.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)

	// Read with a small buffer (simulating json.Decoder's initial ~512-byte read)
	result := make([]byte, 0, len(data))
	smallBuf := make([]byte, 512)
	for len(result) < len(data) {
		n, err = rwc.Read(smallBuf)
		require.NoError(t, err)
		result = append(result, smallBuf[:n]...)
	}

	assert.Equal(t, data, result)
}

func TestHeaderPrefixPipe_MultipleMessages(t *testing.T) {
	buf := newRwc()
	pipe := &headerPrefixPipe{stdio: buf}

	// Write two messages back-to-back
	msg1 := []byte(`{"jsonrpc":"2.0","id":1,"result":"first"}`)
	msg2 := []byte(`{"jsonrpc":"2.0","id":2,"result":"second"}`)

	header1 := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(msg1))
	header2 := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(msg2))
	buf.(*rwc).Buffer.Write([]byte(header1))
	buf.(*rwc).Buffer.Write(msg1)
	buf.(*rwc).Buffer.Write([]byte(header2))
	buf.(*rwc).Buffer.Write(msg2)

	// Read first message
	readBuf := make([]byte, 512)
	n, err := pipe.Read(readBuf)
	require.NoError(t, err)
	assert.Equal(t, msg1, readBuf[:n])

	// Read second message — exercises persistent bufio.Reader
	n, err = pipe.Read(readBuf)
	require.NoError(t, err)
	assert.Equal(t, msg2, readBuf[:n])
}

func TestHeaderPrefixPipe_SkipsStrayOutputBeforeContentLength(t *testing.T) {
	buf := newRwc()
	pipe := &headerPrefixPipe{stdio: buf}

	msg := []byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`)

	// Stray lines an evaluation function might emit on stdout before the
	// server loop starts (e.g. model-loading logs, library init messages).
	stray := "Loading model...\nReady.\n"
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(msg))
	buf.(*rwc).Buffer.Write([]byte(stray))
	buf.(*rwc).Buffer.Write([]byte(header))
	buf.(*rwc).Buffer.Write(msg)

	readBuf := make([]byte, 512)
	n, err := pipe.Read(readBuf)
	require.NoError(t, err)
	assert.Equal(t, msg, readBuf[:n])
}

func TestHeaderPrefixPipe_ConcurrentReadWrite(t *testing.T) {
	// Use two separate pipes: one for the write direction, one for the read
	// direction. This mirrors the real pipe topology where read and write are
	// on different OS pipes, so the reader never blocks the writer.
	inR, inW := io.Pipe()   // writerPipe writes here; readerPipe reads here
	outR, outW := io.Pipe() // readerPipe writes here; writerPipe reads here

	writerPipe := &headerPrefixPipe{stdio: &splitRWC{r: outR, w: inW}}
	readerPipe := &headerPrefixPipe{stdio: &splitRWC{r: inR, w: outW}}

	msg := []byte(`{"jsonrpc":"2.0","id":1,"method":"eval"}`)
	done := make(chan error, 2)

	// Goroutine 1: Write (mimics go-ethereum Send())
	go func() {
		_, err := writerPipe.Write(msg)
		done <- err
	}()

	// Goroutine 2: Read (mimics go-ethereum background reader)
	go func() {
		buf := make([]byte, 512)
		n, err := readerPipe.Read(buf)
		if err != nil {
			done <- err
			return
		}
		if !bytes.Equal(buf[:n], msg) {
			done <- fmt.Errorf("got %q want %q", buf[:n], msg)
			return
		}
		done <- nil
	}()

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("deadlock: concurrent Read and Write timed out")
		}
	}
}

// splitRWC allows separate r and w so reads and writes go to different pipes.
type splitRWC struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (s *splitRWC) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *splitRWC) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *splitRWC) Close() error {
	s.r.Close()
	return s.w.Close()
}
