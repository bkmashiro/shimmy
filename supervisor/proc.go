package supervisor

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type proc[T any] struct {
	pid         int
	termination chan struct{}
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	stdin       io.WriteCloser

	msgid     int
	msgidLock sync.Mutex

	log *zap.Logger
}

func (p *proc[T]) Terminate() {
	p.kill(syscall.SIGTERM)
}

func (p *proc[T]) Kill(
	ctx context.Context,
	timeout time.Duration,
) error {
	// kill should report success if the process terminated by the time
	// supervisor receives the request.
	select {
	case <-p.termination:
		p.log.Debug("process already terminated")
		return nil
	default:
		// continue
	}

	// kill the process
	p.kill(syscall.SIGKILL)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// block until either:
	//  * the main process exits (parent ctx is cancelled)
	//  * the child process exits (w.termination is closed)
	//  * the timeout is reached
	select {
	case <-p.termination:
		return nil
	case <-ctx.Done():
		return ErrKillTimeout
	}
}

func (p *proc[T]) kill(signal syscall.Signal) {
	log := p.log.With(zap.Stringer("signal", signal))

	// close stdin before killing the process, to
	// avoid the process hanging on input
	if err := p.stdin.Close(); err != nil {
		log.Error("close stdin failed", zap.Error(err))
	}

	log.Info("sending signal")

	// best effort, ignore errors
	if err := p.sendKillSignal(signal); err != nil {
		log.Error("stop failed", zap.Error(err))
	}
}

func (p *proc[T]) sendKillSignal(signal syscall.Signal) error {
	if pgid, err := syscall.Getpgid(p.pid); err == nil {
		// Negative pid sends signal to all in process group
		return syscall.Kill(-pgid, signal)
	} else {
		return syscall.Kill(p.pid, signal)
	}
}

func (p *proc[T]) Write(ctx context.Context, data T) (int, error) {
	reqID := p.nextMsgID()

	req := Message[T]{
		ID:   reqID,
		Data: data,
	}

	// write encoded message to process stdin
	if err := json.NewEncoder(p.stdin).Encode(req); err != nil {
		return 0, err
	}

	return reqID, nil
}

func (p *proc[T]) nextMsgID() int {
	p.msgidLock.Lock()
	defer p.msgidLock.Unlock()

	id := p.msgid
	p.msgid++

	return id
}

func (p *proc[T]) Read(ctx context.Context, timeout time.Duration) (Message[T], error) {
	var result Message[T]

	// Create a channel to signal the completion of reading and decoding.
	done := make(chan error, 1)

	// Start a goroutine to read from stdout and decode the JSON.
	go func() {
		if err := json.NewDecoder(p.stdout).Decode(&result); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	// Create a context with the specified timeout.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-ctx.Done(): // Context was cancelled or timed out
		return result, ctx.Err()
	case err := <-done: // Finished reading and decoding
		return result, err
	}
}
