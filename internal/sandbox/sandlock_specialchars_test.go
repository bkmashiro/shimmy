package sandbox

import (
	"context"
	"reflect"
	"testing"
)

func TestSandlockBackend_WrapCommandPreservesSpecialCharacterArgs(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}

	args := []string{
		"--flag=semi;colon",
		"space separated value",
		"quote\"and'apostrophe",
		"dollar$PATH",
		"json={\"key\":\"value\"}",
	}

	cmd, err := backend.WrapCommand(context.Background(), "tool", args, Config{AllowNetwork: true})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	want := append([]string{bin, "--", "tool"}, args...)
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}
