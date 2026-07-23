package qemurun

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestCodecRoundTripPreservesRawPayload(t *testing.T) {
	var wire bytes.Buffer
	codec := NewCodec(1024)
	want := Frame{Type: FrameData, StreamID: 7, Payload: []byte{0x00, 0xff, '\n', 'x'}}

	if err := codec.WriteFrame(&wire, want); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := codec.ReadFrame(&wire)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Type != want.Type || got.StreamID != want.StreamID || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestCodecHelloGoldenBytes(t *testing.T) {
	var wire bytes.Buffer
	codec := NewCodec(1024)
	if err := codec.WriteFrame(&wire, Frame{Type: FrameHello, Payload: []byte("{}")}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	const wantHex = "0000000701000000007b7d"
	if got := hex.EncodeToString(wire.Bytes()); got != wantHex {
		t.Fatalf("wire bytes = %s, want %s", got, wantHex)
	}
}

func TestCodecRejectsOversizedPayloadBeforeWrite(t *testing.T) {
	var wire bytes.Buffer
	codec := NewCodec(8) // 5-byte body header + at most 3 payload bytes.
	err := codec.WriteFrame(&wire, Frame{Type: FrameData, StreamID: 1, Payload: []byte("four")})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v, want ErrFrameTooLarge", err)
	}
	if wire.Len() != 0 {
		t.Fatalf("oversized frame wrote %d bytes", wire.Len())
	}
}

func TestCodecRejectsTruncatedBody(t *testing.T) {
	codec := NewCodec(1024)
	wire := bytes.NewReader([]byte{0, 0, 0, 7, byte(FrameHello), 0, 0})
	_, err := codec.ReadFrame(wire)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestCodecRejectsUnknownFrameType(t *testing.T) {
	codec := NewCodec(1024)
	wire := bytes.NewReader([]byte{0, 0, 0, 5, 0xff, 0, 0, 0, 0})
	_, err := codec.ReadFrame(wire)
	if !errors.Is(err, ErrUnknownFrameType) {
		t.Fatalf("error = %v, want ErrUnknownFrameType", err)
	}
}

func TestCodecValidatesControlAndStreamIDs(t *testing.T) {
	codec := NewCodec(1024)
	cases := []Frame{
		{Type: FrameHello, StreamID: 1},
		{Type: FrameReady, StreamID: 1},
		{Type: FrameFileRequest, StreamID: 1},
		{Type: FrameOpen},
		{Type: FrameData},
		{Type: FrameHalfClose},
		{Type: FrameClose},
		{Type: FrameStreamError},
	}
	for _, frame := range cases {
		var wire bytes.Buffer
		if err := codec.WriteFrame(&wire, frame); !errors.Is(err, ErrInvalidStreamID) {
			t.Fatalf("frame %v error = %v, want ErrInvalidStreamID", frame.Type, err)
		}
	}
}

func TestStreamTrackerRejectsDuplicateAndUnknownStreams(t *testing.T) {
	tracker := NewStreamTracker(2)
	if err := tracker.Accept(Frame{Type: FrameOpen, StreamID: 1}); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := tracker.Accept(Frame{Type: FrameOpen, StreamID: 1}); !errors.Is(err, ErrDuplicateStream) {
		t.Fatalf("duplicate open error = %v", err)
	}
	if err := tracker.Accept(Frame{Type: FrameData, StreamID: 2}); !errors.Is(err, ErrUnknownStream) {
		t.Fatalf("unknown data error = %v", err)
	}
}

func TestStreamTrackerBoundsConcurrentStreams(t *testing.T) {
	tracker := NewStreamTracker(1)
	if err := tracker.Accept(Frame{Type: FrameOpen, StreamID: 1}); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := tracker.Accept(Frame{Type: FrameOpen, StreamID: 2}); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("second open error = %v, want ErrTooManyStreams", err)
	}
	if err := tracker.Accept(Frame{Type: FrameClose, StreamID: 1}); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	if err := tracker.Accept(Frame{Type: FrameOpen, StreamID: 2}); err != nil {
		t.Fatalf("open after close: %v", err)
	}
}

func TestStreamTrackerRejectsRepeatedHalfClose(t *testing.T) {
	tracker := NewStreamTracker(1)
	if err := tracker.Accept(Frame{Type: FrameOpen, StreamID: 1}); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := tracker.Accept(Frame{Type: FrameHalfClose, StreamID: 1}); err != nil {
		t.Fatalf("half close: %v", err)
	}
	if err := tracker.Accept(Frame{Type: FrameHalfClose, StreamID: 1}); !errors.Is(err, ErrInvalidStreamState) {
		t.Fatalf("second half close error = %v, want ErrInvalidStreamState", err)
	}
}

func TestStreamTrackerAllowsControlFramesWithoutChangingStreams(t *testing.T) {
	tracker := NewStreamTracker(1)
	for _, typ := range []FrameType{FrameHello, FrameReady, FrameStart, FrameExit, FrameFileRequest, FrameFileResult, FrameCancel} {
		if err := tracker.Accept(Frame{Type: typ}); err != nil {
			t.Fatalf("control frame %v: %v", typ, err)
		}
	}
	if tracker.Len() != 0 {
		t.Fatalf("control frames changed stream count to %d", tracker.Len())
	}
}
