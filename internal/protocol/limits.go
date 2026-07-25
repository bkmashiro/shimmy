package protocol

import "errors"

const (
	DefaultMaxMessageBytes = 4 << 20
	MaxFrameHeaderBytes    = 64 << 10
)

var ErrMessageTooLarge = errors.New("message exceeds maximum size")
