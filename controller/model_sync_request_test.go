package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncUpstreamModelsRejectsMalformedNonEmptyBodyBeforeSync(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"locale":`,
		`{"locale":"en"}{"overwrite":[]}`,
		`{"locale":"en"} trailing`,
	} {
		t.Run(body, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(
				http.MethodPost,
				"/api/models/sync_upstream",
				strings.NewReader(body),
			)
			c.Request.Header.Set("Content-Type", "application/json")

			require.NotPanics(t, func() {
				SyncUpstreamModels(c)
			})

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.JSONEq(t, `{"success":false,"message":"Invalid request"}`, recorder.Body.String())
		})
	}
}

func TestDecodeUpstreamEnvelopeRejectsFailureAndMalformedShapes(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}

	t.Run("envelope", func(t *testing.T) {
		var result upstreamEnvelope[item]
		require.NoError(t, decodeUpstreamEnvelope(
			[]byte(`{"success":true,"data":[{"name":"gpt"}]}`),
			&result,
		))
		assert.True(t, result.Success)
		assert.Equal(t, []item{{Name: "gpt"}}, result.Data)
	})

	t.Run("bare array", func(t *testing.T) {
		var result upstreamEnvelope[item]
		require.NoError(t, decodeUpstreamEnvelope(
			[]byte(`[{"name":"claude"}]`),
			&result,
		))
		assert.True(t, result.Success)
		assert.Equal(t, []item{{Name: "claude"}}, result.Data)
	})

	for name, body := range map[string]string{
		"explicit failure": `{"success":false,"message":"failed","data":[]}`,
		"unknown object":   `{"unexpected":true}`,
		"malformed json":   `{"success":`,
		"null array":       `null`,
	} {
		t.Run(name, func(t *testing.T) {
			original := upstreamEnvelope[item]{
				Success: true,
				Data:    []item{{Name: "last-good"}},
			}
			result := original
			require.Error(t, decodeUpstreamEnvelope([]byte(body), &result))
			assert.Equal(t, original, result)
		})
	}
}
