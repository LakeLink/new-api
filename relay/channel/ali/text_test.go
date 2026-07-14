package ali

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestOpenAI2AliPreservesOmittedTopP(t *testing.T) {
	converted := requestOpenAI2Ali(dto.GeneralOpenAIRequest{Model: "qwen-plus"})

	require.NotNil(t, converted)
	assert.Nil(t, converted.TopP)
}

func TestRequestOpenAI2AliBoundsExplicitTopP(t *testing.T) {
	for _, test := range []struct {
		name string
		in   float64
		want float64
	}{
		{name: "zero", in: 0, want: 0.001},
		{name: "one", in: 1, want: 0.999},
		{name: "interior", in: 0.5, want: 0.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			converted := requestOpenAI2Ali(dto.GeneralOpenAIRequest{Model: "qwen-plus", TopP: &test.in})

			require.NotNil(t, converted.TopP)
			assert.Equal(t, test.want, *converted.TopP)
		})
	}
}
