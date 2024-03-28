package supervisor

import "errors"

var (
	ErrUnsupportedIOMode       = errors.New("unsupported io interface")
	ErrInvalidPersistentFileIO = errors.New("persistent workers are not supported for file IO yet")
)

type IOInterface string

const (
	// StdIO describes communication over stdin/stdout
	StdIO IOInterface = "stdio"

	// FileIO describes communication w/ processes over files
	FileIO IOInterface = "file"

	// SocketIO describes communication w/ processes over sockets
	// SocketIO IOMode = "socket"
)
