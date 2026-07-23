package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestGuestOptionsUseStableVirtioDefaults(t *testing.T) {
	got, err := parseGuestOptions(nil)
	if err != nil {
		t.Fatalf("parseGuestOptions: %v", err)
	}
	if got.device != "/dev/virtio-ports/org.shimmy.control" || got.workRoot != "/run/shimmy" || got.maxFrameBytes != 4<<20 {
		t.Fatalf("defaults = %#v", got)
	}
}

func TestGuestOptionsParseExplicitValues(t *testing.T) {
	got, err := parseGuestOptions([]string{"--device", "/dev/vport0p1", "--work-root", "/tmp/guest", "--max-frame-bytes", "8192"})
	if err != nil {
		t.Fatalf("parseGuestOptions: %v", err)
	}
	if got.device != "/dev/vport0p1" || got.workRoot != "/tmp/guest" || got.maxFrameBytes != 8192 {
		t.Fatalf("options = %#v", got)
	}
}

func TestGuestOptionsRejectInvalidFrameBound(t *testing.T) {
	if _, err := parseGuestOptions([]string{"--max-frame-bytes", "128"}); err == nil {
		t.Fatal("parseGuestOptions accepted undersized frame bound")
	}
}

func TestRunGuestReportsDeviceOpenFailure(t *testing.T) {
	want := errors.New("open failed")
	err := runGuest(context.Background(), []string{"--device", "/missing"}, func(string) (io.ReadWriteCloser, error) {
		return nil, want
	})
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "/missing") {
		t.Fatalf("error = %v", err)
	}
}
