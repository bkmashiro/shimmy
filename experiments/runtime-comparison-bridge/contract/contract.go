package contract

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

func Valid(workload Workload, invocationCount uint64) bool {
	return workload.Iterations >= 0 && workload.Iterations <= MaxIterations && invocationCount > 0
}

func Evaluate(response, answer string, workload Workload, invocationCount uint64) Result {
	value := workload.Seed
	for index := 0; index < workload.Iterations; index++ {
		value = value*multiplier + increment + uint64(index)
	}
	return Result{
		IsCorrect:            response == answer,
		WorkChecksum:         hex16(value),
		GuestInvocationCount: invocationCount,
	}
}

func hex16(value uint64) string {
	const digits = "0123456789abcdef"
	var encoded [16]byte
	for index := len(encoded) - 1; index >= 0; index-- {
		encoded[index] = digits[value&0xf]
		value >>= 4
	}
	return string(encoded[:])
}
