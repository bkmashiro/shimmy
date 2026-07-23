package qemuguest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

type ServerConfig struct {
	WorkRoot      string
	MaxFrameBytes int
}

func Serve(ctx context.Context, conn io.ReadWriteCloser, config ServerConfig) error {
	defer conn.Close()
	codec := qemurun.NewCodec(config.MaxFrameBytes)

	helloFrame, err := codec.ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("qemu guest: read hello: %w", err)
	}
	if helloFrame.Type != qemurun.FrameHello {
		return fmt.Errorf("qemu guest: first frame is %s, want hello", helloFrame.Type)
	}
	var hello qemurun.HelloMessage
	if err := json.Unmarshal(helloFrame.Payload, &hello); err != nil {
		return fmt.Errorf("qemu guest: decode hello: %w", err)
	}
	if hello.Version != qemurun.ProtocolVersion {
		return fmt.Errorf("qemu guest: unsupported protocol version %d", hello.Version)
	}
	if err := writeJSON(codec, conn, qemurun.FrameReady, qemurun.ReadyMessage{
		Version: qemurun.ProtocolVersion,
		BootID:  currentBootID(),
	}); err != nil {
		return err
	}

	startFrame, err := codec.ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("qemu guest: read start: %w", err)
	}
	if startFrame.Type != qemurun.FrameStart {
		return fmt.Errorf("qemu guest: frame is %s, want start", startFrame.Type)
	}
	var start qemurun.StartMessage
	if err := json.Unmarshal(startFrame.Payload, &start); err != nil {
		return fmt.Errorf("qemu guest: decode start: %w", err)
	}
	switch start.Mode {
	case qemurun.ModeFile:
		requestFrame, err := codec.ReadFrame(conn)
		if err != nil {
			return fmt.Errorf("qemu guest: read file request: %w", err)
		}
		if requestFrame.Type != qemurun.FrameFileRequest {
			return fmt.Errorf("qemu guest: frame is %s, want file_request", requestFrame.Type)
		}
		result, executeErr := ExecuteFile(ctx, config.WorkRoot, ProcessSpec{
			Command: start.Command,
			Args:    start.Args,
			Cwd:     start.Cwd,
			Env:     start.Env,
		}, requestFrame.Payload)
		resultMessage := qemurun.FileResultMessage{
			Response: result.Response,
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
			ExitCode: result.ExitCode,
		}
		if executeErr != nil {
			resultMessage.Error = executeErr.Error()
		}
		if err := writeJSON(codec, conn, qemurun.FrameFileResult, resultMessage); err != nil {
			return err
		}
		exitMessage := qemurun.ExitMessage{Code: result.ExitCode}
		if executeErr != nil {
			exitMessage.Error = executeErr.Error()
		}
		if err := writeJSON(codec, conn, qemurun.FrameExit, exitMessage); err != nil {
			return err
		}
		return nil
	case qemurun.ModeRPC:
		if start.Transport != "stdio" {
			return fmt.Errorf("qemu guest: unsupported RPC transport %q", start.Transport)
		}
		return serveRPCStdio(ctx, conn, codec, start)
	default:
		return fmt.Errorf("qemu guest: unsupported mode %q", start.Mode)
	}
}

func writeJSON(codec qemurun.Codec, w io.Writer, typ qemurun.FrameType, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("qemu guest: encode %s: %w", typ, err)
	}
	if err := codec.WriteFrame(w, qemurun.Frame{Type: typ, Payload: payload}); err != nil {
		return fmt.Errorf("qemu guest: write %s: %w", typ, err)
	}
	return nil
}

func currentBootID() string {
	if data, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return hex.EncodeToString(random[:])
	}
	return "unknown"
}
