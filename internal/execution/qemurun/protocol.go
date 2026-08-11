package qemurun

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	frameMetadataBytes = 5 // one frame type byte plus one uint32 stream ID
	defaultMaxFrame    = 1 << 20
)

var (
	ErrFrameTooLarge      = errors.New("qemu protocol: frame too large")
	ErrInvalidFrame       = errors.New("qemu protocol: invalid frame")
	ErrUnknownFrameType   = errors.New("qemu protocol: unknown frame type")
	ErrInvalidStreamID    = errors.New("qemu protocol: invalid stream id")
	ErrDuplicateStream    = errors.New("qemu protocol: duplicate stream")
	ErrUnknownStream      = errors.New("qemu protocol: unknown stream")
	ErrTooManyStreams     = errors.New("qemu protocol: too many streams")
	ErrInvalidStreamState = errors.New("qemu protocol: invalid stream state")
)

type FrameType byte

const (
	FrameHello FrameType = iota + 1
	FrameReady
	FrameStart
	FrameExit
	FrameFileRequest
	FrameFileResult
	FrameOpen
	FrameData
	FrameHalfClose
	FrameClose
	FrameStreamError
	FrameCancel
)

type Frame struct {
	Type     FrameType
	StreamID uint32
	Payload  []byte
}

type Codec struct {
	maxFrameBytes uint32
}

func NewCodec(maxFrameBytes int) Codec {
	if maxFrameBytes <= 0 {
		maxFrameBytes = defaultMaxFrame
	}
	return Codec{maxFrameBytes: uint32(maxFrameBytes)}
}

func (c Codec) WriteFrame(w io.Writer, frame Frame) error {
	if err := validateFrame(frame); err != nil {
		return err
	}
	bodyLen := uint64(frameMetadataBytes) + uint64(len(frame.Payload))
	if bodyLen > uint64(c.maxFrameBytes) || bodyLen > uint64(^uint32(0)) {
		return ErrFrameTooLarge
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(bodyLen))
	if err := writeAll(w, header[:]); err != nil {
		return fmt.Errorf("qemu protocol: write length: %w", err)
	}

	metadata := [frameMetadataBytes]byte{byte(frame.Type)}
	binary.BigEndian.PutUint32(metadata[1:], frame.StreamID)
	if err := writeAll(w, metadata[:]); err != nil {
		return fmt.Errorf("qemu protocol: write metadata: %w", err)
	}
	if err := writeAll(w, frame.Payload); err != nil {
		return fmt.Errorf("qemu protocol: write payload: %w", err)
	}
	return nil
}

func (c Codec) ReadFrame(r io.Reader) (Frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	bodyLen := binary.BigEndian.Uint32(header[:])
	if bodyLen < frameMetadataBytes {
		return Frame{}, ErrInvalidFrame
	}
	if bodyLen > c.maxFrameBytes {
		return Frame{}, ErrFrameTooLarge
	}

	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, err
	}
	frame := Frame{
		Type:     FrameType(body[0]),
		StreamID: binary.BigEndian.Uint32(body[1:frameMetadataBytes]),
		Payload:  append([]byte(nil), body[frameMetadataBytes:]...),
	}
	if err := validateFrame(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func validateFrame(frame Frame) error {
	if !frame.Type.valid() {
		return fmt.Errorf("%w: %d", ErrUnknownFrameType, frame.Type)
	}
	if frame.Type.requiresStream() && frame.StreamID == 0 {
		return fmt.Errorf("%w: %s requires a non-zero id", ErrInvalidStreamID, frame.Type)
	}
	if frame.Type.requiresControl() && frame.StreamID != 0 {
		return fmt.Errorf("%w: %s requires id zero", ErrInvalidStreamID, frame.Type)
	}
	return nil
}

func (t FrameType) valid() bool {
	return t >= FrameHello && t <= FrameCancel
}

func (t FrameType) requiresStream() bool {
	switch t {
	case FrameOpen, FrameData, FrameHalfClose, FrameClose, FrameStreamError:
		return true
	default:
		return false
	}
}

func (t FrameType) requiresControl() bool {
	switch t {
	case FrameHello, FrameReady, FrameStart, FrameExit, FrameFileRequest, FrameFileResult:
		return true
	default:
		return false
	}
}

func (t FrameType) String() string {
	switch t {
	case FrameHello:
		return "hello"
	case FrameReady:
		return "ready"
	case FrameStart:
		return "start"
	case FrameExit:
		return "exit"
	case FrameFileRequest:
		return "file_request"
	case FrameFileResult:
		return "file_result"
	case FrameOpen:
		return "open"
	case FrameData:
		return "data"
	case FrameHalfClose:
		return "half_close"
	case FrameClose:
		return "close"
	case FrameStreamError:
		return "stream_error"
	case FrameCancel:
		return "cancel"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

type streamState struct {
	halfClosed bool
}

type StreamTracker struct {
	mu         sync.Mutex
	maxStreams int
	streams    map[uint32]streamState
}

func NewStreamTracker(maxStreams int) *StreamTracker {
	if maxStreams <= 0 {
		maxStreams = 1
	}
	return &StreamTracker{maxStreams: maxStreams, streams: make(map[uint32]streamState)}
}

func (t *StreamTracker) Accept(frame Frame) error {
	if err := validateFrame(frame); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	switch frame.Type {
	case FrameOpen:
		if _, exists := t.streams[frame.StreamID]; exists {
			return ErrDuplicateStream
		}
		if len(t.streams) >= t.maxStreams {
			return ErrTooManyStreams
		}
		t.streams[frame.StreamID] = streamState{}
		return nil

	case FrameData:
		state, exists := t.streams[frame.StreamID]
		if !exists {
			return ErrUnknownStream
		}
		if state.halfClosed {
			return ErrInvalidStreamState
		}
		return nil

	case FrameHalfClose:
		state, exists := t.streams[frame.StreamID]
		if !exists {
			return ErrUnknownStream
		}
		if state.halfClosed {
			return ErrInvalidStreamState
		}
		state.halfClosed = true
		t.streams[frame.StreamID] = state
		return nil

	case FrameClose, FrameStreamError:
		if _, exists := t.streams[frame.StreamID]; !exists {
			return ErrUnknownStream
		}
		delete(t.streams, frame.StreamID)
		return nil

	case FrameCancel:
		if frame.StreamID == 0 {
			return nil
		}
		if _, exists := t.streams[frame.StreamID]; !exists {
			return ErrUnknownStream
		}
		delete(t.streams, frame.StreamID)
		return nil

	default:
		return nil
	}
}

func (t *StreamTracker) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.streams)
}
