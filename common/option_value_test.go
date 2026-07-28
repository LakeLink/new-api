package common

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeOptionAccessorsReadUnderOptionLock(t *testing.T) {
	OptionMapRWMutex.Lock()
	original := OptionMap
	OptionMap = map[string]string{
		"enabled": "true",
		"count":   "42",
		"ratio":   "1.25",
	}
	OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		OptionMapRWMutex.Lock()
		OptionMap = original
		OptionMapRWMutex.Unlock()
	})

	assert.True(t, GetOptionBool("enabled", false))
	assert.Equal(t, 42, GetOptionInt("count", 0))
	assert.Equal(t, 1.25, GetOptionFloat64("ratio", 0))
	assert.Equal(t, "fallback", GetOptionString("missing", "fallback"))
}

func TestRuntimeOptionAccessorsAreRaceSafeDuringUpdates(t *testing.T) {
	OptionMapRWMutex.Lock()
	original := OptionMap
	OptionMap = map[string]string{"toggle": "false"}
	OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		OptionMapRWMutex.Lock()
		OptionMap = original
		OptionMapRWMutex.Unlock()
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2_000; i++ {
			OptionMapRWMutex.Lock()
			OptionMap["toggle"] = strconv.FormatBool(i%2 == 0)
			OptionMapRWMutex.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range 4_000 {
			_ = GetOptionBool("toggle", false)
		}
	}()
	close(start)
	wg.Wait()
}

func TestLegacyRuntimeOptionFallbackIsReadUnderOptionLock(t *testing.T) {
	legacyBool := true
	legacyString := "legacy"
	legacyInt := 7
	legacyFloat := 1.5

	OptionMapRWMutex.Lock()
	original := OptionMap
	OptionMap = nil
	OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		OptionMapRWMutex.Lock()
		OptionMap = original
		OptionMapRWMutex.Unlock()
	})

	assert.True(t, GetLegacyOptionBool("bool", &legacyBool))
	assert.Equal(t, "legacy", GetLegacyOptionString("string", &legacyString))
	assert.Equal(t, 7, GetLegacyOptionInt("int", &legacyInt))
	assert.Equal(t, 1.5, GetLegacyOptionFloat64("float", &legacyFloat))
}
