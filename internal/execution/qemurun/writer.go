package qemurun

import (
	"context"
	"io"
	"sync"
)

type frameWriteRequest struct {
	frame  Frame
	result chan error
}

type SerializedFrameWriter struct {
	queue chan frameWriteRequest
	done  chan struct{}

	mu  sync.Mutex
	err error
}

func NewSerializedFrameWriter(
	ctx context.Context,
	transport io.Writer,
	codec Codec,
	queueDepth int,
) *SerializedFrameWriter {
	if queueDepth <= 0 {
		queueDepth = 1
	}
	writer := &SerializedFrameWriter{
		queue: make(chan frameWriteRequest, queueDepth),
		done:  make(chan struct{}),
	}
	go writer.run(ctx, transport, codec)
	return writer
}

func (w *SerializedFrameWriter) Write(ctx context.Context, frame Frame) error {
	request := frameWriteRequest{frame: frame, result: make(chan error, 1)}
	select {
	case w.queue <- request:
	case <-w.done:
		return w.Error()
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-request.result:
		return err
	case <-w.done:
		select {
		case err := <-request.result:
			return err
		default:
			return w.Error()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *SerializedFrameWriter) Error() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *SerializedFrameWriter) run(ctx context.Context, transport io.Writer, codec Codec) {
	for {
		select {
		case <-ctx.Done():
			w.finish(ctx.Err())
			return
		case request := <-w.queue:
			err := codec.WriteFrame(transport, request.frame)
			request.result <- err
			if err != nil {
				w.finish(err)
				return
			}
		}
	}
}

func (w *SerializedFrameWriter) finish(err error) {
	w.mu.Lock()
	w.err = err
	w.mu.Unlock()
	close(w.done)
	for {
		select {
		case request := <-w.queue:
			request.result <- err
		default:
			return
		}
	}
}
