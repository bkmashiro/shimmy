package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanConfigStrictDecoderRejectsUnknownField(t *testing.T) {
	_, err := ParsePlanConfig([]byte(`{"schema":"agent-python-ultimate-config/v1","seed":1,"unknown":1}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestPlanExpanderReturnsExpectedUltimateRowCount(t *testing.T) {
	path := filepath.Join("configs", "ultimate.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	cfg, err := ParsePlanConfig(raw)
	require.NoError(t, err)

	plan, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{Seed: cfg.Seed})
	require.NoError(t, err)
	assert.Equal(t, 1401, len(plan.Rows))

	counts := map[string]int{}
	for _, row := range plan.Rows {
		counts[row.Campaign]++
	}
	for campaign, expected := range campaignRowCounts {
		assert.Equalf(t, expected, counts[campaign], "campaign %s mismatch", campaign)
	}
}

func TestUltimateRowsRespectProductionRuntimeBounds(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("configs", "ultimate.json"))
	require.NoError(t, err)
	cfg, err := ParsePlanConfig(raw)
	require.NoError(t, err)
	plan, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{Seed: cfg.Seed})
	require.NoError(t, err)

	for _, row := range plan.Rows {
		if row.Concurrency != nil {
			assert.LessOrEqual(t, *row.Concurrency, 16, row.ID)
		}
		switch row.Lifecycle {
		case LifecycleSnapshotMemcpy:
			assert.Equal(t, "memcpy", row.SnapshotSelected, row.ID)
		case LifecycleSnapshotCow:
			assert.Equal(t, "cow", row.SnapshotSelected, row.ID)
		}
	}
}

func TestPlanExpanderStableIDsAndShardCoverage(t *testing.T) {
	path := filepath.Join("configs", "ultimate.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	cfg, err := ParsePlanConfig(raw)
	require.NoError(t, err)

	a, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{Seed: 20260727})
	require.NoError(t, err)
	b, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{Seed: 20260727})
	require.NoError(t, err)

	aIDs := make([]string, 0, len(a.Rows))
	bIDs := make([]string, 0, len(b.Rows))
	for _, r := range a.Rows {
		aIDs = append(aIDs, r.ID)
	}
	for _, r := range b.Rows {
		bIDs = append(bIDs, r.ID)
	}
	assert.Equal(t, aIDs, bIDs)

	union := map[string]struct{}{}
	for shard := 0; shard < 4; shard++ {
		planShard, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{Seed: cfg.Seed, ShardIndex: shard, ShardTotal: 4})
		require.NoError(t, err)
		for _, row := range planShard.Rows {
			_, exists := union[row.ID]
			require.False(t, exists, "row %s appears in multiple shards", row.ID)
			union[row.ID] = struct{}{}
		}
	}
	assert.Len(t, union, len(a.Rows))
}

func TestPlanExpanderCampaignFilterAndFullFactorialGuardrails(t *testing.T) {
	path := filepath.Join("configs", "smoke.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	cfg, err := ParsePlanConfig(raw)
	require.NoError(t, err)

	plan, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{Seed: cfg.Seed, CampaignFilter: []string{"direct-startup"}})
	require.NoError(t, err)
	for _, r := range plan.Rows {
		assert.Equal(t, "direct-startup", r.Campaign)
	}

	_, err = ExpandPlanFromConfig(cfg, PlanExpandOptions{FullFactorial: true, AllowLargePlan: false, Seed: cfg.Seed})
	require.Error(t, err)
	_, err = ExpandPlanFromConfig(cfg, PlanExpandOptions{FullFactorial: true, AllowLargePlan: true, MaxRows: 1, Seed: cfg.Seed})
	require.Error(t, err)
}

func TestPlanConfigMaxRowsAppliedForCampaignSubset(t *testing.T) {
	cfg := &PlanConfig{
		Schema:         PlanConfigSchema,
		Seed:           2026,
		MaxRows:        10,
		AllowLargePlan: true,
		Campaigns: map[string]CampaignConfig{
			"capability": {Enabled: true},
		},
	}

	plan, err := ExpandPlanFromConfig(cfg, PlanExpandOptions{MaxRows: 4})
	require.NoError(t, err)
	assert.Equal(t, 4, len(plan.Rows))

	_, err = ExpandPlanFromConfig(cfg, PlanExpandOptions{MaxRows: 1, Seed: 2})
	require.Error(t, err)
}
