package qemurun

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodecRoundTrip(t *testing.T) {
	codec := NewCodec(1024)
	want := Frame{Type: FrameData, StreamID: 7, Payload: []byte("payload")}
	var wire bytes.Buffer
	require.NoError(t, codec.WriteFrame(&wire, want))
	got, err := codec.ReadFrame(&wire)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestCodecRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	codec := NewCodec(64)
	var wire bytes.Buffer
	require.NoError(t, binary.Write(&wire, binary.BigEndian, uint32(65)))
	wire.Write(make([]byte, 65))
	_, err := codec.ReadFrame(&wire)
	assert.ErrorIs(t, err, ErrFrameTooLarge)
}

func TestCodecRejectsInvalidStreamIDs(t *testing.T) {
	codec := NewCodec(1024)
	for _, frame := range []Frame{
		{Type: FrameData, StreamID: 0},
		{Type: FrameHello, StreamID: 1},
	} {
		var wire bytes.Buffer
		err := codec.WriteFrame(&wire, frame)
		assert.ErrorIs(t, err, ErrInvalidStreamID)
	}
}

func TestStreamTrackerEnforcesLifecycleAndLimit(t *testing.T) {
	tracker := NewStreamTracker(1)
	require.NoError(t, tracker.Accept(Frame{Type: FrameOpen, StreamID: 1}))
	assert.ErrorIs(t, tracker.Accept(Frame{Type: FrameOpen, StreamID: 2}), ErrTooManyStreams)
	require.NoError(t, tracker.Accept(Frame{Type: FrameData, StreamID: 1}))
	require.NoError(t, tracker.Accept(Frame{Type: FrameHalfClose, StreamID: 1}))
	assert.ErrorIs(t, tracker.Accept(Frame{Type: FrameData, StreamID: 1}), ErrInvalidStreamState)
	require.NoError(t, tracker.Accept(Frame{Type: FrameClose, StreamID: 1}))
	assert.Equal(t, 0, tracker.Len())
	assert.ErrorIs(t, tracker.Accept(Frame{Type: FrameClose, StreamID: 1}), ErrUnknownStream)
}

func TestCodecPropagatesShortWrites(t *testing.T) {
	codec := NewCodec(1024)
	err := codec.WriteFrame(zeroWriter{}, Frame{Type: FrameHello})
	assert.ErrorIs(t, err, bytes.ErrTooLarge)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, bytes.ErrTooLarge }
