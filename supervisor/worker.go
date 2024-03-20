package supervisor

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type StartParams struct {
	// Cmd is the path or name of the binary to execute
	Cmd string

	// Cwd is the working directory in which
	// the binary should be executed
	Cwd string

	// Args is the list of arguments to pass to the command
	Args []string

	// Env is a map of environment variables
	// to set when running the command
	Env map[string]any
}

type KillParams struct {
	// Timeout is the duration to wait for the worker to stop
	Timeout time.Duration
}

type ReadParams struct {
	// Timeout is the duration to wait for the worker to send a message
	Timeout time.Duration
}

type ExitEvent struct {
	Code   *int32
	Signal *int32
}

type Message[T any] struct {
	ID   int `json:"id"`
	Data T   `json:"data"`
}

type Worker[T any] interface {
	Start(context.Context, StartParams) (chan ExitEvent, error)
	Terminate(context.Context) error
	Kill(context.Context, KillParams) error
	Read(context.Context, ReadParams) (T, error)
	Write(context.Context, T) error
	Send(context.Context, T, ReadParams) (T, error)
}

type ProcessWorker[T any] struct {
	processLock sync.Mutex
	process     *proc[T]
	log         *zap.Logger
}

var _ = Worker[any](&ProcessWorker[any]{})

var (
	ErrKillTimeout          = fmt.Errorf("kill timeout")
	ErrInvalidTimeout       = fmt.Errorf("invalid timeout")
	ErrWorkerNotStarted     = fmt.Errorf("worker not started")
	ErrWorkerAlreadyStarted = fmt.Errorf("worker already started")
)

// Start starts the worker process.
func (w *ProcessWorker[T]) Start(ctx context.Context, params StartParams) (chan ExitEvent, error) {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	if w.process != nil {
		return nil, ErrWorkerAlreadyStarted
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
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	w.process = &proc[T]{
		pid:         cmd.Process.Pid,
		termination: make(chan struct{}),
		stdout:      stdout,
		stderr:      stderr,
		stdin:       stdin,
		log:         w.log.Named("worker_proc").With(zap.Int("pid", cmd.Process.Pid)),
	}

	exitChan := make(chan ExitEvent)

	go func() {
		// block until the process exits
		err := cmd.Wait()

		// close the termination channel,
		close(w.process.termination)

		exitChan <- getExitEvent(err)

		close(exitChan)
	}()

	return exitChan, nil
}

// Terminate sends a SIGTERM signal to the worker process to request it to stop.
// The method returns immediately, without waiting for the process to stop.
func (w *ProcessWorker[T]) Terminate(ctx context.Context) error {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	if w.process == nil {
		return ErrWorkerNotStarted
	}

	w.process.Terminate()

	return nil
}

// Kill sends a SIGKILL signal to the worker process to force-terminate it.
// The method blocks until the process is terminated or the timeout is reached.
func (w *ProcessWorker[T]) Kill(ctx context.Context, params KillParams) error {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	if params.Timeout <= 0 {
		return ErrInvalidTimeout
	}

	if w.process == nil {
		return ErrWorkerNotStarted
	}

	return w.process.Kill(ctx, params.Timeout)
}

// Read tries to read a message from the worker process. The message
// is expected to be a JSON-serializable object. The worker process is
// expected to send the message on its stdout.
func (w *ProcessWorker[T]) Read(ctx context.Context, params ReadParams) (T, error) {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	var result T

	if w.process == nil {
		return result, ErrWorkerNotStarted
	}

	msg, err := w.process.Read(ctx, params.Timeout)
	if err != nil {
		return result, err
	}

	return msg.Data, nil
}

// Write writes a message to the worker process. The message is
// expected to be a JSON-serializable object. The worker process
// is expected to read the message from stdin and process it.
func (w *ProcessWorker[T]) Write(ctx context.Context, data T) error {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	if w.process == nil {
		return ErrWorkerNotStarted
	}

	_, err := w.process.Write(ctx, data)
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
func (w *ProcessWorker[T]) Send(
	ctx context.Context,
	data T,
	params ReadParams,
) (T, error) {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	var result T

	if w.process == nil {
		return result, ErrWorkerNotStarted
	}

	msgId, err := w.process.Write(ctx, data)
	if err != nil {
		return result, err
	}

	msg, err := w.process.Read(ctx, params.Timeout)
	if err != nil {
		return result, err
	}

	if msg.ID != msgId {
		return result, fmt.Errorf("unexpected message id: expected %d, got %d", msgId, msg.ID)
	}

	return msg.Data, nil
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
