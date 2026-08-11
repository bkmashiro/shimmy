package qemuguest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrMissingCommand = errors.New("qemu guest: missing evaluator command")

type ProcessSpec struct {
	Command string
	Args    []string
	Cwd     string
	Env     []string
}

type FileResult struct {
	Response []byte
	Stdout   string
	Stderr   string
	ExitCode int
}

type ProcessExitError struct {
	Code   int
	Stderr string
}

func (e *ProcessExitError) Error() string {
	return fmt.Sprintf("qemu guest: evaluator exited with code %d: %s", e.Code, strings.TrimSpace(e.Stderr))
}

func ExecuteFile(ctx context.Context, workRoot string, spec ProcessSpec, request []byte) (FileResult, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return FileResult{}, ErrMissingCommand
	}
	if workRoot == "" {
		workRoot = os.TempDir()
	}
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		return FileResult{}, fmt.Errorf("qemu guest: create work root: %w", err)
	}
	workDir, err := os.MkdirTemp(workRoot, "request-")
	if err != nil {
		return FileResult{}, fmt.Errorf("qemu guest: create request directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	requestPath := filepath.Join(workDir, "request.json")
	responsePath := filepath.Join(workDir, "response.json")
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
		return FileResult{}, fmt.Errorf("qemu guest: write request: %w", err)
	}
	if err := os.WriteFile(responsePath, nil, 0o600); err != nil {
		return FileResult{}, fmt.Errorf("qemu guest: create response: %w", err)
	}

	args := append(append([]string(nil), spec.Args...), requestPath, responsePath)
	cmd := exec.CommandContext(ctx, spec.Command, args...)
	cmd.Dir = spec.Cwd
	if spec.Env != nil {
		cmd.Env = append([]string(nil), spec.Env...)
	}
	cmd.Env = append(cmd.Env,
		"EVAL_IO=FILE",
		"EVAL_FILE_NAME_REQUEST="+requestPath,
		"EVAL_FILE_NAME_RESPONSE="+responsePath,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	result := FileResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: -1,
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return result, &ProcessExitError{Code: result.ExitCode, Stderr: result.Stderr}
		}
		return result, fmt.Errorf("qemu guest: start evaluator: %w", runErr)
	}

	response, err := os.ReadFile(responsePath)
	if err != nil {
		return result, fmt.Errorf("qemu guest: read response: %w", err)
	}
	result.Response = response
	return result, nil
}
