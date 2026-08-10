package qemurun

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

var ErrInvalidInvocation = errors.New("qemu runner: invalid invocation")

type Invocation struct {
	Start            StartMessage
	HostRequestPath  string
	HostResponsePath string
}

func ParseInvocation(args, effectiveEnv []string) (Invocation, error) {
	if len(args) == 0 || args[0] != "--" {
		return Invocation{}, fmt.Errorf("%w: first argument must be --", ErrInvalidInvocation)
	}
	if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
		return Invocation{}, fmt.Errorf("%w: missing evaluator command", ErrInvalidInvocation)
	}

	guestEnv := make([]string, 0, len(effectiveEnv))
	for _, entry := range effectiveEnv {
		name := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			name = entry[:index]
		}
		if strings.HasPrefix(name, "FUNCTION_QEMU_") || strings.HasPrefix(name, "SHIMMY_QEMU_") {
			continue
		}
		guestEnv = append(guestEnv, entry)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Invocation{}, fmt.Errorf("%w: get cwd: %v", ErrInvalidInvocation, err)
	}
	start := StartMessage{
		Command: args[1],
		Cwd:     cwd,
		Env:     guestEnv,
	}

	interfaceValue := envLast(effectiveEnv, "EVAL_IO")
	switch {
	case strings.EqualFold(interfaceValue, "file"):
		if len(args) < 4 {
			return Invocation{}, fmt.Errorf("%w: file invocation requires request and response paths", ErrInvalidInvocation)
		}
		requestPath := args[len(args)-2]
		responsePath := args[len(args)-1]
		if requestPath == "" || responsePath == "" {
			return Invocation{}, fmt.Errorf("%w: file invocation has empty request or response path", ErrInvalidInvocation)
		}
		if envLast(effectiveEnv, "EVAL_FILE_NAME_REQUEST") != requestPath || envLast(effectiveEnv, "EVAL_FILE_NAME_RESPONSE") != responsePath {
			return Invocation{}, fmt.Errorf("%w: file argv and EVAL_FILE_NAME_* paths differ", ErrInvalidInvocation)
		}
		start.Mode = ModeFile
		start.Args = append([]string(nil), args[2:len(args)-2]...)
		return Invocation{
			Start:            start,
			HostRequestPath:  requestPath,
			HostResponsePath: responsePath,
		}, nil

	case strings.EqualFold(interfaceValue, "rpc"):
		transport := strings.ToLower(strings.TrimSpace(envLast(effectiveEnv, "EVAL_RPC_TRANSPORT")))
		endpoint, err := rpcEndpoint(effectiveEnv, transport)
		if err != nil {
			return Invocation{}, err
		}
		start.Mode = ModeRPC
		start.Args = append([]string(nil), args[2:]...)
		start.Transport = transport
		start.Endpoint = endpoint
		return Invocation{Start: start}, nil

	default:
		return Invocation{}, fmt.Errorf("%w: unsupported EVAL_IO %q", ErrInvalidInvocation, interfaceValue)
	}
}

func rpcEndpoint(env []string, transport string) (string, error) {
	var endpoint string
	switch transport {
	case "stdio":
		return "", nil
	case "ipc":
		endpoint = envLast(env, "EVAL_RPC_IPC_ENDPOINT")
	case "tcp":
		endpoint = envLast(env, "EVAL_RPC_TCP_ADDRESS")
	case "http":
		endpoint = envLast(env, "EVAL_RPC_HTTP_URL")
	case "ws":
		endpoint = envLast(env, "EVAL_RPC_WS_URL")
	default:
		return "", fmt.Errorf("%w: unsupported or missing RPC transport %q", ErrInvalidInvocation, transport)
	}
	if strings.TrimSpace(endpoint) == "" {
		return "", fmt.Errorf("%w: missing endpoint for RPC transport %q", ErrInvalidInvocation, transport)
	}
	return endpoint, nil
}

func envLast(env []string, key string) string {
	prefix := key + "="
	for index := len(env) - 1; index >= 0; index-- {
		if strings.HasPrefix(env[index], prefix) {
			return strings.TrimPrefix(env[index], prefix)
		}
	}
	return ""
}
