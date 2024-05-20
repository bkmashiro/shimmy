package worker

import (
	"bytes"
	"context"
	"errors"
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

type Worker interface {
	io.ReadWriteCloser

	Start(context.Context, StartConfig) error
	Terminate() error
	Wait(context.Context) (ExitEvent, error)
	WaitFor(context.Context, time.Duration) (ExitEvent, error)
}

type ProcessWorker struct {
	processLock sync.Mutex
	process     *proc

	wait chan struct{}
	done chan struct{}
	exit chan ExitEvent

	stderr   bytes.Buffer
	stderrWg sync.WaitGroup

	log *zap.Logger
}

func NewProcessWorker(log *zap.Logger) *ProcessWorker {
	return &ProcessWorker{
		wait: make(chan struct{}),
		done: make(chan struct{}),
		exit: make(chan ExitEvent),
		log:  log.Named("worker"),
	}
}

var _ Worker = (*ProcessWorker)(nil)

// Start starts the worker process.
func (w *ProcessWorker) Start(ctx context.Context, config StartConfig) error {
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
	process, err := startProc(ctx, config, w.log)
	if err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// set the process
	w.process = process

	// wait for the process to terminate,
	// and send the exit event to the channel
	go func() {
		// first, wait for `Wait` to be called
		<-w.wait

		// wait for stderr to be read
		w.stderrWg.Wait()

		// block until the process exits
		err = process.Wait()

		// get the exit event
		evt := getExitEvent(err, w.stderr.String())

		// log the exit event
		w.log.With(
			zap.Any("code", evt.Code),
			zap.Any("signal", evt.Signal),
			zap.String("stderr", evt.Stderr),
		).Debug("process exited")

		// send the exit event to the channel
		w.exit <- evt

		// close the exit channel
		close(w.exit)

		// close the done channel
		close(w.done)
	}()

	// read from stderr in a separate goroutine
	w.stderrWg.Add(1)
	go func() {
		defer w.stderrWg.Done()

		// read from stderr and save it for later use
		_, err := io.Copy(&w.stderr, process.StderrPipe())
		if err != nil && err != io.EOF {
			w.log.Warn("failed to read from stderr", zap.Error(err))
		}
	}()

	return nil
}

// Wait waits for the worker process to exit. The method blocks until the process
// exits. The method returns an ExitEvent object that contains the exit status of
// the process. If the process is already terminated, the method returns immediately.
//
// Any of `Wait` or `WaitFor` are intended to be called only once. Subsequent calls
// will return an error.
func (w *ProcessWorker) Wait(ctx context.Context) (ExitEvent, error) {
	// close the wait channel to signal that `Wait` has been called
	select {
	case <-w.wait:
		return ExitEvent{}, errors.New("wait has already been called")
	default:
		close(w.wait)
	}

	select {
	case <-ctx.Done():
		return ExitEvent{}, ctx.Err()
	case exitEvent := <-w.exit:
		return exitEvent, nil
	}
}

// WaitFor waits for the worker process to exit. It blocks until the process exits
// or the timeout is reached. The method returns an ExitEvent that contains the exit
// status. If the process is already terminated, the method returns immediately.
//
// Any of `Wait` or `WaitFor` are intended to be called only once. Subsequent calls
// will return an error.
func (w *ProcessWorker) WaitFor(
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
func (w *ProcessWorker) Kill() error {
	if process := w.acquireProcess(); process != nil {
		return process.Kill()
	}

	return ErrWorkerNotStarted
}

// Terminate sends a SIGTERM signal to the worker process to request it to stop.
// The method returns immediately, without waiting for the process to stop.
func (w *ProcessWorker) Terminate() error {
	if process := w.acquireProcess(); process != nil {
		return process.Terminate()
	}

	return ErrWorkerNotStarted
}

func (w *ProcessWorker) Read(p []byte) (int, error) {
	if process := w.acquireProcess(); process != nil {
		return process.StdoutPipe().Read(p)
	}

	return 0, ErrWorkerNotStarted
}

func (w *ProcessWorker) Write(p []byte) (int, error) {
	if process := w.acquireProcess(); process != nil {
		return process.StdinPipe().Write(p)
	}

	return 0, ErrWorkerNotStarted
}

func (w *ProcessWorker) Close() error {
	if process := w.acquireProcess(); process != nil {
		return process.Close()
	}

	return ErrWorkerNotStarted
}

func (w *ProcessWorker) Pid() int {
	if process := w.acquireProcess(); process != nil {
		return process.Pid()
	}

	return 0
}

// acquireProcess returns the worker process. The method is thread-safe.
func (w *ProcessWorker) acquireProcess() *proc {
	w.processLock.Lock()
	defer w.processLock.Unlock()

	return w.process
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
