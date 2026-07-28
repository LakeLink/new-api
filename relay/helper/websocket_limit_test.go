package helper

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebsocketMessageLimitBytesUsesSafeFallbackAndSaturates(t *testing.T) {
	assert.Equal(t, int64(128<<20), WebsocketMessageLimitBytes(0))
	assert.Equal(t, int64(2<<20), WebsocketMessageLimitBytes(2))
	assert.Equal(t, int64(math.MaxInt64), WebsocketMessageLimitBytes(math.MaxInt))
}
