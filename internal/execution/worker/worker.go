package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/mitchellh/mapstructure"
	"go.uber.org/zap"
)

type ExitEvent struct {
	// Code is the exit code of the process
	Code *int32

	// Signal is the signal that caused the process to exit
	Signal *int32
}

type Message[T any] struct {
	// ID is the message identifier
	ID int `mapstructure:"id,omitempty"`

	// Data is the message payload
	Data T `mapstructure:",squash"`
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

	msgid     int
	msgidLock sync.Mutex

	log *zap.Logger
}

func NewProcessWorker[I, O any](log *zap.Logger) *ProcessWorker[I, O] {

	return &ProcessWorker[I, O]{
		log: log.Named("worker"),
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

	process := w.acquireProcess()

	if process != nil {
		return ErrWorkerAlreadyStarted
	}

	process, err := startProc(config, w.log)
	if err != nil {
		return err
	}

	w.setProcess(process)

	w.exitChan = make(chan ExitEvent)

	go func() {
		err := <-process.termination

		w.exitChan <- getExitEvent(err)

		close(w.exitChan)
	}()

	go func() {
		// block until the context is done
		<-ctx.Done()

		// kill the process without further ado
		// TODO: check
		process.Kill(-1)
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
// status of the process. If the process is already terminated, the method returns
// immediately.
func (w *ProcessWorker[I, O]) WaitFor(
	ctx context.Context,
	deadline time.Duration,
) (ExitEvent, error) {
	var waitCtx context.Context
	var cancel context.CancelFunc

	if deadline <= 0 {
		waitCtx, cancel = context.WithCancel(ctx)
		defer cancel()
	} else {
		waitCtx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}

	return w.Wait(waitCtx)
}

// Terminate sends a SIGTERM signal to the worker process to request it to stop.
// The method returns immediately, without waiting for the process to stop.
func (w *ProcessWorker[I, O]) Terminate() error {
	process := w.acquireProcess()

	if process == nil {
		return ErrWorkerNotStarted
	}

	err := process.Terminate(-1)
	if err != nil {
		return err
	}

	return nil
}

// Kill sends a SIGKILL signal to the worker process to force-terminate it.
// The method blocks until the process is terminated or the timeout is reached.
// TODO: see what to do with this method
// func (w *ProcessWorker[T]) Kill(params KillParams) error {
// 	process := w.acquireProcess()

// 	if params.Timeout <= 0 {
// 		return ErrInvalidTimeout
// 	}

// 	if process == nil {
// 		return ErrWorkerNotStarted
// 	}

// 	return process.Kill(params.Timeout)
// }

// Read tries to read a message from the worker process. The message
// is expected to be a JSON-serializable object. The worker process is
// expected to send the message on its stdout.
func (w *ProcessWorker[I, O]) Read(ctx context.Context, params ReadConfig) (O, error) {
	process := w.acquireProcess()

	var result O

	if process == nil {
		return result, ErrWorkerNotStarted
	}

	msg, err := w.readJsonStdout(ctx, process, params.Timeout)
	if err != nil {
		return result, err
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
	done := make(chan error, 1)

	// Start a goroutine to read from stdout and decode the JSON. We're using
	// a goroutine to support timeouts and context cancellation, as the json
	// decoder doesn't support these.
	go func() {
		var resMap map[string]any

		// first, decode the json to a generic map
		if err := json.NewDecoder(process.StdoutPipe()).Decode(&resMap); err != nil {
			done <- err
			return
		}

		// then, decode the map to the Message struct. We're using mapstructure
		// for decoding as json.Unmarshal doesn't support squashing.
		if err := mapstructure.Decode(resMap, &result); err != nil {
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

	var reqMap map[string]any

	// decode message to map
	if err := mapstructure.Decode(req, &reqMap); err != nil {
		return 0, err
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

// getProcess returns the worker process. The method is thread-safe.
func (w *ProcessWorker[I, O]) setProcess(p *proc) {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	w.process = p
}

// acquireProcess returns the worker process. The method is thread-safe.
func (w *ProcessWorker[I, O]) acquireProcess() *proc {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	return w.process
}

func getExitEvent(err error) ExitEvent {
	var cell int32
	var exitStatus *int32
	var signo *int32

	if err == nil {
		exitStatus = &cell
	} else if exitError, ok := err.(*exec.ExitError); ok {
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
			if code := status.ExitStatus(); code >= 0 {
				cell = int32(code)
				exitStatus = &cell
			} else {
				cell = int32(status.Signal())
				signo = &cell
			}
		}
	}

	if signo == nil && exitStatus == nil {
		cell = 1
		exitStatus = &cell
	}

	return ExitEvent{
		Code:   exitStatus,
		Signal: signo,
	}
}

func (w *ProcessWorker[I, O]) nextMsgID() int {
	w.msgidLock.Lock()
	defer w.msgidLock.Unlock()

	id := w.msgid
	w.msgid++

	return id
}
