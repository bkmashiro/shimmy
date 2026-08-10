package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/lambda-feedback/shimmy/internal/execution/qemuguest"
)

const (
	minGuestFrameBytes = 1024
	maxGuestFrameBytes = 64 << 20
)

type guestOptions struct {
	device        string
	workRoot      string
	maxFrameBytes int
}

func parseGuestOptions(args []string) (guestOptions, error) {
	options := guestOptions{}
	flags := flag.NewFlagSet("shimmy-qemu-guest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.device, "device", "/dev/virtio-ports/org.shimmy.control", "virtio-serial control device")
	flags.StringVar(&options.workRoot, "work-root", "/run/shimmy", "guest request work root")
	flags.IntVar(&options.maxFrameBytes, "max-frame-bytes", 4<<20, "maximum bridge frame body size")
	if err := flags.Parse(args); err != nil {
		return guestOptions{}, fmt.Errorf("qemu guest: parse options: %w", err)
	}
	if flags.NArg() != 0 {
		return guestOptions{}, fmt.Errorf("qemu guest: unexpected positional arguments")
	}
	if options.device == "" || options.workRoot == "" {
		return guestOptions{}, fmt.Errorf("qemu guest: device and work-root must be non-empty")
	}
	if options.maxFrameBytes < minGuestFrameBytes || options.maxFrameBytes > maxGuestFrameBytes {
		return guestOptions{}, fmt.Errorf("qemu guest: max-frame-bytes must be in [%d,%d]", minGuestFrameBytes, maxGuestFrameBytes)
	}
	return options, nil
}

func runGuest(
	ctx context.Context,
	args []string,
	opener func(string) (io.ReadWriteCloser, error),
) error {
	options, err := parseGuestOptions(args)
	if err != nil {
		return err
	}
	connection, err := opener(options.device)
	if err != nil {
		return fmt.Errorf("qemu guest: open control device %q: %w", options.device, err)
	}
	return qemuguest.Serve(ctx, connection, qemuguest.ServerConfig{
		WorkRoot:      options.workRoot,
		MaxFrameBytes: options.maxFrameBytes,
	})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runGuest(ctx, os.Args[1:], func(path string) (io.ReadWriteCloser, error) {
		return os.OpenFile(path, os.O_RDWR, 0)
	}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
