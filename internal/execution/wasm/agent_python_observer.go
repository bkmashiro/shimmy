package wasm

import "time"

// AgentPythonPhase identifies one measured Agent Python lifecycle boundary.
type AgentPythonPhase string

const (
	AgentPythonPhaseArtifactVerify AgentPythonPhase = "artifact-verify"
	AgentPythonPhaseRuntimeCreate  AgentPythonPhase = "runtime-create"
	AgentPythonPhaseWASIImports    AgentPythonPhase = "wasi-imports"
	AgentPythonPhaseHostImports    AgentPythonPhase = "host-imports"
	AgentPythonPhaseCompile        AgentPythonPhase = "compile"
	AgentPythonPhaseInstantiate    AgentPythonPhase = "instantiate"
	AgentPythonPhaseInitialize     AgentPythonPhase = "initialize"
	AgentPythonPhaseRuntimeInit    AgentPythonPhase = "runtime-init"
	AgentPythonPhaseRuntimePrepare AgentPythonPhase = "runtime-prepare"
	AgentPythonPhaseHeadroom       AgentPythonPhase = "headroom"
	AgentPythonPhaseStrategySelect AgentPythonPhase = "strategy-select"
	AgentPythonPhaseSnapshotTake   AgentPythonPhase = "snapshot-take"
	AgentPythonPhaseCheckout       AgentPythonPhase = "checkout"
	AgentPythonPhaseExecute        AgentPythonPhase = "execute"
	AgentPythonPhaseDecode         AgentPythonPhase = "decode"
	AgentPythonPhaseRestore        AgentPythonPhase = "restore"
	AgentPythonPhaseClose          AgentPythonPhase = "close"
)

// AgentPythonPurpose explains why a slot or phase was created.
type AgentPythonPurpose string

const (
	AgentPythonPurposeStartup     AgentPythonPurpose = "startup"
	AgentPythonPurposeRequest     AgentPythonPurpose = "request"
	AgentPythonPurposeFresh       AgentPythonPurpose = "fresh"
	AgentPythonPurposeRefill      AgentPythonPurpose = "refill"
	AgentPythonPurposeReplacement AgentPythonPurpose = "replacement"
)

// AgentPythonOutcome is the terminal state of one observed phase.
type AgentPythonOutcome string

const (
	AgentPythonOutcomeOK    AgentPythonOutcome = "ok"
	AgentPythonOutcomeError AgentPythonOutcome = "error"
)

func agentPythonPhaseOutcome(err error) AgentPythonOutcome {
	if err != nil {
		return AgentPythonOutcomeError
	}
	return AgentPythonOutcomeOK
}

// AgentPythonPhaseEvent is immutable phase evidence delivered after timing stops.
// Observer callbacks may be concurrent during single-use refill.
type AgentPythonPhaseEvent struct {
	Phase             AgentPythonPhase   `json:"phase"`
	Purpose           AgentPythonPurpose `json:"purpose,omitempty"`
	Lifecycle         string             `json:"lifecycle,omitempty"`
	SnapshotRequested string             `json:"snapshot_requested,omitempty"`
	SnapshotSelected  string             `json:"snapshot_selected,omitempty"`
	RequestID         uint64             `json:"request_id,omitempty"`
	SlotID            uint64             `json:"slot_id,omitempty"`
	Duration          time.Duration      `json:"duration_ns"`
	MemoryBytes       uint64             `json:"memory_bytes,omitempty"`
	Outcome           AgentPythonOutcome `json:"outcome"`
	Error             string             `json:"error,omitempty"`
}

// AgentPythonPhaseObservation is the internal input used to finish a phase.
type AgentPythonPhaseObservation struct {
	Phase            AgentPythonPhase
	Purpose          AgentPythonPurpose
	RequestID        uint64
	SlotID           uint64
	Started          time.Time
	MemoryBytes      uint64
	SnapshotSelected string
	Outcome          AgentPythonOutcome
	Err              error
}

func (d *AgentPythonDispatcher) observeAgentPythonPhase(observation AgentPythonPhaseObservation) {
	observer := d.cfg.AgentPythonObserver
	if observer == nil {
		return
	}
	duration := time.Duration(0)
	if !observation.Started.IsZero() {
		duration = time.Since(observation.Started)
	}
	event := AgentPythonPhaseEvent{
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
	defer func() {
		if recovered := recover(); recovered != nil {
			d.log.Warn("agent-python observer panicked")
		}
	}()
	observer(event)
}
