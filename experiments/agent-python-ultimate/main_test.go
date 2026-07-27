package main

import (
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
