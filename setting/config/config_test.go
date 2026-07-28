package config

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testConfigWithMap struct {
	Modes map[string]string `json:"modes"`
	Exprs map[string]string `json:"exprs"`
	Name  string            `json:"name"`
}

func TestUpdateConfigFromMap_MapReplacement(t *testing.T) {
	cfg := &testConfigWithMap{
		Modes: map[string]string{
			"model-a": "tiered_expr",
			"model-b": "tiered_expr",
		},
		Exprs: map[string]string{
			"model-a": "p * 5 + c * 25",
			"model-b": "p * 10 + c * 50",
		},
		Name: "billing",
	}

	// Simulate removing model-a: new value only has model-b
	err := UpdateConfigFromMap(cfg, map[string]string{
		"modes": `{"model-b": "tiered_expr"}`,
		"exprs": `{"model-b": "p * 10 + c * 50"}`,
	})
	if err != nil {
		t.Fatalf("UpdateConfigFromMap failed: %v", err)
	}

	if _, ok := cfg.Modes["model-a"]; ok {
		t.Errorf("Modes still contains model-a after it was removed from the update; got %v", cfg.Modes)
	}
	if _, ok := cfg.Exprs["model-a"]; ok {
		t.Errorf("Exprs still contains model-a after it was removed from the update; got %v", cfg.Exprs)
	}

	if cfg.Modes["model-b"] != "tiered_expr" {
		t.Errorf("Modes[model-b] = %q, want %q", cfg.Modes["model-b"], "tiered_expr")
	}
	if cfg.Exprs["model-b"] != "p * 10 + c * 50" {
		t.Errorf("Exprs[model-b] = %q, want %q", cfg.Exprs["model-b"], "p * 10 + c * 50")
	}
}

func TestUpdateConfigFromMap_EmptyMapClearsAll(t *testing.T) {
	cfg := &testConfigWithMap{
		Modes: map[string]string{
			"model-a": "tiered_expr",
		},
		Exprs: map[string]string{
			"model-a": "p * 5 + c * 25",
		},
	}

	err := UpdateConfigFromMap(cfg, map[string]string{
		"modes": `{}`,
		"exprs": `{}`,
	})
	if err != nil {
		t.Fatalf("UpdateConfigFromMap failed: %v", err)
	}

	if len(cfg.Modes) != 0 {
		t.Errorf("Modes should be empty after updating with {}, got %v", cfg.Modes)
	}
	if len(cfg.Exprs) != 0 {
		t.Errorf("Exprs should be empty after updating with {}, got %v", cfg.Exprs)
	}
}

func TestUpdateConfigFromMap_ScalarFieldsUnchanged(t *testing.T) {
	cfg := &testConfigWithMap{
		Modes: map[string]string{"m": "v"},
		Name:  "old",
	}

	err := UpdateConfigFromMap(cfg, map[string]string{
		"name": "new",
	})
	if err != nil {
		t.Fatalf("UpdateConfigFromMap failed: %v", err)
	}

	if cfg.Name != "new" {
		t.Errorf("Name = %q, want %q", cfg.Name, "new")
	}
	// modes was not in configMap, should remain unchanged
	if cfg.Modes["m"] != "v" {
		t.Errorf("Modes should be unchanged, got %v", cfg.Modes)
	}
}

type concurrentSnapshotConfig struct {
	First  int            `json:"first"`
	Second int            `json:"second"`
	Labels map[string]int `json:"labels"`
}

type strictUpdateConfig struct {
	Count   int               `json:"count"`
	Limit   uint8             `json:"limit"`
	Ratio   float64           `json:"ratio"`
	Enabled bool              `json:"enabled"`
	Labels  map[string]string `json:"labels"`
	IDs     []int             `json:"ids,omitempty"`
}

type semanticTestConfig struct {
	Limit int `json:"limit"`
}

func (c semanticTestConfig) Validate() error {
	if c.Limit <= 0 {
		return fmt.Errorf("limit must be positive")
	}
	return nil
}

func TestUpdateConfigFromMapRejectsInvalidBatchAtomically(t *testing.T) {
	const name = "test_strict_atomic_update"
	source := &strictUpdateConfig{
		Count:   3,
		Limit:   8,
		Ratio:   1.5,
		Enabled: true,
		Labels:  map[string]string{"generation": "old"},
	}
	GlobalConfig.Register(name, source)
	before := Snapshot[strictUpdateConfig](name)

	err := UpdateConfigFromMap(source, map[string]string{
		"count":  "9",
		"labels": `{"generation":"new"}`,
		"limit":  "256",
	})

	require.Error(t, err)
	assert.Equal(t, 3, source.Count)
	assert.Equal(t, uint8(8), source.Limit)
	assert.Equal(t, map[string]string{"generation": "old"}, source.Labels)
	assert.Same(t, before, Snapshot[strictUpdateConfig](name))
}

func TestUpdateConfigFromMapRejectsMalformedAndNonFiniteValues(t *testing.T) {
	source := &strictUpdateConfig{}

	tests := []map[string]string{
		{"count": "1.5"},
		{"limit": "-1"},
		{"ratio": "NaN"},
		{"ratio": "+Inf"},
		{"enabled": "sometimes"},
		{"labels": "not-json"},
		{"unknown": "value"},
	}
	for _, update := range tests {
		require.Error(t, UpdateConfigFromMap(source, update), update)
	}
}

func TestConfigMapUsesCanonicalJSONTagName(t *testing.T) {
	source := &strictUpdateConfig{IDs: []int{1}}

	values, err := ConfigToMap(source)
	require.NoError(t, err)
	assert.Equal(t, "[1]", values["ids"])
	assert.NotContains(t, values, "ids,omitempty")

	require.NoError(t, UpdateConfigFromMap(source, map[string]string{"ids": "[2,3]"}))
	assert.Equal(t, []int{2, 3}, source.IDs)
}

func TestValidateConfigFromMapDoesNotMutateSource(t *testing.T) {
	source := &strictUpdateConfig{Count: 5}

	require.NoError(t, ValidateConfigFromMap(source, map[string]string{"count": "6"}))
	assert.Equal(t, 5, source.Count)
	require.Error(t, ValidateConfigFromMap(source, map[string]string{"count": "bad"}))
	assert.Equal(t, 5, source.Count)
}

func TestSemanticValidationRejectsUpdatesAndMutationsAtomically(t *testing.T) {
	const name = "test_semantic_atomic_update"
	source := &semanticTestConfig{Limit: 5}
	GlobalConfig.Register(name, source)
	before := Snapshot[semanticTestConfig](name)

	require.Error(t, UpdateConfigFromMap(source, map[string]string{"limit": "0"}))
	assert.Equal(t, 5, source.Limit)
	assert.Same(t, before, Snapshot[semanticTestConfig](name))

	require.Error(t, Mutate(source, func() { source.Limit = -1 }))
	assert.Equal(t, 5, source.Limit)
	assert.Same(t, before, Snapshot[semanticTestConfig](name))
}

func TestLoadFromDBKeepsLastGoodSemanticConfiguration(t *testing.T) {
	manager := NewConfigManager()
	source := &semanticTestConfig{Limit: 5}
	manager.Register("semantic", source)
	before := manager.configs["semantic"].snapshot.Load().(*semanticTestConfig)

	require.NoError(t, manager.LoadFromDB(map[string]string{"semantic.limit": "0"}))
	assert.Equal(t, 5, source.Limit)
	assert.Same(t, before, manager.configs["semantic"].snapshot.Load())
}

func TestRegisteredConfigPublishesCoherentSnapshotsConcurrently(t *testing.T) {
	const name = "test_concurrent_snapshot"
	source := &concurrentSnapshotConfig{
		Labels: map[string]int{"generation": 0},
	}
	GlobalConfig.Register(name, source)

	start := make(chan struct{})
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 1; i <= 1_000; i++ {
			value := "1"
			if i%2 == 0 {
				value = "2"
			}
			err := UpdateConfigFromMap(source, map[string]string{
				"first":  value,
				"second": value,
				"labels": fmt.Sprintf(`{"generation":%s}`, value),
			})
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
		}
	}()

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 4_000 {
				snapshot := Snapshot[concurrentSnapshotConfig](name)
				if snapshot.First != snapshot.Second || snapshot.Labels["generation"] != snapshot.First {
					select {
					case errCh <- fmt.Errorf("torn snapshot: %+v", snapshot):
					default:
					}
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

func TestRegisteredConfigReplacesSnapshotsWithoutMutatingPriorValues(t *testing.T) {
	const name = "test_snapshot_replacement"
	source := &concurrentSnapshotConfig{
		First:  1,
		Second: 1,
		Labels: map[string]int{"generation": 1},
	}
	GlobalConfig.Register(name, source)
	before := Snapshot[concurrentSnapshotConfig](name)

	require.NoError(t, UpdateConfigFromMap(source, map[string]string{
		"first":  "2",
		"second": "2",
		"labels": `{"generation":2}`,
	}))

	after := Snapshot[concurrentSnapshotConfig](name)
	assert.NotSame(t, before, after)
	assert.Equal(t, 1, before.First)
	assert.Equal(t, map[string]int{"generation": 1}, before.Labels)
	assert.Equal(t, 2, after.First)
	assert.Equal(t, map[string]int{"generation": 2}, after.Labels)
}
