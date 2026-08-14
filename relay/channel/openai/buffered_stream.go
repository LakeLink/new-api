package openai

import (
	"bufio"
	"context"
	"io"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"

	"github.com/gin-gonic/gin"
)

type bufferedScanResult struct {
	line string
	err  error
	done bool
}

func bufferedStreamContext(c *gin.Context, info *relaycommon.RelayInfo) context.Context {
	if info != nil && info.RelayCancelCtx != nil {
		return info.RelayCancelCtx
	}
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func bufferedStreamIdleTimeout() time.Duration {
	return common.SafeIntervalDuration(
		constant.StreamingTimeout,
		time.Second,
		helper.DefaultStreamingTimeout,
		"buffered stream timeout",
	)
}

func scanBufferedStream(ctx context.Context, reader io.Reader) <-chan bufferedScanResult {
	scanner := helper.NewStreamScanner(reader)
	scanner.Split(bufio.ScanLines)
	results := make(chan bufferedScanResult, 1)

	go func() {
		for scanner.Scan() {
			select {
			case results <- bufferedScanResult{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case results <- bufferedScanResult{err: scanner.Err(), done: true}:
		case <-ctx.Done():
		}
	}()

	return results
}
