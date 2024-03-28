package shell

import (
	"errors"
	"fmt"
)

type ExitError struct {
	ExitCode int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("shell exited with %d", e.ExitCode)
}

func NewExitError(exitCode int) *ExitError {
	return &ExitError{ExitCode: exitCode}
}

func IsExitError(err error) bool {
	if err == nil {
		return false
	}

	var exitErr *ExitError
	return errors.As(err, &exitErr)
}
