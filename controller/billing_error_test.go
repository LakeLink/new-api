package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBillingEndpointsHandleMissingTokenContext(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	wasNil := common.OptionMap == nil
	if wasNil {
		common.OptionMap = make(map[string]string)
	}
	previous, hadPrevious := common.OptionMap["DisplayTokenStatEnabled"]
	common.OptionMap["DisplayTokenStatEnabled"] = "true"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadPrevious {
			common.OptionMap["DisplayTokenStatEnabled"] = previous
		} else {
			delete(common.OptionMap, "DisplayTokenStatEnabled")
		}
		if wasNil {
			common.OptionMap = nil
		}
		common.OptionMapRWMutex.Unlock()
	})

	for _, testCase := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{name: "subscription", handler: GetSubscription},
		{name: "usage", handler: GetUsage},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			assert.NotPanics(t, func() {
				testCase.handler(context)
			})
			assert.Contains(t, recorder.Body.String(), `"error"`)
		})
	}
}
