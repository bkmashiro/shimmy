package contract

import "testing"

func TestEvaluateDeterministicWorkload(t *testing.T) {
	result, err := Evaluate("42", "42", Workload{Iterations: 100_000, Seed: 7}, 1)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.IsCorrect {
		t.Fatal("expected exact response to be correct")
	}
	if result.WorkChecksum != "5e7135fac6225d57" {
		t.Fatalf("checksum = %q", result.WorkChecksum)
	}
	if result.GuestInvocationCount != 1 {
		t.Fatalf("invocation count = %d", result.GuestInvocationCount)
	}
}

func TestEvaluateZeroWorkAndMismatch(t *testing.T) {
	result, err := Evaluate("41", "42", Workload{Iterations: 0, Seed: 7}, 3)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.IsCorrect {
		t.Fatal("expected mismatch to be incorrect")
	}
	if result.WorkChecksum != "0000000000000007" {
		t.Fatalf("checksum = %q", result.WorkChecksum)
	}
	if result.GuestInvocationCount != 3 {
		t.Fatalf("invocation count = %d", result.GuestInvocationCount)
	}
}

func TestEvaluateRejectsUnboundedWork(t *testing.T) {
	for _, iterations := range []int{-1, MaxIterations + 1} {
		if _, err := Evaluate("42", "42", Workload{Iterations: iterations, Seed: 7}, 1); err == nil {
			t.Fatalf("iterations %d should fail", iterations)
		}
	}
}
