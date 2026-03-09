//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Simulated sandbox config
type Config struct {
	MaxCPUSeconds uint64
	MaxMemoryMB   uint64
	MaxProcesses  uint64
	MaxFileSizeMB uint64
	MaxOpenFiles  uint64
	Timeout       time.Duration
	AllowNetwork  bool
	WrapperPath   string
}

func DefaultConfig() Config {
	return Config{
		MaxCPUSeconds: 5,
		MaxMemoryMB:   128,
		MaxProcesses:  10,
		MaxFileSizeMB: 10,
		MaxOpenFiles:  100,
		Timeout:       10 * time.Second,
		AllowNetwork:  false,
		WrapperPath:   "./sandbox-wrapper",
	}
}

func WrapCommandContext(ctx context.Context, name string, args []string, cfg Config) *exec.Cmd {
	wrapperArgs := []string{
		fmt.Sprintf("-cpu=%d", cfg.MaxCPUSeconds),
		fmt.Sprintf("-mem=%d", cfg.MaxMemoryMB),
		fmt.Sprintf("-nproc=%d", cfg.MaxProcesses),
		fmt.Sprintf("-fsize=%d", cfg.MaxFileSizeMB),
		fmt.Sprintf("-nofile=%d", cfg.MaxOpenFiles),
		fmt.Sprintf("-timeout=%s", cfg.Timeout),
	}
	if !cfg.AllowNetwork {
		wrapperArgs = append(wrapperArgs, "--no-network")
	}
	wrapperArgs = append(wrapperArgs, "--", name)
	wrapperArgs = append(wrapperArgs, args...)
	
	return exec.CommandContext(ctx, cfg.WrapperPath, wrapperArgs...)
}

func main() {
	ctx := context.Background()
	cfg := DefaultConfig()
	
	fmt.Println("=== Testing shimmy sandbox integration ===")
	fmt.Println()
	
	// Test 1: Network blocking
	fmt.Println("Test 1: Network should be blocked")
	cmd := WrapCommandContext(ctx, "python3", []string{"-c", `
import socket
try:
    s = socket.socket()
    print("FAIL: socket allowed")
except Exception as e:
    print(f"PASS: socket blocked - {e}")
`}, cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
	fmt.Println()
	
	// Test 2: Memory limit
	fmt.Println("Test 2: Memory should be limited")
	cmd = WrapCommandContext(ctx, "python3", []string{"-c", `
try:
    x = []
    for i in range(200):
        x.append('A' * 1024 * 1024)
except MemoryError:
    print("PASS: memory limited")
`}, cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
	fmt.Println()
	
	// Test 3: File size limit
	fmt.Println("Test 3: File size should be limited")
	cmd = WrapCommandContext(ctx, "python3", []string{"-c", `
import os
try:
    with open('/tmp/test_big', 'wb') as f:
        for i in range(20):
            f.write(b'X' * 1024 * 1024)
except Exception as e:
    print(f"PASS: file size limited - {e}")
finally:
    try:
        os.remove('/tmp/test_big')
    except:
        pass
`}, cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
	fmt.Println()
	
	fmt.Println("=== All tests complete ===")
}
