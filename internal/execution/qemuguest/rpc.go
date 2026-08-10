package qemuguest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

const maxRPCStderrBytes = 64 << 10

func serveRPCStdio(
	ctx context.Context,
	connection io.ReadWriteCloser,
	codec qemurun.Codec,
	start qemurun.StartMessage,
) error {
	openFrame, err := codec.ReadFrame(connection)
	if err != nil {
		return fmt.Errorf("qemu guest: read stdio open: %w", err)
	}
	if openFrame.Type != qemurun.FrameOpen || openFrame.StreamID != qemurun.StdioStreamID {
		return fmt.Errorf("qemu guest: expected stdio open, got %s stream %d", openFrame.Type, openFrame.StreamID)
	}

	processCtx, cancelProcess := context.WithCancel(ctx)
	defer cancelProcess()
	command := exec.CommandContext(processCtx, start.Command, start.Args...)
	command.Dir = start.Cwd
	command.Env = append([]string(nil), start.Env...)
	if command.Env == nil {
		command.Env = os.Environ()
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("qemu guest: evaluator stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("qemu guest: evaluator stdout: %w", err)
	}
	stderr := &boundedBuffer{limit: maxRPCStderrBytes}
	command.Stderr = stderr

	writerCtx, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	writer := qemurun.NewSerializedFrameWriter(writerCtx, connection, codec, 8)
	if err := command.Start(); err != nil {
		return writeRPCExit(ctx, writer, -1, fmt.Sprintf("start evaluator: %v", err))
	}

	outputDone := make(chan error, 1)
	go func() {
		outputDone <- copyRPCOutput(ctx, writer, stdout)
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	frames := make(chan qemurun.Frame)
	frameErrors := make(chan error, 1)
	go func() {
		for {
			frame, readErr := codec.ReadFrame(connection)
			if readErr != nil {
				frameErrors <- readErr
				return
			}
			select {
			case frames <- frame:
			case <-processCtx.Done():
				return
			}
		}
	}()

	inputClosed := false
	for {
		select {
		case frame := <-frames:
			switch frame.Type {
			case qemurun.FrameData:
				if frame.StreamID != qemurun.StdioStreamID || inputClosed {
					cancelProcess()
					return fmt.Errorf("qemu guest: invalid stdio data for stream %d", frame.StreamID)
				}
				if _, err := stdin.Write(frame.Payload); err != nil {
					cancelProcess()
					return fmt.Errorf("qemu guest: write evaluator stdin: %w", err)
				}
			case qemurun.FrameHalfClose, qemurun.FrameClose:
				if frame.StreamID != qemurun.StdioStreamID {
					cancelProcess()
					return fmt.Errorf("qemu guest: invalid stdio close for stream %d", frame.StreamID)
				}
				if !inputClosed {
					inputClosed = true
					_ = stdin.Close()
				}
			case qemurun.FrameCancel:
				cancelProcess()
			case qemurun.FrameStreamError:
				cancelProcess()
				return fmt.Errorf("qemu guest: host stdio stream error: %s", frame.Payload)
			default:
				cancelProcess()
				return fmt.Errorf("qemu guest: unexpected stdio frame %s", frame.Type)
			}
		case waitErr := <-waitDone:
			outputErr := <-outputDone
			code, detail := rpcExitStatus(waitErr, stderr.String())
			if outputErr != nil && !errors.Is(outputErr, context.Canceled) {
				if detail != "" {
					detail += "; "
				}
				detail += "stdout bridge: " + outputErr.Error()
				if code == 0 {
					code = -1
				}
			}
			return writeRPCExit(ctx, writer, code, detail)
		case readErr := <-frameErrors:
			cancelProcess()
			_ = stdin.Close()
			<-waitDone
			<-outputDone
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("qemu guest: read stdio frame: %w", readErr)
		case <-ctx.Done():
			cancelProcess()
			_ = stdin.Close()
			<-waitDone
			<-outputDone
			return ctx.Err()
		}
	}
}

func copyRPCOutput(ctx context.Context, writer *qemurun.SerializedFrameWriter, output io.Reader) error {
	buffer := make([]byte, 32<<10)
	for {
		count, readErr := output.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			if err := writer.Write(ctx, qemurun.Frame{
				Type:     qemurun.FrameData,
				StreamID: qemurun.StdioStreamID,
				Payload:  payload,
			}); err != nil {
				return err
			}
		}
		if readErr == io.EOF || errors.Is(readErr, os.ErrClosed) {
			return writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameHalfClose, StreamID: qemurun.StdioStreamID})
		}
		if readErr != nil {
			return readErr
		}
	}
}

func writeRPCExit(
	ctx context.Context,
	writer *qemurun.SerializedFrameWriter,
	code int,
	detail string,
) error {
	payload, err := json.Marshal(qemurun.ExitMessage{Code: code, Error: detail})
	if err != nil {
		return err
	}
	return writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameExit, Payload: payload})
}

func rpcExitStatus(waitErr error, stderr string) (int, string) {
	if waitErr == nil {
		return 0, ""
	}
	code := -1
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		code = exitErr.ExitCode()
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = waitErr.Error()
	}
	return code, detail
}

type boundedBuffer struct {
	limit int
	bytes.Buffer
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	if b.limit <= 0 {
		return originalLength, nil
	}
	if len(value) >= b.limit {
		b.Buffer.Reset()
		_, _ = b.Buffer.Write(value[len(value)-b.limit:])
		return originalLength, nil
	}
	if overflow := b.Buffer.Len() + len(value) - b.limit; overflow > 0 {
		current := append([]byte(nil), b.Buffer.Bytes()[overflow:]...)
		b.Buffer.Reset()
		_, _ = b.Buffer.Write(current)
	}
	_, _ = b.Buffer.Write(value)
	return originalLength, nil
}
