package middleware

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSuccessfulRequestReservationsReleaseFailedRequest(t *testing.T) {
	var reservations successfulRequestReservations
	const now int64 = 1_000

	assert.True(t, reservations.reserve("user", "request-1", 2, 60, now))
	assert.True(t, reservations.reserve("user", "request-2", 2, 60, now))
	assert.False(t, reservations.reserve("user", "request-3", 2, 60, now))

	reservations.release("user", "request-1")
	assert.True(t, reservations.reserve("user", "request-3", 2, 60, now))
}

func TestSuccessfulRequestReservationsExpireOutsideWindow(t *testing.T) {
	var reservations successfulRequestReservations

	assert.True(t, reservations.reserve("user", "request-1", 1, 60, 1_000))
	assert.False(t, reservations.reserve("user", "request-2", 1, 60, 1_059))
	assert.True(t, reservations.reserve("user", "request-2", 1, 60, 1_060))
}

func TestSuccessfulRequestReservationsEnforceConcurrentCap(t *testing.T) {
	var reservations successfulRequestReservations
	var allowed atomic.Int32
	var waitGroup sync.WaitGroup

	for index := 0; index < 12; index++ {
		waitGroup.Add(1)
		go func(requestID string) {
			defer waitGroup.Done()
			if reservations.reserve("user", requestID, 3, 60, 1_000) {
				allowed.Add(1)
			}
		}(fmt.Sprintf("request-%d", index))
	}
	waitGroup.Wait()

	assert.EqualValues(t, 3, allowed.Load())
}
