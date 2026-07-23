package qemurun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const StdioStreamID uint32 = 1

func RunRPCStdio(
	ctx context.Context,
	connection io.ReadWriteCloser,
	start StartMessage,
	stdin io.Reader,
	stdout io.Writer,
	maxFrameBytes int,
) error {
	if start.Mode != ModeRPC || start.Transport != "stdio" {
		return fmt.Errorf("qemu client: stdio RPC requires mode=rpc and transport=stdio")
	}
	codec := NewCodec(maxFrameBytes)
	writerCtx, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	writer := NewSerializedFrameWriter(writerCtx, connection, codec, 8)

	stopCancelWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopCancelWatch:
		}
	}()
	defer close(stopCancelWatch)

	if err := writeJSONSerialized(ctx, writer, FrameHello, 0, HelloMessage{Version: ProtocolVersion}); err != nil {
		return err
	}
	readyFrame, err := readReadyFrame(codec, connection)
	if err != nil {
		return err
	}
	var ready ReadyMessage
	if err := json.Unmarshal(readyFrame.Payload, &ready); err != nil {
		return fmt.Errorf("qemu client: decode ready: %w", err)
	}
	if ready.Version != ProtocolVersion {
		return fmt.Errorf("qemu client: guest protocol version %d, want %d", ready.Version, ProtocolVersion)
	}
	if err := clearGuestBootDeadline(connection); err != nil {
		return err
	}
	if err := writeJSONSerialized(ctx, writer, FrameStart, 0, start); err != nil {
		return err
	}
	if err := writer.Write(ctx, Frame{Type: FrameOpen, StreamID: StdioStreamID}); err != nil {
		return fmt.Errorf("qemu client: open stdio stream: %w", err)
	}

	inputDone := make(chan error, 1)
	go func() {
		inputDone <- copyInputToFrames(ctx, writer, stdin, maxFrameBytes)
	}()

	for {
		frame, err := codec.ReadFrame(connection)
		if err != nil {
			select {
			case inputErr := <-inputDone:
				if inputErr != nil {
					return inputErr
				}
			default:
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("qemu client: read RPC frame: %w", err)
		}
		switch frame.Type {
		case FrameData:
			if frame.StreamID != StdioStreamID {
				return fmt.Errorf("qemu client: data for unexpected stdio stream %d", frame.StreamID)
			}
			if _, err := stdout.Write(frame.Payload); err != nil {
				return fmt.Errorf("qemu client: write RPC stdout: %w", err)
			}
		case FrameHalfClose, FrameClose:
			if frame.StreamID != StdioStreamID {
				return fmt.Errorf("qemu client: close for unexpected stdio stream %d", frame.StreamID)
			}
		case FrameStreamError:
			return fmt.Errorf("qemu client: guest stdio stream error: %s", frame.Payload)
		case FrameExit:
			var exit ExitMessage
			if err := json.Unmarshal(frame.Payload, &exit); err != nil {
				return fmt.Errorf("qemu client: decode RPC exit: %w", err)
			}
			if exit.Code != 0 {
				return &RemoteExitError{Code: exit.Code, Detail: exit.Error}
			}
			return nil
		default:
			return fmt.Errorf("qemu client: unexpected RPC frame %s", frame.Type)
		}
	}
}

func copyInputToFrames(
	ctx context.Context,
	writer *SerializedFrameWriter,
	input io.Reader,
	maxFrameBytes int,
) error {
	chunkSize := maxFrameBytes - frameMetadataBytes
	if chunkSize > 32<<10 {
		chunkSize = 32 << 10
	}
	if chunkSize <= 0 {
		return ErrFrameTooLarge
	}
	buffer := make([]byte, chunkSize)
	for {
		count, readErr := input.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			if err := writer.Write(ctx, Frame{Type: FrameData, StreamID: StdioStreamID, Payload: payload}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return writer.Write(ctx, Frame{Type: FrameHalfClose, StreamID: StdioStreamID})
		}
		if readErr != nil {
			_ = writer.Write(ctx, Frame{Type: FrameStreamError, StreamID: StdioStreamID, Payload: []byte(readErr.Error())})
			return readErr
		}
	}
}

func writeJSONSerialized(
	ctx context.Context,
	writer *SerializedFrameWriter,
	typeValue FrameType,
	streamID uint32,
	value any,
) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("qemu client: encode %s: %w", typeValue, err)
	}
	if err := writer.Write(ctx, Frame{Type: typeValue, StreamID: streamID, Payload: payload}); err != nil {
		return fmt.Errorf("qemu client: write %s: %w", typeValue, err)
	}
	return nil
}
