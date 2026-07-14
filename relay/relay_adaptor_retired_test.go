package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
)

func TestGetAdaptorDoesNotExposeRetiredProviders(t *testing.T) {
	assert.Nil(t, GetAdaptor(constant.APITypePaLM))
	assert.Nil(t, GetAdaptor(constant.APITypeTencent))
	assert.NotNil(t, GetAdaptor(constant.APITypeGemini))
}
