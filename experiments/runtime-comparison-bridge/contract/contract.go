package contract

import (
	"errors"
	"fmt"
)

const MaxIterations = 5_000_000

const (
	multiplier uint64 = 6364136223846793005
	increment  uint64 = 1442695040888963407
)

type Workload struct {
	Iterations int    `json:"iterations"`
	Seed       uint64 `json:"seed"`
}

type Result struct {
	IsCorrect            bool   `json:"is_correct"`
	WorkChecksum         string `json:"work_checksum"`
	GuestInvocationCount uint64 `json:"guest_invocation_count"`
}

func Evaluate(response, answer string, workload Workload, invocationCount uint64) (Result, error) {
	if workload.Iterations < 0 || workload.Iterations > MaxIterations {
		return Result{}, fmt.Errorf("iterations must be in [0,%d]", MaxIterations)
	}
	if invocationCount == 0 {
		return Result{}, errors.New("invocation count must be positive")
	}

	value := workload.Seed
	for index := 0; index < workload.Iterations; index++ {
		value = value*multiplier + increment + uint64(index)
	}
	return Result{
		IsCorrect:            response == answer,
		WorkChecksum:         fmt.Sprintf("%016x", value),
		GuestInvocationCount: invocationCount,
	}, nil
}
