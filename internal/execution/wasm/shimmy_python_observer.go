package wasm

import "time"

// ShimmyPythonPhase identifies one measured Shimmy Python lifecycle boundary.
type ShimmyPythonPhase string

const (
	ShimmyPythonPhaseArtifactVerify ShimmyPythonPhase = "artifact-verify"
	ShimmyPythonPhaseRuntimeCreate  ShimmyPythonPhase = "runtime-create"
	ShimmyPythonPhaseWASIImports    ShimmyPythonPhase = "wasi-imports"
	ShimmyPythonPhaseCompile        ShimmyPythonPhase = "compile"
	ShimmyPythonPhaseInstantiate    ShimmyPythonPhase = "instantiate"
	ShimmyPythonPhaseInitialize     ShimmyPythonPhase = "initialize"
	ShimmyPythonPhaseRuntimeInit    ShimmyPythonPhase = "runtime-init"
	ShimmyPythonPhaseRuntimePrepare ShimmyPythonPhase = "runtime-prepare"
	ShimmyPythonPhaseHeadroom       ShimmyPythonPhase = "headroom"
	ShimmyPythonPhaseStrategySelect ShimmyPythonPhase = "strategy-select"
	ShimmyPythonPhaseSnapshotTake   ShimmyPythonPhase = "snapshot-take"
	ShimmyPythonPhaseCheckout       ShimmyPythonPhase = "checkout"
	ShimmyPythonPhaseEvaluate       ShimmyPythonPhase = "evaluate"
	ShimmyPythonPhaseDecode         ShimmyPythonPhase = "decode"
	ShimmyPythonPhaseRestore        ShimmyPythonPhase = "restore"
	ShimmyPythonPhaseClose          ShimmyPythonPhase = "close"
)

// ShimmyPythonPurpose explains why a slot or phase was created.
type ShimmyPythonPurpose string

const (
	ShimmyPythonPurposeStartup     ShimmyPythonPurpose = "startup"
	ShimmyPythonPurposeRequest     ShimmyPythonPurpose = "request"
	ShimmyPythonPurposeFresh       ShimmyPythonPurpose = "fresh"
	ShimmyPythonPurposeRefill      ShimmyPythonPurpose = "refill"
	ShimmyPythonPurposeReplacement ShimmyPythonPurpose = "replacement"
)

// ShimmyPythonOutcome is the terminal state of one observed phase.
type ShimmyPythonOutcome string

const (
	ShimmyPythonOutcomeOK    ShimmyPythonOutcome = "ok"
	ShimmyPythonOutcomeError ShimmyPythonOutcome = "error"
)

func shimmyPythonPhaseOutcome(err error) ShimmyPythonOutcome {
	if err != nil {
		return ShimmyPythonOutcomeError
	}
	return ShimmyPythonOutcomeOK
}

// ShimmyPythonPhaseEvent is immutable phase evidence delivered after timing stops.
// Observer callbacks may be concurrent during single-use refill.
type ShimmyPythonPhaseEvent struct {
	Phase             ShimmyPythonPhase   `json:"phase"`
	Purpose           ShimmyPythonPurpose `json:"purpose,omitempty"`
	Lifecycle         string              `json:"lifecycle,omitempty"`
	SnapshotRequested string              `json:"snapshot_requested,omitempty"`
	SnapshotSelected  string              `json:"snapshot_selected,omitempty"`
	RequestID         uint64              `json:"request_id,omitempty"`
	SlotID            uint64              `json:"slot_id,omitempty"`
	Duration          time.Duration       `json:"duration_ns"`
	MemoryBytes       uint64              `json:"memory_bytes,omitempty"`
	Outcome           ShimmyPythonOutcome `json:"outcome"`
	Error             string              `json:"error,omitempty"`
}

// ShimmyPythonPhaseObservation is the internal input used to finish a phase.
type ShimmyPythonPhaseObservation struct {
	Phase            ShimmyPythonPhase
	Purpose          ShimmyPythonPurpose
	RequestID        uint64
	SlotID           uint64
	Started          time.Time
	MemoryBytes      uint64
	SnapshotSelected string
	Outcome          ShimmyPythonOutcome
	Err              error
}

func (d *ShimmyPythonDispatcher) observeShimmyPythonPhase(observation ShimmyPythonPhaseObservation) {
	observer := d.cfg.ShimmyPythonObserver
	if observer == nil {
		return
	}
	duration := time.Duration(0)
	if !observation.Started.IsZero() {
		duration = time.Since(observation.Started)
	}
	event := ShimmyPythonPhaseEvent{
		Phase:             observation.Phase,
		Purpose:           observation.Purpose,
		Lifecycle:         d.cfg.PythonLifecycle,
		SnapshotRequested: d.cfg.SnapshotMode,
		SnapshotSelected:  observation.SnapshotSelected,
		RequestID:         observation.RequestID,
		SlotID:            observation.SlotID,
		Duration:          duration,
		MemoryBytes:       observation.MemoryBytes,
		Outcome:           observation.Outcome,
	}
	if observation.Err != nil {
		event.Error = observation.Err.Error()
	}
	d.emitShimmyPythonPhaseEvent(observer, event)
}

func (d *ShimmyPythonDispatcher) emitShimmyPythonPhaseEvent(observer func(ShimmyPythonPhaseEvent), event ShimmyPythonPhaseEvent) {
	if observer == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			d.log.Warn("shimmy-python observer panicked")
		}
	}()
	observer(event)
}
