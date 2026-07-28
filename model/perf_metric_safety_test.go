package model

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertPerfMetricSaturatesPersistedCounters(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &PerfMetric{})
	base := &PerfMetric{
		ModelName:      "saturation-model",
		Group:          "default",
		BucketTs:       3_600,
		RequestCount:   math.MaxInt64 - 2,
		SuccessCount:   math.MaxInt64 - 2,
		TotalLatencyMs: math.MaxInt64 - 2,
		TtftSumMs:      math.MaxInt64 - 2,
		TtftCount:      math.MaxInt64 - 2,
		OutputTokens:   math.MaxInt64 - 2,
		GenerationMs:   math.MaxInt64 - 2,
	}
	require.NoError(t, db.Create(base).Error)

	require.NoError(t, UpsertPerfMetric(&PerfMetric{
		ModelName:      base.ModelName,
		Group:          base.Group,
		BucketTs:       base.BucketTs,
		RequestCount:   10,
		SuccessCount:   10,
		TotalLatencyMs: 10,
		TtftSumMs:      10,
		TtftCount:      10,
		OutputTokens:   10,
		GenerationMs:   10,
	}))

	var persisted PerfMetric
	require.NoError(t, db.Where("identity_hash = ?", base.IdentityHash).First(&persisted).Error)
	assert.Equal(t, int64(math.MaxInt64), persisted.RequestCount)
	assert.Equal(t, int64(math.MaxInt64), persisted.SuccessCount)
	assert.Equal(t, int64(math.MaxInt64), persisted.TotalLatencyMs)
	assert.Equal(t, int64(math.MaxInt64), persisted.TtftSumMs)
	assert.Equal(t, int64(math.MaxInt64), persisted.TtftCount)
	assert.Equal(t, int64(math.MaxInt64), persisted.OutputTokens)
	assert.Equal(t, int64(math.MaxInt64), persisted.GenerationMs)
}

func TestUpsertPerfMetricRejectsInvalidCounters(t *testing.T) {
	setupBillingAdjustmentTestDB(t, &PerfMetric{})

	err := UpsertPerfMetric(&PerfMetric{
		ModelName:    "invalid-model",
		Group:        "default",
		BucketTs:     3_600,
		RequestCount: 1,
		OutputTokens: -1,
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot be negative")
}

func TestPerfMetricStartTimeRejectsDurationOverflow(t *testing.T) {
	now := time.Now().Unix()
	start := PerfMetricStartTime(int(^uint(0) >> 1))

	assert.InDelta(t, now-int64(24*time.Hour/time.Second), start, 2)
}
