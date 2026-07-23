package qemurun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type RemoteExitError struct {
	Code   int
	Detail string
}

func (e *RemoteExitError) Error() string {
	return fmt.Sprintf("qemu evaluator exited with code %d: %s", e.Code, strings.TrimSpace(e.Detail))
}

func RunFile(
	ctx context.Context,
	conn io.ReadWriteCloser,
	start StartMessage,
	request []byte,
	maxFrameBytes int,
) (FileResultMessage, ReadyMessage, error) {
	if start.Mode != ModeFile {
		return FileResultMessage{}, ReadyMessage{}, fmt.Errorf("qemu client: mode %q is not file", start.Mode)
	}
	codec := NewCodec(maxFrameBytes)
	stopCancelWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopCancelWatch:
		}
	}()
	defer close(stopCancelWatch)

	if err := writeJSONFrame(codec, conn, FrameHello, HelloMessage{Version: ProtocolVersion}); err != nil {
		return FileResultMessage{}, ReadyMessage{}, err
	}
	readyFrame, err := readExpectedFrame(codec, conn, FrameReady)
	if err != nil {
		return FileResultMessage{}, ReadyMessage{}, err
	}
	var ready ReadyMessage
	if err := json.Unmarshal(readyFrame.Payload, &ready); err != nil {
		return FileResultMessage{}, ReadyMessage{}, fmt.Errorf("qemu client: decode ready: %w", err)
	}
	if ready.Version != ProtocolVersion {
		return FileResultMessage{}, ready, fmt.Errorf("qemu client: guest protocol version %d, want %d", ready.Version, ProtocolVersion)
	}

	if err := writeJSONFrame(codec, conn, FrameStart, start); err != nil {
		return FileResultMessage{}, ready, err
	}
	if err := codec.WriteFrame(conn, Frame{Type: FrameFileRequest, Payload: request}); err != nil {
		return FileResultMessage{}, ready, fmt.Errorf("qemu client: write file request: %w", err)
	}

	resultFrame, err := readExpectedFrame(codec, conn, FrameFileResult)
	if err != nil {
		return FileResultMessage{}, ready, err
	}
	var result FileResultMessage
	if err := json.Unmarshal(resultFrame.Payload, &result); err != nil {
		return FileResultMessage{}, ready, fmt.Errorf("qemu client: decode file result: %w", err)
	}
	exitFrame, err := readExpectedFrame(codec, conn, FrameExit)
	if err != nil {
		return result, ready, err
	}
	var exit ExitMessage
	if err := json.Unmarshal(exitFrame.Payload, &exit); err != nil {
		return result, ready, fmt.Errorf("qemu client: decode exit: %w", err)
	}
	if exit.Code != result.ExitCode {
		return result, ready, fmt.Errorf("qemu client: result exit code %d differs from exit frame %d", result.ExitCode, exit.Code)
	}
	if result.Error != "" || result.ExitCode != 0 {
		detail := result.Error
		if detail == "" {
			detail = result.Stderr
		}
		return result, ready, &RemoteExitError{Code: result.ExitCode, Detail: detail}
	}
	return result, ready, nil
}

func writeJSONFrame(codec Codec, w io.Writer, typ FrameType, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("qemu client: encode %s: %w", typ, err)
	}
	if err := codec.WriteFrame(w, Frame{Type: typ, Payload: payload}); err != nil {
		return fmt.Errorf("qemu client: write %s: %w", typ, err)
	}
	return nil
}

func readExpectedFrame(codec Codec, r io.Reader, want FrameType) (Frame, error) {
	frame, err := codec.ReadFrame(r)
	if err != nil {
		return Frame{}, fmt.Errorf("qemu client: read %s: %w", want, err)
	}
	if frame.Type != want {
		return Frame{}, fmt.Errorf("qemu client: frame is %s, want %s", frame.Type, want)
	}
	return frame, nil
}
