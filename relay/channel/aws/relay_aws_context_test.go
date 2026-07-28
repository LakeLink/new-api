package aws

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAWSInvokeContextPreservesParentCancellationAndBoundsInvalidTimeout(t *testing.T) {
	originalTimeout := common.RelayTimeout
	t.Cleanup(func() { common.RelayTimeout = originalTimeout })

	common.RelayTimeout = 0
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := newAwsInvokeContext(parent)
	cancelParent()
	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("AWS invoke context did not preserve parent cancellation")
	}
	cancel()

	common.RelayTimeout = -1
	ctx, cancel = newAwsInvokeContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Minute), deadline, time.Second)
}
