package common

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInMemoryRateLimiterConcurrentInitializationAndRequest(t *testing.T) {
	var limiter InMemoryRateLimiter
	var initializers sync.WaitGroup
	for range 32 {
		initializers.Add(1)
		go func() {
			defer initializers.Done()
			limiter.Init(0)
		}()
	}
	initializers.Wait()

	start := make(chan struct{})
	var requests sync.WaitGroup
	var allowedMu sync.Mutex
	allowed := 0
	for range 32 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			if limiter.Request("same-key", 1, 60) {
				allowedMu.Lock()
				allowed++
				allowedMu.Unlock()
			}
		}()
	}
	close(start)
	requests.Wait()

	assert.Equal(t, 1, allowed)
}
