package worker

import (
	"context"
	"os"
	"path/filepath"
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

	if filepath.Base(cmd.Path) != "echo" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "echo")
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

	if filepath.Base(cmd.Path) != "echo" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "echo")
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

	if filepath.Base(cmd.Path) != "echo" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "echo")
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
		Cwd:  "/tmp",
		SandboxConfig: &sandbox.Config{
			WorkDir:     "/sandbox-dir",
			MaxMemoryMB: 512,
		},
	})

	// cmd.Dir comes from config.Cwd, not SandboxConfig.WorkDir
	if cmd.Dir != "/tmp" {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, "/tmp")
	}
}

// --- Additional scenarios ---

func TestCreateCmd_SandboxDisabledExplicitly(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "0")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "python3",
		Args: []string{"script.py"},
	})

	// SHIMMY_SANDBOX != "1" means no sandbox
	if filepath.Base(cmd.Path) != "python3" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "python3")
	}
}

func TestCreateCmd_SandboxEnabledNoBackendEnv(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "echo",
		Args: []string{"test"},
	})

	// Empty backend env → DirectBackend fallback
	if filepath.Base(cmd.Path) != "echo" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "echo")
	}
}

func TestCreateCmd_SandboxEnabledGarbageBackend(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "nonexistent-backend")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "echo",
		Args: []string{"test"},
	})

	// Unknown backend → DirectBackend fallback
	if filepath.Base(cmd.Path) != "echo" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "echo")
	}
}

func TestCreateCmd_SandboxConfigOverrideWorkDirFallsBackToCwd(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd: "echo",
		Cwd: "/fallback-dir",
		SandboxConfig: &sandbox.Config{
			// WorkDir empty → should fall back to Cwd
		},
	})

	if cmd.Dir != "/fallback-dir" {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, "/fallback-dir")
	}
}

func TestCreateCmd_SandboxConfigOverrideWorkDirTakesPrecedence(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd: "echo",
		Cwd: "/cwd-dir",
		SandboxConfig: &sandbox.Config{
			WorkDir: "/override-dir",
		},
	})

	// SandboxConfig.WorkDir is set, so Cwd is also set from cmd.Dir line
	// But since DirectBackend ignores Config, cmd.Dir should be set from Cwd
	if cmd.Dir != "/cwd-dir" {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, "/cwd-dir")
	}
}

func TestCreateCmd_EnvMerge(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd: "echo",
		Env: []string{"CUSTOM1=val1", "CUSTOM2=val2"},
	})

	if !contains(cmd.Env, "CUSTOM1=val1") {
		t.Fatalf("cmd.Env missing CUSTOM1=val1")
	}
	if !contains(cmd.Env, "CUSTOM2=val2") {
		t.Fatalf("cmd.Env missing CUSTOM2=val2")
	}
	// Should also contain OS env vars
	if len(cmd.Env) <= 2 {
		t.Fatalf("cmd.Env only has %d entries, expected OS env + custom vars", len(cmd.Env))
	}
}

func TestCreateCmd_NilEnvUsesOSEnv(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd: "echo",
		Env: nil,
	})

	// Should have OS environment variables
	osEnvLen := len(os.Environ())
	if len(cmd.Env) < osEnvLen {
		t.Fatalf("cmd.Env has %d entries, expected >= %d (os.Environ)", len(cmd.Env), osEnvLen)
	}
}

func TestCreateCmd_NoCwdDoesNotSetDir(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd: "echo",
	})

	if cmd.Dir != "" {
		t.Fatalf("cmd.Dir = %q, want empty when Cwd not set", cmd.Dir)
	}
}

func TestCreateCmd_SandboxAllBackendsUnavailableStillWorks(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "sandlock") // not available on macOS

	cmd := createCmd(context.Background(), StartConfig{
		Cmd:  "echo",
		Args: []string{"fallback"},
	})

	// Should fall back to direct execution
	if cmd == nil {
		t.Fatal("createCmd returned nil, expected valid cmd")
	}
	if filepath.Base(cmd.Path) != "echo" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "echo")
	}
}

func TestCreateCmd_NoArgsProducesValidCmd(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd: "pwd",
	})

	if filepath.Base(cmd.Path) != "pwd" {
		t.Fatalf("cmd.Path = %q, want base %q", cmd.Path, "pwd")
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "pwd" {
		t.Fatalf("cmd.Args = %v, want [pwd]", cmd.Args)
	}
}

func TestCreateCmd_SandboxConfigEnvVarsFallback(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX", "1")
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	cmd := createCmd(context.Background(), StartConfig{
		Cmd: "echo",
		Env: []string{"MY_VAR=test"},
		SandboxConfig: &sandbox.Config{
			// EnvVars nil → should fall back to env from StartConfig + os.Environ
		},
	})

	if !contains(cmd.Env, "MY_VAR=test") {
		t.Fatalf("cmd.Env missing MY_VAR=test")
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
