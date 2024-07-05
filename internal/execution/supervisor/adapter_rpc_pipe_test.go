package supervisor

import (
	"testing"

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
