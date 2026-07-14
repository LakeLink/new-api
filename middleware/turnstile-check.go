package middleware

import (
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

type turnstileCheckResponse struct {
	Success bool `json:"success"`
}

var (
	turnstileVerifyURL  = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	turnstileHTTPClient = &http.Client{Timeout: 10 * time.Second}
)

func TurnstileCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.TurnstileCheckEnabled {
			response := c.Query("turnstile")
			if response == "" {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile token 为空",
				})
				c.Abort()
				return
			}
			rawRes, err := turnstileHTTPClient.PostForm(turnstileVerifyURL, url.Values{
				"secret":   {common.TurnstileSecretKey},
				"response": {response},
				"remoteip": {c.ClientIP()},
			})
			if err != nil {
				common.SysLog(err.Error())
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": err.Error(),
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
				common.SysLog(err.Error())
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": err.Error(),
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
