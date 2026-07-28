package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type telegramAuthRequest struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	PhotoURL  string `json:"photo_url"`
	AuthDate  int64  `json:"auth_date"`
	Hash      string `json:"hash"`
	Lang      string `json:"lang"`
}

func decodeTelegramAuthorization(body io.Reader) (map[string][]string, error) {
	var request telegramAuthRequest
	if err := common.DecodeJson(body, &request); err != nil {
		return nil, errors.New("invalid Telegram authorization body")
	}
	if request.ID <= 0 || request.AuthDate <= 0 {
		return nil, errors.New("invalid Telegram authorization body")
	}
	params := map[string][]string{
		"id":        {strconv.FormatInt(request.ID, 10)},
		"auth_date": {strconv.FormatInt(request.AuthDate, 10)},
		"hash":      {request.Hash},
	}
	for key, value := range map[string]string{
		"first_name": request.FirstName,
		"last_name":  request.LastName,
		"username":   request.Username,
		"photo_url":  request.PhotoURL,
		"lang":       request.Lang,
	} {
		if value != "" {
			params[key] = []string{value}
		}
	}
	return params, nil
}

func TelegramBind(c *gin.Context) {
	if !common.GetLegacyOptionBool("TelegramOAuthEnabled", &common.TelegramOAuthEnabled) {
		c.JSON(200, gin.H{
			"message": "管理员未开启通过 Telegram 登录以及注册",
			"success": false,
		})
		return
	}
	params, err := decodeTelegramAuthorization(c.Request.Body)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !checkTelegramAuthorization(params, common.GetLegacyOptionString("TelegramBotToken", &common.TelegramBotToken)) {
		c.JSON(200, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}
	telegramID := params["id"][0]
	taken, err := model.IsTelegramIdAlreadyTaken(telegramID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if taken {
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
	claimed, err := claimTelegramAuthorization(params)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if !claimed {
		c.JSON(http.StatusOK, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return model.BindBuiltInOAuthIdentityWithTx(
			tx,
			model.BuiltInOAuthProviderTelegram,
			telegramID,
			user.Id,
		)
	}); err != nil {
		c.JSON(200, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
	})
}

func TelegramLogin(c *gin.Context) {
	if !common.GetLegacyOptionBool("TelegramOAuthEnabled", &common.TelegramOAuthEnabled) {
		c.JSON(200, gin.H{
			"message": "管理员未开启通过 Telegram 登录以及注册",
			"success": false,
		})
		return
	}
	params, err := decodeTelegramAuthorization(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}
	if !checkTelegramAuthorization(params, common.GetLegacyOptionString("TelegramBotToken", &common.TelegramBotToken)) {
		c.JSON(200, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}

	telegramID := params["id"][0]
	user := model.User{TelegramId: telegramID}
	if err := user.FillUserByTelegramId(); err != nil {
		c.JSON(200, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	if user.Id == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message": "用户已注销",
			"success": false,
		})
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "用户已被封禁",
			"success": false,
		})
		return
	}
	claimed, err := claimTelegramAuthorization(params)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if !claimed {
		c.JSON(http.StatusOK, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}
	setupLogin(&user, c)
}

func claimTelegramAuthorization(params map[string][]string) (bool, error) {
	authDate, err := strconv.ParseInt(params["auth_date"][0], 10, 64)
	if err != nil {
		return false, err
	}
	const assertionTTL = 5 * time.Minute
	return model.ClaimAuthenticationToken(
		"telegram",
		params["hash"][0],
		time.Unix(authDate, 0).Add(assertionTTL).UnixMilli(),
		time.Now().UnixMilli(),
	)
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
	// Telegram login data is a short-lived authentication assertion. Reject
	// stale assertions and clocks implausibly in the future.
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
