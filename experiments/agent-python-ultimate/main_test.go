package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidWorkerResultRequiresResumableStatusAndExactRow(t *testing.T) {
	row := PlanRow{ID: "row-1", Campaign: "capability", Lifecycle: LifecycleFresh, Pool: 1, PreparedCapacity: 1, Repeat: 1, Surface: "direct"}
	path := filepath.Join(t.TempDir(), "row.json")

	result := WorkerResult{Schema: workerResultSchema, Row: row, Status: "failed"}
	require.NoError(t, writeJSON(path, result))
	assert.False(t, validWorkerResult(path, row, "ok", "unavailable", "unsupported"))

	result.Status = "ok"
	require.NoError(t, writeJSON(path, result))
	assert.True(t, validWorkerResult(path, row, "ok", "unavailable", "unsupported"))

	changed := row
	changed.Surface = "http"
	assert.False(t, validWorkerResult(path, changed, "ok", "unavailable", "unsupported"))
}

func TestRunPlanWorkerProcessRecordsWorkerCrashAsFailedRow(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "crash-worker.sh")
	require.NoError(t, os.WriteFile(executable, []byte("#!/bin/sh\nexit 4\n"), 0o700))
	row := PlanRow{ID: "fault-row", Campaign: "fault-recovery", Lifecycle: LifecycleSnapshotCow, Pool: 1, PreparedCapacity: 1, Repeat: 1, Surface: "direct", Fault: "timeout", SnapshotSelected: "cow"}
	resultPath := filepath.Join(dir, "result.json")

	require.NoError(t, runPlanWorkerProcess(
		executable,
		filepath.Join(dir, "input.json"),
		resultPath,
		filepath.Join(dir, "stdout"),
		filepath.Join(dir, "stderr"),
		row,
	))

	raw, err := os.ReadFile(resultPath)
	require.NoError(t, err)
	var result WorkerResult
	require.NoError(t, decodeStrictJSON(raw, &result))
	assert.Equal(t, workerResultSchema, result.Schema)
	assert.Equal(t, row, result.Row)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Error, "exit status 4")
	assert.NotEmpty(t, result.StartedUTC)
	assert.NotEmpty(t, result.FinishedUTC)
}
