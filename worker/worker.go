package worker

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

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
	ID int `json:"id"`

	// Data is the message payload
	Data T `json:"data"`
}

type Worker[I any, O any] interface {
	Start(context.Context, StartParams) error
	Terminate() error
	// Kill(StopParams) error
	Read(context.Context, ReadParams) (O, error)
	Write(context.Context, I) error
	Send(context.Context, I, SendParams) (O, error)
	Wait(context.Context) (ExitEvent, error)
	WaitFor(context.Context, time.Duration) (ExitEvent, error)
}

type ProcessWorker[I any, O any] struct {
	processLock sync.Mutex
	process     *proc[I, O]
	exitChan    chan ExitEvent
	log         *zap.Logger
}

func NewProcessWorker[I, O any](log *zap.Logger) *ProcessWorker[I, O] {
	return &ProcessWorker[I, O]{
		log: log,
	}
}

var _ = Worker[any, any](&ProcessWorker[any, any]{})

// Start starts the worker process.
func (w *ProcessWorker[I, O]) Start(ctx context.Context, params StartParams) error {
	process := w.acquireProcess()

	if process != nil {
		return ErrWorkerAlreadyStarted
	}

	cmd := exec.Command(params.Cmd, params.Args...)

	if params.Env != nil {
		env := make([]string, 0, len(params.Env))
		for k, v := range params.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	if params.Cwd != "" {
		cmd.Dir = params.Cwd
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err = cmd.Start()
	if err != nil {
		return err
	}

	process = &proc[I, O]{
		pid:         cmd.Process.Pid,
		termination: make(chan struct{}),
		stdout:      stdout,
		stderr:      stderr,
		stdin:       stdin,
		log:         w.log.Named("worker_proc").With(zap.Int("pid", cmd.Process.Pid)),
	}

	w.setProcess(process)

	w.exitChan = make(chan ExitEvent)

	go func() {
		// block until the process exits
		err := cmd.Wait()

		// close the termination channel,
		close(w.process.termination)

		w.exitChan <- getExitEvent(err)

		close(w.exitChan)
	}()

	go func() {
		// block until the context is done
		<-ctx.Done()

		// kill the process without further ado
		// TODO: check
		process.Kill(0)
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
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	return w.Wait(ctx)
}

// Terminate sends a SIGTERM signal to the worker process to request it to stop.
// The method returns immediately, without waiting for the process to stop.
func (w *ProcessWorker[I, O]) Terminate() error {
	process := w.acquireProcess()

	if process == nil {
		return ErrWorkerNotStarted
	}

	process.Terminate()

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
func (w *ProcessWorker[I, O]) Read(ctx context.Context, params ReadParams) (O, error) {
	process := w.acquireProcess()

	var result O

	if process == nil {
		return result, ErrWorkerNotStarted
	}

	msg, err := process.Read(ctx, params.Timeout)
	if err != nil {
		return result, err
	}

	return msg.Data, nil
}

// Write writes a message to the worker process. The message is
// expected to be a JSON-serializable object. The worker process
// is expected to read the message from stdin and process it.
func (w *ProcessWorker[I, O]) Write(ctx context.Context, data I) error {
	process := w.acquireProcess()

	if process == nil {
		return ErrWorkerNotStarted
	}

	_, err := process.Write(ctx, data)
	if err != nil {
		return err
	}

	return nil
}

// Send sends a message to the worker process. The message is
// written to the process's stdin. The message is expected to
// be a JSON-serializable object. The worker process is expected
// to read the message from stdin and process it. The worker
// process may send a response to the message on its stdout.
func (w *ProcessWorker[I, O]) Send(
	ctx context.Context,
	data I,
	params SendParams,
) (O, error) {
	process := w.acquireProcess()

	var result O

	if process == nil {
		return result, ErrWorkerNotStarted
	}

	msgId, err := process.Write(ctx, data)
	if err != nil {
		return result, err
	}

	msg, err := process.Read(ctx, params.Timeout)
	if err != nil {
		return result, err
	}

	if msg.ID != msgId {
		return result, fmt.Errorf("unexpected message id: expected %d, got %d", msgId, msg.ID)
	}

	return msg.Data, nil
}

// getProcess returns the worker process. The method is thread-safe.
func (w *ProcessWorker[I, O]) setProcess(p *proc[I, O]) {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	w.process = p
}

// acquireProcess returns the worker process. The method is thread-safe.
func (w *ProcessWorker[I, O]) acquireProcess() *proc[I, O] {
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
