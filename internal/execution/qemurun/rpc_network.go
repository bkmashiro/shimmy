package qemurun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

func RunRPCNetwork(
	ctx context.Context,
	guest io.ReadWriteCloser,
	start StartMessage,
	listener net.Listener,
	maxFrameBytes int,
	maxStreams int,
) error {
	if start.Mode != ModeRPC || start.Transport == "stdio" {
		return fmt.Errorf("qemu client: network RPC requires non-stdio RPC start")
	}
	if listener == nil {
		return errors.New("qemu client: network RPC listener is nil")
	}
	if maxStreams <= 0 {
		return errors.New("qemu client: max streams must be positive")
	}
	defer listener.Close()
	bridgeCtx, cancelBridge := context.WithCancel(ctx)
	defer cancelBridge()
	codec := NewCodec(maxFrameBytes)
	writer := NewSerializedFrameWriter(bridgeCtx, guest, codec, maxStreams+1)
	if err := beginRPCSession(ctx, codec, writer, guest, start); err != nil {
		return err
	}

	accepted := make(chan net.Conn)
	acceptErrors := make(chan error, 1)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				acceptErrors <- err
				return
			}
			select {
			case accepted <- connection:
			case <-bridgeCtx.Done():
				_ = connection.Close()
				return
			}
		}
	}()
	frames := make(chan Frame)
	frameErrors := make(chan error, 1)
	go func() {
		for {
			frame, err := codec.ReadFrame(guest)
			if err != nil {
				frameErrors <- err
				return
			}
			select {
			case frames <- frame:
			case <-bridgeCtx.Done():
				return
			}
		}
	}()
	streamErrors := make(chan streamBridgeError, maxStreams)
	streams := make(map[uint32]net.Conn)
	defer func() {
		for _, stream := range streams {
			_ = stream.Close()
		}
	}()
	nextStreamID := uint32(1)

	for {
		select {
		case connection := <-accepted:
			if len(streams) >= maxStreams {
				_ = connection.Close()
				continue
			}
			streamID := nextStreamID
			nextStreamID++
			if nextStreamID == 0 {
				nextStreamID = 1
			}
			streams[streamID] = connection
			if err := writer.Write(ctx, Frame{Type: FrameOpen, StreamID: streamID}); err != nil {
				_ = connection.Close()
				delete(streams, streamID)
				return fmt.Errorf("qemu client: open network stream: %w", err)
			}
			go pumpHostStream(bridgeCtx, writer, streamID, connection, maxFrameBytes, streamErrors)
		case frame := <-frames:
			if frame.Type == FrameExit {
				var exit ExitMessage
				if err := json.Unmarshal(frame.Payload, &exit); err != nil {
					return fmt.Errorf("qemu client: decode network exit: %w", err)
				}
				if exit.Code != 0 {
					return &RemoteExitError{Code: exit.Code, Detail: exit.Error}
				}
				return nil
			}
			stream, ok := streams[frame.StreamID]
			if !ok {
				return fmt.Errorf("qemu client: frame %s for unknown stream %d", frame.Type, frame.StreamID)
			}
			switch frame.Type {
			case FrameData:
				if err := writeAll(stream, frame.Payload); err != nil {
					_ = stream.Close()
					delete(streams, frame.StreamID)
					_ = writer.Write(ctx, Frame{Type: FrameStreamError, StreamID: frame.StreamID, Payload: []byte(err.Error())})
				}
			case FrameHalfClose:
				_ = closeNetworkWrite(stream)
			case FrameClose, FrameStreamError:
				_ = stream.Close()
				delete(streams, frame.StreamID)
			default:
				return fmt.Errorf("qemu client: unexpected network frame %s", frame.Type)
			}
		case streamErr := <-streamErrors:
			stream, ok := streams[streamErr.streamID]
			if ok && streamErr.err != nil && !errors.Is(streamErr.err, net.ErrClosed) {
				_ = writer.Write(ctx, Frame{Type: FrameStreamError, StreamID: streamErr.streamID, Payload: []byte(streamErr.err.Error())})
				_ = stream.Close()
				delete(streams, streamErr.streamID)
			}
		case err := <-frameErrors:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("qemu client: read network frame: %w", err)
		case err := <-acceptErrors:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("qemu client: accept RPC connection: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func beginRPCSession(
	ctx context.Context,
	codec Codec,
	writer *SerializedFrameWriter,
	guest io.Reader,
	start StartMessage,
) error {
	if err := writeJSONSerialized(ctx, writer, FrameHello, 0, HelloMessage{Version: ProtocolVersion}); err != nil {
		return err
	}
	readyFrame, err := readReadyFrame(codec, guest)
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
	if err := clearGuestBootDeadline(guest); err != nil {
		return err
	}
	return writeJSONSerialized(ctx, writer, FrameStart, 0, start)
}

type streamBridgeError struct {
	streamID uint32
	err      error
}

func pumpHostStream(
	ctx context.Context,
	writer *SerializedFrameWriter,
	streamID uint32,
	connection net.Conn,
	maxFrameBytes int,
	errorsOut chan<- streamBridgeError,
) {
	chunkSize := maxFrameBytes - frameMetadataBytes
	if chunkSize > 32<<10 {
		chunkSize = 32 << 10
	}
	if chunkSize <= 0 {
		errorsOut <- streamBridgeError{streamID: streamID, err: ErrFrameTooLarge}
		return
	}
	buffer := make([]byte, chunkSize)
	for {
		count, err := connection.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			if writeErr := writer.Write(ctx, Frame{Type: FrameData, StreamID: streamID, Payload: payload}); writeErr != nil {
				errorsOut <- streamBridgeError{streamID: streamID, err: writeErr}
				return
			}
		}
		if err == io.EOF {
			writeErr := writer.Write(ctx, Frame{Type: FrameHalfClose, StreamID: streamID})
			errorsOut <- streamBridgeError{streamID: streamID, err: writeErr}
			return
		}
		if err != nil {
			errorsOut <- streamBridgeError{streamID: streamID, err: err}
			return
		}
	}
}

func closeNetworkWrite(connection net.Conn) error {
	type closeWriter interface{ CloseWrite() error }
	if writer, ok := connection.(closeWriter); ok {
		return writer.CloseWrite()
	}
	return connection.Close()
}
