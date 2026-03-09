package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	timeout := flag.Duration("timeout", 10*time.Second, "Execution timeout")
	maxMem := flag.Int64("mem", 256, "Max memory in MB")
	maxCPU := flag.Int64("cpu", 5, "Max CPU seconds")
	noNetwork := flag.Bool("no-network", false, "Block network syscalls")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Usage: sandbox-test [options] command [args...]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	fmt.Printf("🔒 Sandbox Config:\n")
	fmt.Printf("   Timeout: %v\n", *timeout)
	fmt.Printf("   Memory: %d MB\n", *maxMem)
	fmt.Printf("   CPU: %d seconds\n", *maxCPU)
	fmt.Printf("   Network: %v\n", !*noNetwork)
	fmt.Printf("   Command: %s\n\n", strings.Join(args, " "))

	// Create command
	cmd := exec.Command(args[0], args[1:]...)
	
	// Capture output
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	
	// Start
	start := time.Now()
	if err := cmd.Start(); err != nil {
		fmt.Printf("❌ Failed to start: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("⏳ Running (PID: %d)...\n\n", cmd.Process.Pid)
	
	// Read output
	go io.Copy(os.Stdout, stdout)
	go io.Copy(os.Stderr, stderr)
	
	// Wait with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	
	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("\n❌ Exited with error: %v (%.2fs)\n", err, elapsed.Seconds())
		} else {
			fmt.Printf("\n✅ Success (%.2fs)\n", elapsed.Seconds())
		}
	case <-time.After(*timeout):
		cmd.Process.Kill()
		fmt.Printf("\n⏰ Timeout after %v\n", *timeout)
	}
}
