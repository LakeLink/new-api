package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func TelegramBind(c *gin.Context) {
	if !common.TelegramOAuthEnabled {
		c.JSON(200, gin.H{
			"message": "管理员未开启通过 Telegram 登录以及注册",
			"success": false,
		})
		return
	}
	params := c.Request.URL.Query()
	if !checkTelegramAuthorization(params, common.TelegramBotToken) {
		c.JSON(200, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}
	telegramId := params["id"][0]
	if model.IsTelegramIdAlreadyTaken(telegramId) {
		c.JSON(200, gin.H{
			"message": "该 Telegram 账户已被绑定",
			"success": false,
		})
		return
	}

	user, err := getCurrentSessionUser(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"message": "请先登录后再绑定 Telegram",
			"success": false,
		})
		return
	}
	if user.Id == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "用户已注销",
		})
		return
	}
	user.TelegramId = telegramId
	if err := user.Update(false); err != nil {
		c.JSON(200, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}

	c.Redirect(302, common.ThemeAwarePath("/console/personal"))
}

func TelegramLogin(c *gin.Context) {
	if !common.TelegramOAuthEnabled {
		c.JSON(200, gin.H{
			"message": "管理员未开启通过 Telegram 登录以及注册",
			"success": false,
		})
		return
	}
	params := c.Request.URL.Query()
	if !checkTelegramAuthorization(params, common.TelegramBotToken) {
		c.JSON(200, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}

	telegramId := params["id"][0]
	user := model.User{TelegramId: telegramId}
	if err := user.FillUserByTelegramId(); err != nil {
		c.JSON(200, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	setupLogin(&user, c)
}

func checkTelegramAuthorization(params map[string][]string, token string) bool {
	if token == "" || len(params["id"]) != 1 || len(params["auth_date"]) != 1 || len(params["hash"]) != 1 {
		return false
	}
	authDate, err := strconv.ParseInt(params["auth_date"][0], 10, 64)
	if err != nil {
		return false
	}
	now := time.Now().Unix()
	// Telegram login data is an authentication assertion, not a reusable account
	// credential. Reject replayed assertions and clocks implausibly in the future.
	if authDate > now+30 || now-authDate > int64((5*time.Minute)/time.Second) {
		return false
	}

	strs := make([]string, 0, len(params)-1)
	hash := params["hash"][0]
	for k, v := range params {
		if k == "hash" {
			continue
		}
		if len(v) != 1 {
			return false
		}
		strs = append(strs, k+"="+v[0])
	}
	sort.Strings(strs)
	var imploded = ""
	for _, s := range strs {
		if imploded != "" {
			imploded += "\n"
		}
		imploded += s
	}
	sha256hash := sha256.New()
	io.WriteString(sha256hash, token)
	hmachash := hmac.New(sha256.New, sha256hash.Sum(nil))
	io.WriteString(hmachash, imploded)
	expectedHash := hmachash.Sum(nil)
	providedHash, err := hex.DecodeString(hash)
	if err != nil || len(providedHash) != len(expectedHash) {
		return false
	}
	return hmac.Equal(providedHash, expectedHash)
}
