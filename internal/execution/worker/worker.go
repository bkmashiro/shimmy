package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type ExitEvent struct {
	// Code is the exit code of the process
	Code *int

	// Signal is the signal that caused the process to exit
	Signal *int

	// Stderr is the stderr output of the process
	Stderr string
}

type Message[T any] struct {
	// ID is the message identifier
	ID int `json:"id"`

	// Data is the message payload
	Data T `json:"data"`
}

type Worker[I, O any] interface {
	Start(context.Context, StartConfig) error
	Terminate() error
	// Kill(StopParams) error
	Read(context.Context, ReadConfig) (O, error)
	Write(context.Context, I) error
	Send(context.Context, I, SendConfig) (O, error)
	Wait(context.Context) (ExitEvent, error)
	WaitFor(context.Context, time.Duration) (ExitEvent, error)
}

type ProcessWorker[I, O any] struct {
	processLock sync.Mutex
	process     *proc
	exitChan    chan ExitEvent

	stderr   bytes.Buffer
	stderrWg sync.WaitGroup

	msgid     int
	msgidLock sync.Mutex

	log *zap.Logger
}

func NewProcessWorker[I, O any](log *zap.Logger) *ProcessWorker[I, O] {
	return &ProcessWorker[I, O]{
		exitChan: make(chan ExitEvent, 1),
		log:      log.Named("worker"),
	}
}

var _ Worker[any, any] = (*ProcessWorker[any, any])(nil)

// Start starts the worker process.
func (w *ProcessWorker[I, O]) Start(ctx context.Context, config StartConfig) error {
	w.log.With(
		zap.String("command", config.Cmd),
		zap.Strings("args", config.Args),
		zap.String("cwd", config.Cwd),
		zap.Any("env", config.Env),
	).Debug("starting worker process")

	// synchronize access to the process
	w.processLock.Lock()
	defer w.processLock.Unlock()

	// return if the worker is already started
	if w.process != nil {
		return ErrWorkerAlreadyStarted
	}

	// exit early if the context is already cancelled
	if ctx.Err() != nil {
		return fmt.Errorf("failed to start process: %w", ctx.Err())
	}

	// start the process
	process, err := startProc(config, w.log)
	if err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// set the process
	w.process = process

	// wait for the process to terminate,
	// and send the exit event to the channel
	go func() {
		// block until the process exits
		err := process.Wait()

		// wait for stderr to be read
		w.stderrWg.Wait()

		// send the exit event to the channel
		w.exitChan <- getExitEvent(err, w.stderr.String())

		// close the exit channel
		close(w.exitChan)
	}()

	// wait for the context to be cancelled,
	// and terminate the process.
	go func() {
		select {
		case <-process.Done():
			// the process has terminated, do nothing
		case <-ctx.Done():
			// kill the process without further ado
			process.Kill(-1)
		}
	}()

	// read from stderr in a separate goroutine
	w.stderrWg.Add(1)
	go func() {
		defer w.stderrWg.Done()

		// read from stderr and save it for later use
		_, err := io.Copy(&w.stderr, process.StderrPipe())
		if err != nil && err != io.EOF {
			w.log.Error("failed to read from stderr", zap.Error(err))
		}
	}()

	return nil
}

// Wait waits for the worker process to exit. The method blocks until the process
// exits. The method returns an ExitEvent object that contains the exit status of
// the process. If the process is already terminated, the method returns immediately.
func (w *ProcessWorker[I, O]) Wait(ctx context.Context) (ExitEvent, error) {
	select {
	case <-ctx.Done():
		return ExitEvent{}, ctx.Err()
	case exitEvent := <-w.exitChan:
		return exitEvent, nil
	}
}

// WaitFor waits for the worker process to exit. It blocks until the process exits
// or the timeout is reached. The method returns an ExitEvent that contains the exit
// status. If the process is already terminated, the method returns immediately.
func (w *ProcessWorker[I, O]) WaitFor(
	ctx context.Context,
	deadline time.Duration,
) (ExitEvent, error) {
	var waitCtx context.Context
	var cancel context.CancelFunc

	if deadline <= 0 {
		waitCtx, cancel = context.WithCancel(ctx)
	} else {
		waitCtx, cancel = context.WithTimeout(ctx, deadline)
	}

	defer cancel()

	return w.Wait(waitCtx)
}

// Terminate sends a SIGKILL signal to the worker process to request it to stop.
// The method returns immediately, without waiting for the process to stop.
func (w *ProcessWorker[I, O]) Kill() error {
	if process := w.acquireProcess(); process != nil {
		return process.Kill(-1)
	}

	return ErrWorkerNotStarted
}

// Terminate sends a SIGTERM signal to the worker process to request it to stop.
// The method returns immediately, without waiting for the process to stop.
func (w *ProcessWorker[I, O]) Terminate() error {
	if process := w.acquireProcess(); process != nil {
		return process.Terminate(-1)
	}

	return ErrWorkerNotStarted
}

// Read tries to read a message from the worker process. The message
// is expected to be a JSON-serializable object. The worker process is
// expected to send the message on its stdout.
func (w *ProcessWorker[I, O]) Read(ctx context.Context, params ReadConfig) (O, error) {
	var res O

	process := w.acquireProcess()
	if process == nil {
		return res, ErrWorkerNotStarted
	}

	msg, err := w.readJsonStdout(ctx, process, params.Timeout)
	if err != nil {
		return res, err
	}

	return msg.Data, nil
}

func (w *ProcessWorker[I, O]) readJsonStdout(
	ctx context.Context,
	process *proc,
	timeout time.Duration,
) (Message[O], error) {
	var result Message[O]

	// Create a channel to signal the completion of reading and decoding.
	done := make(chan error)

	// Start a goroutine to read from stdout and decode the JSON. We're using
	// a goroutine to support timeouts and context cancellation, as the json
	// decoder doesn't support these.
	go func() {
		// first, decode the json to a generic map
		if err := json.NewDecoder(process.StdoutPipe()).Decode(&result); err != nil {
			done <- err
			return
		}

		done <- nil
	}()

	if timeout > 0 {
		// Create a context with the specified timeout.
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		ctx = timeoutCtx
	}

	select {
	case <-ctx.Done(): // Context was cancelled or timed out
		return result, ctx.Err()
	case err := <-done: // Finished reading and decoding
		return result, err
	}
}

// Write writes a message to the worker process. The message is
// expected to be a JSON-serializable object. The worker process
// is expected to read the message from stdin and process it.
func (w *ProcessWorker[I, O]) Write(ctx context.Context, data I) error {
	process := w.acquireProcess()
	if process == nil {
		return ErrWorkerNotStarted
	}

	_, err := w.writeJsonStdin(ctx, process, data)
	if err != nil {
		return err
	}

	return nil
}

func (w *ProcessWorker[I, O]) writeJsonStdin(
	ctx context.Context,
	process *proc,
	data I,
) (int, error) {
	reqID := w.nextMsgID()

	req := Message[I]{
		ID:   reqID,
		Data: data,
	}

	// write encoded message to process stdin
	if err := json.NewEncoder(process.StdinPipe()).Encode(req); err != nil {
		return 0, err
	}

	return reqID, nil
}

// Send sends a message to the worker process. The message is
// written to the process's stdin. The message is expected to
// be a JSON-serializable object. The worker process is expected
// to read the message from stdin and process it. The worker
// process may send a response to the message on its stdout.
func (w *ProcessWorker[I, O]) Send(
	ctx context.Context,
	data I,
	params SendConfig,
) (O, error) {
	process := w.acquireProcess()

	var result O

	if process == nil {
		return result, ErrWorkerNotStarted
	}

	msgId, err := w.writeJsonStdin(ctx, process, data)
	if err != nil {
		return result, err
	}

	if params.CloseAfterSend {
		if err := process.Close(); err != nil {
			return result, err
		}
	}

	msg, err := w.readJsonStdout(ctx, process, params.Timeout)
	if err != nil {
		return result, err
	}

	if msg.ID != msgId {
		return result, fmt.Errorf("unexpected message id: expected %d, got %d", msgId, msg.ID)
	}

	return msg.Data, nil
}

func (w *ProcessWorker[I, O]) Pid() int {
	if process := w.acquireProcess(); process != nil {
		return process.pid
	}

	return 0
}

// acquireProcess returns the worker process. The method is thread-safe.
func (w *ProcessWorker[I, O]) acquireProcess() *proc {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	return w.process
}

// nextMsgID returns the next message identifier. The method is thread-safe.
func (w *ProcessWorker[I, O]) nextMsgID() int {
	w.msgidLock.Lock()
	defer w.msgidLock.Unlock()

	id := w.msgid
	w.msgid++

	return id
}

// MARK: - Helpers

func getExitEvent(err error, stderr string) ExitEvent {
	var cell int
	var exitStatus *int
	var signo *int

	if err == nil {
		// the process exited successfully, set the exit code to 0
		exitStatus = &cell
	} else if exitError, ok := err.(*exec.ExitError); ok {
		// the process exited with an error
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
			if code := status.ExitStatus(); code >= 0 {
				// the process exited with an exit code
				cell = code
				exitStatus = &cell
			} else {
				// the process was terminated by a signal
				cell = int(status.Signal())
				signo = &cell
			}
		}
	}

	if signo == nil && exitStatus == nil {
		// could not determine the exit status or signal,
		// set exit status to 1
		cell = 1
		exitStatus = &cell
	}

	return ExitEvent{
		Code:   exitStatus,
		Signal: signo,
		Stderr: stderr,
	}
}
