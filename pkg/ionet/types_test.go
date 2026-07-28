package ionet

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerConfigPreservesExplicitZeroTrafficPort(t *testing.T) {
	var config ContainerConfig
	require.NoError(t, common.Unmarshal([]byte(`{"replica_count":1,"traffic_port":0}`), &config))

	require.NotNil(t, config.TrafficPort)
	assert.Zero(t, *config.TrafficPort)

	encoded, err := common.Marshal(config)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"traffic_port":0`)
}

func TestContainerConfigOmitsAbsentTrafficPort(t *testing.T) {
	config := ContainerConfig{ReplicaCount: 1}
	assert.Nil(t, config.TrafficPort)

	encoded, err := common.Marshal(config)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"traffic_port"`)
}
