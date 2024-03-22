package supervisor

import "errors"

var (
	ErrUnsupportedIOMode       = errors.New("unsupported io mode")
	ErrInvalidPersistentFileIO = errors.New("persistent workers are not supported for file IO yet")
)

type IOMode string

const (
	// StdIO describes communication over stdin/stdout
	StdIO IOMode = "stdio"

	// FileIO describes communication w/ processes over files
	FileIO IOMode = "file"

	// SocketIO describes communication w/ processes over sockets
	// SocketIO IOMode = "socket"
)
