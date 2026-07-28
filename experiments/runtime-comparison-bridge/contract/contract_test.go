package contract

import "testing"

func TestEvaluateDeterministicWorkload(t *testing.T) {
	workload := Workload{Iterations: 100_000, Seed: 7}
	if !Valid(workload, 1) {
		t.Fatal("expected workload to be valid")
	}
	result := Evaluate("42", "42", workload, 1)
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
	result := Evaluate("41", "42", Workload{Iterations: 0, Seed: 7}, 3)
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

func TestValidRejectsUnboundedWorkAndZeroInvocation(t *testing.T) {
	for _, iterations := range []int{-1, MaxIterations + 1} {
		if Valid(Workload{Iterations: iterations, Seed: 7}, 1) {
			t.Fatalf("iterations %d should be invalid", iterations)
		}
	}
	if Valid(Workload{}, 0) {
		t.Fatal("zero invocation count should be invalid")
	}
}
