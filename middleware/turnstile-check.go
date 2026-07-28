package middleware

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type turnstileCheckResponse struct {
	Success bool `json:"success"`
}

var (
	turnstileVerifyURL  = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	turnstileHTTPClient = service.GetHttpClientWithTimeout(10 * time.Second)
)

func TurnstileCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.GetLegacyOptionBool("TurnstileCheckEnabled", &common.TurnstileCheckEnabled) {
			response := c.Query("turnstile")
			if response == "" {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile token 为空",
				})
				c.Abort()
				return
			}
			form := url.Values{
				"secret":   {common.GetLegacyOptionString("TurnstileSecretKey", &common.TurnstileSecretKey)},
				"response": {response},
				"remoteip": {c.ClientIP()},
			}
			request, err := http.NewRequestWithContext(
				c.Request.Context(),
				http.MethodPost,
				turnstileVerifyURL,
				strings.NewReader(form.Encode()),
			)
			if err == nil {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			var rawRes *http.Response
			if err == nil {
				rawRes, err = service.DoUpstreamRequest(turnstileHTTPClient, request)
			}
			if err != nil {
				common.SysLog("Turnstile verification request failed: " + common.MaskSensitiveInfo(err.Error()))
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验服务异常，请稍后重试！",
				})
				c.Abort()
				return
			}
			defer rawRes.Body.Close()
			if rawRes.StatusCode < http.StatusOK || rawRes.StatusCode >= http.StatusMultipleChoices {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验服务异常，请稍后重试！",
				})
				c.Abort()
				return
			}
			var res turnstileCheckResponse
			err = common.DecodeJson(io.LimitReader(rawRes.Body, 64*1024), &res)
			if err != nil {
				common.SysLog("Turnstile verification response decode failed: " + err.Error())
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验服务异常，请稍后重试！",
				})
				c.Abort()
				return
			}
			if !res.Success {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验失败，请刷新重试！",
				})
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
