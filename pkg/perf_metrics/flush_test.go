package perfmetrics

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOldPerfBucketRetainsSampleRecordedAfterDrain(t *testing.T) {
	key := bucketKey{
		model:    "late-sample-model",
		group:    "default",
		bucketTs: bucketStart(time.Now().Add(-25 * time.Hour).Unix()),
	}
	bucket := &atomicBucket{}
	hotBuckets.Store(key, bucket)
	t.Cleanup(func() {
		hotBuckets.Delete(key)
	})

	assert.Zero(t, bucket.drain().requestCount)
	addHotBucketSample(key, Sample{
		Model:        key.model,
		Group:        key.group,
		LatencyMs:    125,
		Success:      true,
		OutputTokens: 8,
		GenerationMs: 250,
	})
	deleteOldEmptyBucket(key, key, bucket)

	value, ok := hotBuckets.Load(key)
	require.True(t, ok, "a late sample must prevent deletion of its bucket")
	snapshot := value.(*atomicBucket).snapshot()
	assert.EqualValues(t, 1, snapshot.requestCount)
	assert.EqualValues(t, 1, snapshot.successCount)
	assert.EqualValues(t, 125, snapshot.totalLatencyMs)
	assert.EqualValues(t, 8, snapshot.outputTokens)
	assert.EqualValues(t, 250, snapshot.generationMs)
}

func TestMetricCountersSaturateWithoutBecomingNegative(t *testing.T) {
	current := counters{
		requestCount:   math.MaxInt64 - 1,
		successCount:   math.MaxInt64 - 1,
		totalLatencyMs: math.MaxInt64 - 1,
		ttftCount:      math.MaxInt64 - 1,
	}
	updated := current.add(counters{
		requestCount:   10,
		successCount:   10,
		totalLatencyMs: 10,
		ttftCount:      10,
		outputTokens:   -1,
	})

	assert.Equal(t, int64(math.MaxInt64), updated.requestCount)
	assert.Equal(t, int64(math.MaxInt64), updated.successCount)
	assert.Equal(t, int64(math.MaxInt64), updated.totalLatencyMs)
	assert.Equal(t, int64(math.MaxInt64), updated.ttftCount)
	assert.Zero(t, updated.outputTokens)
}
