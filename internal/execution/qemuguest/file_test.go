package qemuguest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

const helperModeEnv = "SHIMMY_QEMU_GUEST_HELPER"

func TestExecuteFilePreservesPayloadArgsCwdAndEnvironment(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	request := []byte(`{"command":"eval","params":{"answer":"42"}}`)
	spec := ProcessSpec{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestGuestHelperProcess", "--", "copy", "arg with spaces"},
		Cwd:     cwd,
		Env: append(os.Environ(),
			helperModeEnv+"=1",
			"EXPECTED_ARG=arg with spaces",
			"EXPECTED_CWD="+cwd,
			"CUSTOM_VALUE=visible",
			"EVAL_FILE_NAME_REQUEST=/host/request",
			"EVAL_FILE_NAME_RESPONSE=/host/response",
		),
	}

	result, err := ExecuteFile(context.Background(), root, spec, request)
	if err != nil {
		t.Fatalf("ExecuteFile: %v", err)
	}
	if !bytes.Equal(result.Response, request) {
		t.Fatalf("response = %q, want exact request %q", result.Response, request)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	if got := strings.TrimSpace(result.Stdout); got != "visible" {
		t.Fatalf("stdout = %q, want effective env marker", got)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("guest work root retained %d entries", len(entries))
	}
}

func TestExecuteFileReturnsStructuredNonzeroExit(t *testing.T) {
	spec := ProcessSpec{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestGuestHelperProcess", "--", "fail"},
		Env:     append(os.Environ(), helperModeEnv+"=1"),
	}

	result, err := ExecuteFile(context.Background(), t.TempDir(), spec, []byte(`{}`))
	var exitErr *ProcessExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %v, want ProcessExitError", err)
	}
	if exitErr.Code != 23 || result.ExitCode != 23 {
		t.Fatalf("exit codes = error:%d result:%d, want 23", exitErr.Code, result.ExitCode)
	}
	if !strings.Contains(exitErr.Stderr, "expected failure") || !strings.Contains(result.Stderr, "expected failure") {
		t.Fatalf("stderr not preserved: error=%q result=%q", exitErr.Stderr, result.Stderr)
	}
}

func TestExecuteFileHonorsContextCancellation(t *testing.T) {
	spec := ProcessSpec{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestGuestHelperProcess", "--", "sleep"},
		Env:     append(os.Environ(), helperModeEnv+"=1"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := ExecuteFile(ctx, t.TempDir(), spec, []byte(`{}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestExecuteFileRejectsMissingCommand(t *testing.T) {
	_, err := ExecuteFile(context.Background(), t.TempDir(), ProcessSpec{}, []byte(`{}`))
	if !errors.Is(err, ErrMissingCommand) {
		t.Fatalf("error = %v, want ErrMissingCommand", err)
	}
}

func TestGuestHelperProcess(t *testing.T) {
	if os.Getenv(helperModeEnv) != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || len(os.Args) < separator+4 {
		os.Exit(97)
	}
	mode := os.Args[separator+1]
	requestPath := os.Args[len(os.Args)-2]
	responsePath := os.Args[len(os.Args)-1]

	switch mode {
	case "copy":
		if os.Args[separator+2] != os.Getenv("EXPECTED_ARG") {
			os.Exit(96)
		}
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(95)
		}
		cwdInfo, cwdErr := os.Stat(cwd)
		expectedInfo, expectedErr := os.Stat(os.Getenv("EXPECTED_CWD"))
		if cwdErr != nil || expectedErr != nil || !os.SameFile(cwdInfo, expectedInfo) {
			os.Exit(95)
		}
		if requestPath != os.Getenv("EVAL_FILE_NAME_REQUEST") || responsePath != os.Getenv("EVAL_FILE_NAME_RESPONSE") {
			os.Exit(94)
		}
		data, err := os.ReadFile(requestPath)
		if err != nil {
			os.Exit(93)
		}
		if err := os.WriteFile(responsePath, data, 0o600); err != nil {
			os.Exit(92)
		}
		_, _ = os.Stdout.WriteString(os.Getenv("CUSTOM_VALUE") + "\n")
		os.Exit(0)
	case "fail":
		_, _ = os.Stderr.WriteString("expected failure\n")
		os.Exit(23)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	default:
		os.Exit(91)
	}
}
