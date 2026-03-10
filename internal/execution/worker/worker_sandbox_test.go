package worker

import (
	"context"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/sandbox"
)

func TestCreateCmd_WithoutSandboxUsesOriginalCommand(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "echo",
		Args: []string{"hello"},
		Cwd:  "/tmp",
		Env:  []string{"FOO=bar"},
	})

	if cmd.Path != "echo" {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, "echo")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "echo" || cmd.Args[1] != "hello" {
		t.Fatalf("cmd.Args = %v, want [echo hello]", cmd.Args)
	}
	if cmd.Dir != "/tmp" {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, "/tmp")
	}
	if !contains(cmd.Env, "FOO=bar") {
		t.Fatalf("cmd.Env = %v, want entry %q", cmd.Env, "FOO=bar")
	}
}

func TestCreateCmd_WithSandboxDirectBackendWrapsCommand(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "echo",
		Args: []string{"hello"},
		Cwd:  "/tmp",
		Env:  []string{"FOO=bar"},
	})

	if cmd.Path != "echo" {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, "echo")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "echo" || cmd.Args[1] != "hello" {
		t.Fatalf("cmd.Args = %v, want [echo hello]", cmd.Args)
	}
	if cmd.Dir != "/tmp" {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, "/tmp")
	}
	if !contains(cmd.Env, "FOO=bar") {
		t.Fatalf("cmd.Env = %v, want entry %q", cmd.Env, "FOO=bar")
	}
}

func TestCreateCmd_WithUnavailableBackendFallsBackToDirect(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "wasm")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "echo",
		Args: []string{"hello"},
	})

	if cmd.Path != "echo" {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, "echo")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "echo" || cmd.Args[1] != "hello" {
		t.Fatalf("cmd.Args = %v, want [echo hello]", cmd.Args)
	}
}

func TestCreateCmd_UsesSandboxConfigOverride(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "echo",
		Args: []string{"hello"},
		SandboxConfig: &sandbox.Config{
			WorkDir: "/tmp",
		},
	})

	if cmd.Dir != "/tmp" {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, "/tmp")
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}

	return false
}
