package qemurun

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseInvocationPreservesFileCommandArgumentsAndEffectiveEnvironment(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"FUNCTION_QEMU_ENABLED=true",
		"FUNCTION_QEMU_BINARY=/opt/qemu",
		"SHIMMY_QEMU_PRIVATE=secret-control",
		"FUNCTION_DBI_SECURITY_ENABLED=false",
		"CUSTOM=visible",
		"EVAL_IO=FILE",
		"EVAL_FILE_NAME_REQUEST=/host/request.json",
		"EVAL_FILE_NAME_RESPONSE=/host/response.json",
	}
	got, err := ParseInvocation([]string{
		"--", "/opt/python 3", "worker.py", "arg with spaces", "--literal=--",
		"/host/request.json", "/host/response.json",
	}, env)
	if err != nil {
		t.Fatalf("ParseInvocation: %v", err)
	}
	if got.Start.Mode != ModeFile || got.Start.Command != "/opt/python 3" {
		t.Fatalf("start = %#v", got.Start)
	}
	wantArgs := []string{"worker.py", "arg with spaces", "--literal=--"}
	if !reflect.DeepEqual(got.Start.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", got.Start.Args, wantArgs)
	}
	if got.HostRequestPath != "/host/request.json" || got.HostResponsePath != "/host/response.json" {
		t.Fatalf("host paths = %q %q", got.HostRequestPath, got.HostResponsePath)
	}
	wantEnv := []string{
		"PATH=/usr/bin",
		"FUNCTION_DBI_SECURITY_ENABLED=false",
		"CUSTOM=visible",
		"EVAL_IO=FILE",
		"EVAL_FILE_NAME_REQUEST=/host/request.json",
		"EVAL_FILE_NAME_RESPONSE=/host/response.json",
	}
	if !reflect.DeepEqual(got.Start.Env, wantEnv) {
		t.Fatalf("guest env = %#v, want %#v", got.Start.Env, wantEnv)
	}
}

func TestParseInvocationPreservesPersistentRPCCommand(t *testing.T) {
	got, err := ParseInvocation([]string{"--", "node", "rpc.js", "--serve"}, []string{
		"EVAL_IO=rpc",
		"EVAL_RPC_TRANSPORT=stdio",
		"CUSTOM=yes",
	})
	if err != nil {
		t.Fatalf("ParseInvocation: %v", err)
	}
	if got.Start.Mode != ModeRPC || got.Start.Command != "node" || got.Start.Transport != "stdio" {
		t.Fatalf("start = %#v", got.Start)
	}
	if !reflect.DeepEqual(got.Start.Args, []string{"rpc.js", "--serve"}) {
		t.Fatalf("args = %#v", got.Start.Args)
	}
	if got.HostRequestPath != "" || got.HostResponsePath != "" {
		t.Fatalf("rpc unexpectedly has host file paths: %#v", got)
	}
}

func TestParseInvocationRejectsMalformedBoundaries(t *testing.T) {
	for name, test := range map[string]struct {
		args []string
		env  []string
	}{
		"missing marker":        {args: []string{"python3", "worker.py"}, env: []string{"EVAL_IO=rpc", "EVAL_RPC_TRANSPORT=stdio"}},
		"missing command":       {args: []string{"--"}, env: []string{"EVAL_IO=rpc", "EVAL_RPC_TRANSPORT=stdio"}},
		"file missing paths":    {args: []string{"--", "python3", "worker.py"}, env: []string{"EVAL_IO=FILE"}},
		"unsupported interface": {args: []string{"--", "worker"}, env: []string{"EVAL_IO=wasm"}},
		"rpc missing transport": {args: []string{"--", "worker"}, env: []string{"EVAL_IO=rpc"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseInvocation(test.args, test.env)
			if !errors.Is(err, ErrInvalidInvocation) {
				t.Fatalf("error = %v, want ErrInvalidInvocation", err)
			}
		})
	}
}
