//go:build !linux

package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Fprintf(os.Stderr, "lambda-probe is Linux-only (current OS: %s)\n", runtime.GOOS)
	os.Exit(2)
}
