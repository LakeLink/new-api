package controller

import (
	"bytes"
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// deferredTaskResponseWriter keeps an accepted upstream task response private
// until the task row and billing outbox have committed. Async task adaptors
// write ordinary non-stream JSON responses, so buffering closes the window
// where a client could receive a task ID that the gateway failed to persist.
type deferredTaskResponseWriter struct {
	gin.ResponseWriter
	header    http.Header
	body      bytes.Buffer
	status    int
	size      int
	statusSet bool
	written   bool
}

func newDeferredTaskResponseWriter(writer gin.ResponseWriter) *deferredTaskResponseWriter {
	deferred := &deferredTaskResponseWriter{
		ResponseWriter: writer,
	}
	deferred.Reset()
	return deferred
}

// Reset discards an uncommitted upstream attempt before a retry. Provider
// adaptors write headers and bodies directly to gin's writer even when they
// later return an error, so carrying this state into the next attempt could
// publish a failed provider's response alongside a successful task ID.
func (w *deferredTaskResponseWriter) Reset() {
	w.header = w.ResponseWriter.Header().Clone()
	w.body.Reset()
	w.status = http.StatusOK
	w.size = -1
	w.statusSet = false
	w.written = false
}

func (w *deferredTaskResponseWriter) Header() http.Header {
	return w.header
}

func (w *deferredTaskResponseWriter) WriteHeader(code int) {
	if code <= 0 || w.written {
		return
	}
	w.status = code
	w.statusSet = true
}

func (w *deferredTaskResponseWriter) WriteHeaderNow() {
	if w.written {
		return
	}
	w.written = true
	w.statusSet = true
	w.size = 0
}

func (w *deferredTaskResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	n, err := w.body.Write(data)
	w.size += n
	return n, err
}

func (w *deferredTaskResponseWriter) WriteString(data string) (int, error) {
	w.WriteHeaderNow()
	n, err := w.body.WriteString(data)
	w.size += n
	return n, err
}

func (w *deferredTaskResponseWriter) Flush() {
	w.WriteHeaderNow()
}

func (w *deferredTaskResponseWriter) Status() int {
	return w.status
}

func (w *deferredTaskResponseWriter) Size() int {
	return w.size
}

func (w *deferredTaskResponseWriter) Written() bool {
	return w.written
}

func (w *deferredTaskResponseWriter) Commit() error {
	targetHeader := w.ResponseWriter.Header()
	for key := range targetHeader {
		targetHeader.Del(key)
	}
	for key, values := range w.header {
		targetHeader[key] = append([]string(nil), values...)
	}
	if w.statusSet || w.written || w.body.Len() != 0 {
		w.ResponseWriter.WriteHeader(w.status)
	}
	if w.body.Len() == 0 {
		return nil
	}
	_, err := w.ResponseWriter.Write(w.body.Bytes())
	return err
}

func failAcceptedTaskSubmission(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	writer gin.ResponseWriter,
	err error,
	code string,
) {
	c.Writer = writer
	if info != nil && info.Billing != nil {
		info.Billing.Refund(c)
	}
	respondTaskError(c, service.TaskErrorWrapperLocal(err, code, http.StatusInternalServerError))
}

var _ gin.ResponseWriter = (*deferredTaskResponseWriter)(nil)
