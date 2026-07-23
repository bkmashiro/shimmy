package qemuguest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

const defaultMaxNetworkStreams = 64

func serveRPCNetwork(
	ctx context.Context,
	connection io.ReadWriteCloser,
	codec qemurun.Codec,
	start qemurun.StartMessage,
	workRoot string,
) error {
	if start.GuestEndpoint == "" {
		endpoint, err := qemurun.PrepareGuestEndpoint(start.Transport, start.Endpoint, workRoot)
		if err != nil {
			return err
		}
		start.GuestEndpoint = endpoint
	}
	processCtx, cancelProcess := context.WithCancel(ctx)
	defer cancelProcess()
	command := exec.CommandContext(processCtx, start.Command, start.Args...)
	command.Dir = start.Cwd
	command.Env = rpcNetworkEnvironment(start)
	command.Stdout = io.Discard
	stderr := &boundedBuffer{limit: maxRPCStderrBytes}
	command.Stderr = stderr

	writerCtx, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	writer := qemurun.NewSerializedFrameWriter(writerCtx, connection, codec, defaultMaxNetworkStreams+1)
	if err := command.Start(); err != nil {
		return writeRPCExit(ctx, writer, -1, fmt.Sprintf("start evaluator: %v", err))
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	frames := make(chan qemurun.Frame)
	frameErrors := make(chan error, 1)
	go func() {
		for {
			frame, err := codec.ReadFrame(connection)
			if err != nil {
				frameErrors <- err
				return
			}
			select {
			case frames <- frame:
			case <-processCtx.Done():
				return
			}
		}
	}()

	streams := make(map[uint32]net.Conn)
	var streamsMu sync.Mutex
	var pumps sync.WaitGroup
	closeStreams := func() {
		streamsMu.Lock()
		defer streamsMu.Unlock()
		for id, stream := range streams {
			_ = stream.Close()
			delete(streams, id)
		}
	}
	defer closeStreams()

	for {
		select {
		case frame := <-frames:
			switch frame.Type {
			case qemurun.FrameOpen:
				streamsMu.Lock()
				_, exists := streams[frame.StreamID]
				streamCount := len(streams)
				streamsMu.Unlock()
				if frame.StreamID == 0 || exists || streamCount >= defaultMaxNetworkStreams {
					return fmt.Errorf("qemu guest: invalid network stream open %d", frame.StreamID)
				}
				stream, err := dialGuestEvaluator(processCtx, start.Transport, start.GuestEndpoint)
				if err != nil {
					_ = writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameStreamError, StreamID: frame.StreamID, Payload: []byte(err.Error())})
					continue
				}
				streamsMu.Lock()
				streams[frame.StreamID] = stream
				streamsMu.Unlock()
				pumps.Add(1)
				go func(streamID uint32, stream net.Conn) {
					defer pumps.Done()
					pumpGuestStream(processCtx, writer, streamID, stream)
				}(frame.StreamID, stream)
			case qemurun.FrameData:
				stream := lookupGuestStream(&streamsMu, streams, frame.StreamID)
				if stream == nil {
					return fmt.Errorf("qemu guest: data for unknown stream %d", frame.StreamID)
				}
				if err := writeAllTo(stream, frame.Payload); err != nil {
					_ = writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameStreamError, StreamID: frame.StreamID, Payload: []byte(err.Error())})
					_ = stream.Close()
					removeGuestStream(&streamsMu, streams, frame.StreamID)
				}
			case qemurun.FrameHalfClose:
				stream := lookupGuestStream(&streamsMu, streams, frame.StreamID)
				if stream == nil {
					return fmt.Errorf("qemu guest: half-close for unknown stream %d", frame.StreamID)
				}
				_ = closeGuestWrite(stream)
			case qemurun.FrameClose, qemurun.FrameStreamError:
				stream := lookupGuestStream(&streamsMu, streams, frame.StreamID)
				if stream != nil {
					_ = stream.Close()
					removeGuestStream(&streamsMu, streams, frame.StreamID)
				}
			case qemurun.FrameCancel:
				cancelProcess()
			default:
				return fmt.Errorf("qemu guest: unexpected network frame %s", frame.Type)
			}
		case waitErr := <-waitDone:
			pumpsDone := make(chan struct{})
			go func() {
				pumps.Wait()
				close(pumpsDone)
			}()
			select {
			case <-pumpsDone:
			case <-time.After(time.Second):
				closeStreams()
				<-pumpsDone
			}
			code, detail := rpcExitStatus(waitErr, stderr.String())
			return writeRPCExit(ctx, writer, code, detail)
		case err := <-frameErrors:
			cancelProcess()
			closeStreams()
			<-waitDone
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("qemu guest: read network frame: %w", err)
		case <-ctx.Done():
			cancelProcess()
			closeStreams()
			<-waitDone
			return ctx.Err()
		}
	}
}

func rpcNetworkEnvironment(start qemurun.StartMessage) []string {
	environment := append([]string(nil), start.Env...)
	if environment == nil {
		environment = os.Environ()
	}
	environment = append(environment, "EVAL_IO=rpc", "EVAL_RPC_TRANSPORT="+start.Transport)
	switch start.Transport {
	case "ipc":
		environment = append(environment, "EVAL_RPC_IPC_ENDPOINT="+start.GuestEndpoint)
	case "tcp":
		environment = append(environment, "EVAL_RPC_TCP_ADDRESS="+start.GuestEndpoint)
	case "http":
		environment = append(environment, "EVAL_RPC_HTTP_URL="+start.GuestEndpoint)
	case "ws":
		environment = append(environment, "EVAL_RPC_WS_URL="+start.GuestEndpoint)
	}
	return environment
}

func dialGuestEvaluator(ctx context.Context, transport, endpoint string) (net.Conn, error) {
	network, address, err := qemurun.NetworkListenAddress(transport, endpoint)
	if err != nil {
		return nil, err
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		connection, err := new(net.Dialer).DialContext(ctx, network, address)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("qemu guest: dial evaluator %s: %w", address, lastErr)
		case <-ticker.C:
		}
	}
}

func pumpGuestStream(
	ctx context.Context,
	writer *qemurun.SerializedFrameWriter,
	streamID uint32,
	stream net.Conn,
) {
	buffer := make([]byte, 32<<10)
	for {
		count, err := stream.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			if writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameData, StreamID: streamID, Payload: payload}) != nil {
				return
			}
		}
		if err == io.EOF {
			_ = writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameHalfClose, StreamID: streamID})
			_ = writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameClose, StreamID: streamID})
			return
		}
		if err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) {
				_ = writer.Write(ctx, qemurun.Frame{Type: qemurun.FrameStreamError, StreamID: streamID, Payload: []byte(err.Error())})
			}
			return
		}
	}
}

func lookupGuestStream(mu *sync.Mutex, streams map[uint32]net.Conn, id uint32) net.Conn {
	mu.Lock()
	defer mu.Unlock()
	return streams[id]
}

func removeGuestStream(mu *sync.Mutex, streams map[uint32]net.Conn, id uint32) {
	mu.Lock()
	defer mu.Unlock()
	delete(streams, id)
}

func closeGuestWrite(connection net.Conn) error {
	type closeWriter interface{ CloseWrite() error }
	if writer, ok := connection.(closeWriter); ok {
		return writer.CloseWrite()
	}
	return connection.Close()
}

func writeAllTo(output io.Writer, payload []byte) error {
	for len(payload) > 0 {
		count, err := output.Write(payload)
		if count > 0 {
			payload = payload[count:]
		}
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
