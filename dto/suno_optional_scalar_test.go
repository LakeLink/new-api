package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSunoSubmitRequestPreservesExplicitZeroContinueAt(t *testing.T) {
	var request SunoSubmitReq
	require.NoError(t, common.Unmarshal([]byte(`{"continue_at":0}`), &request))

	require.NotNil(t, request.ContinueAt)
	assert.Zero(t, *request.ContinueAt)

	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, `{"continue_at":0,"make_instrumental":false}`, string(encoded))
}

func TestSunoSubmitRequestOmitsAbsentContinueAt(t *testing.T) {
	encoded, err := common.Marshal(SunoSubmitReq{})

	require.NoError(t, err)
	assert.JSONEq(t, `{"make_instrumental":false}`, string(encoded))
}
