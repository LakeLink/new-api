package common

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingEventWriter struct{}

func (failingEventWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestCustomEventHandlesNonStringDataAndWriteErrors(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, encode(&output, CustomEvent{Data: 42}))
	assert.Equal(t, "42", output.String())

	output.Reset()
	require.NoError(t, encode(&output, CustomEvent{Data: "data: ready"}))
	assert.Equal(t, "data: ready\n\n", output.String())

	err := encode(failingEventWriter{}, CustomEvent{Data: "data: ready"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "write failed")
}
