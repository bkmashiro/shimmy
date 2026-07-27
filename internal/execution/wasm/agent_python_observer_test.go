package wasm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentPythonObserverRecordsPhaseIdentity(t *testing.T) {
	var got AgentPythonPhaseEvent
	dispatcher := NewAgentPythonDispatcher(Config{
		AgentPythonObserver: func(event AgentPythonPhaseEvent) {
			got = event
		},
	}, zap.NewNop())
	started := time.Now().Add(-time.Millisecond)

	dispatcher.observeAgentPythonPhase(AgentPythonPhaseObservation{
		Phase:     AgentPythonPhaseExecute,
		Purpose:   AgentPythonPurposeRequest,
		RequestID: 7,
		SlotID:    11,
		Started:   started,
		Outcome:   AgentPythonOutcomeOK,
	})

	assert.Equal(t, AgentPythonPhaseExecute, got.Phase)
	assert.Equal(t, AgentPythonPurposeRequest, got.Purpose)
	assert.Equal(t, uint64(7), got.RequestID)
	assert.Equal(t, uint64(11), got.SlotID)
	assert.Equal(t, AgentPythonOutcomeOK, got.Outcome)
	assert.GreaterOrEqual(t, got.Duration, time.Millisecond)
}

func TestAgentPythonObserverNilIsNoop(t *testing.T) {
	dispatcher := NewAgentPythonDispatcher(Config{}, zap.NewNop())
	require.NotPanics(t, func() {
		dispatcher.observeAgentPythonPhase(AgentPythonPhaseObservation{
			Phase:   AgentPythonPhaseCompile,
			Started: time.Now(),
			Outcome: AgentPythonOutcomeOK,
		})
	})
}

func TestAgentPythonObserverPanicDoesNotAffectRuntime(t *testing.T) {
	dispatcher := NewAgentPythonDispatcher(Config{
		AgentPythonObserver: func(AgentPythonPhaseEvent) {
			panic("observer failed")
		},
	}, zap.NewNop())

	require.NotPanics(t, func() {
		dispatcher.observeAgentPythonPhase(AgentPythonPhaseObservation{
			Phase:   AgentPythonPhaseCompile,
			Started: time.Now(),
			Outcome: AgentPythonOutcomeError,
			Err:     assert.AnError,
		})
	})
}
