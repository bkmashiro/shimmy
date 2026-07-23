package qemurun

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestSerializedFrameWriterPreventsConcurrentFrameInterleaving(t *testing.T) {
	host, peer := net.Pipe()
	defer host.Close()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writer := NewSerializedFrameWriter(ctx, host, NewCodec(1024), 4)

	const count = 32
	readFrames := make(chan Frame, count)
	readErr := make(chan error, 1)
	go func() {
		codec := NewCodec(1024)
		for range count {
			frame, err := codec.ReadFrame(peer)
			if err != nil {
				readErr <- err
				return
			}
			readFrames <- frame
		}
		readErr <- nil
	}()

	var group sync.WaitGroup
	for id := 1; id <= count; id++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			if err := writer.Write(ctx, Frame{Type: FrameData, StreamID: uint32(id), Payload: []byte{byte(id)}}); err != nil {
				t.Errorf("Write(%d): %v", id, err)
			}
		}(id)
	}
	group.Wait()
	if err := <-readErr; err != nil {
		t.Fatalf("read frames: %v", err)
	}
	seen := make(map[uint32]bool, count)
	for range count {
		frame := <-readFrames
		if len(frame.Payload) != 1 || frame.Payload[0] != byte(frame.StreamID) {
			t.Fatalf("corrupt frame: %#v", frame)
		}
		seen[frame.StreamID] = true
	}
	if len(seen) != count {
		t.Fatalf("received %d unique frames, want %d", len(seen), count)
	}
}

func TestSerializedFrameWriterPropagatesTransportFailure(t *testing.T) {
	want := errors.New("transport failed")
	writer := NewSerializedFrameWriter(context.Background(), failingWriter{err: want}, NewCodec(1024), 1)
	err := writer.Write(context.Background(), Frame{Type: FrameHello})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want transport failure", err)
	}
	if err := writer.Write(context.Background(), Frame{Type: FrameHello}); !errors.Is(err, want) {
		t.Fatalf("subsequent error = %v, want same transport failure", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

var _ io.Writer = failingWriter{}
