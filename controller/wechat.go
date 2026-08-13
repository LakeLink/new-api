package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type wechatLoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

const maxWeChatAuthResponseBytes int64 = 1 << 20

func getWeChatIdByCode(ctx context.Context, code string) (string, error) {
	if code == "" {
		return "", errors.New("无效的参数")
	}
	serverAddress := common.GetLegacyOptionString("WeChatServerAddress", &common.WeChatServerAddress)
	serverToken := common.GetLegacyOptionString("WeChatServerToken", &common.WeChatServerToken)
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/wechat/user?code=%s", serverAddress, url.QueryEscape(code)), nil)
	if err != nil {
		return "", errors.New("微信认证服务配置无效")
	}
	req.Header.Set("Authorization", serverToken)
	client := service.GetHttpClientWithTimeout(5 * time.Second)
	httpResponse, err := service.DoUpstreamRequest(client, req)
	if err != nil {
		return "", errors.New("微信认证服务暂时不可用")
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		return "", errors.New("微信认证服务暂时不可用")
	}
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxWeChatAuthResponseBytes+1))
	if err != nil || int64(len(body)) > maxWeChatAuthResponseBytes {
		return "", errors.New("微信认证服务返回了无效响应")
	}
	var res wechatLoginResponse
	if err := common.Unmarshal(body, &res); err != nil {
		return "", errors.New("微信认证服务返回了无效响应")
	}
	if !res.Success {
		return "", errors.New("验证码错误或已过期")
	}
	if res.Data == "" {
		return "", errors.New("验证码错误或已过期")
	}
	return res.Data, nil
}

func WeChatAuth(c *gin.Context) {
	if !common.GetLegacyOptionBool("WeChatAuthEnabled", &common.WeChatAuthEnabled) {
		c.JSON(http.StatusOK, gin.H{
			"message": "管理员未开启通过微信登录以及注册",
			"success": false,
		})
		return
	}
	var req wechatBindRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "无效的参数",
			"success": false,
		})
		return
	}
	wechatId, err := getWeChatIdByCode(c.Request.Context(), req.Code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	user := model.User{
		WeChatId: wechatId,
	}
	taken, err := model.IsWeChatIdAlreadyTaken(wechatId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if taken {
		err := user.FillUserByWeChatId()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
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
	} else {
		if common.GetLegacyOptionBool("RegisterEnabled", &common.RegisterEnabled) {
			user.Username = newOAuthUsername("wechat_")
			user.DisplayName = "WeChat User"
			user.Role = common.RoleCommonUser
			user.Status = common.UserStatusEnabled

			err := model.DB.Transaction(func(tx *gorm.DB) error {
				if err := user.InsertWithTx(tx, 0); err != nil {
					return err
				}
				return model.BindBuiltInOAuthIdentityWithTx(
					tx,
					model.BuiltInOAuthProviderWeChat,
					wechatId,
					user.Id,
				)
			})
			if err != nil {
				var conflict *model.BuiltInOAuthIdentityConflictError
				if errors.As(err, &conflict) {
					winner := model.User{WeChatId: wechatId}
					if fillErr := winner.FillUserByWeChatId(); fillErr != nil {
						common.ApiError(c, fillErr)
						return
					}
					if winner.Id == 0 {
						c.JSON(http.StatusOK, gin.H{
							"success": false,
							"message": "用户已注销",
						})
						return
					}
					user = winner
				} else {
					c.JSON(http.StatusOK, gin.H{
						"success": false,
						"message": err.Error(),
					})
					return
				}
			} else {
				user.FinalizeOAuthUserCreation(0)
			}
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "管理员关闭了新用户注册",
			})
			return
		}
	}

	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "用户已被封禁",
			"success": false,
		})
		return
	}
	setupLogin(&user, c)
}

type wechatBindRequest struct {
	Code string `json:"code"`
}

func WeChatBind(c *gin.Context) {
	if !common.GetLegacyOptionBool("WeChatAuthEnabled", &common.WeChatAuthEnabled) {
		c.JSON(http.StatusOK, gin.H{
			"message": "管理员未开启通过微信登录以及注册",
			"success": false,
		})
		return
	}
	var req wechatBindRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的请求",
		})
		return
	}
	code := req.Code
	wechatId, err := getWeChatIdByCode(c.Request.Context(), code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	taken, err := model.IsWeChatIdAlreadyTaken(wechatId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if taken {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该微信账号已被绑定",
		})
		return
	}

	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "请先登录"})
		return
	}
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user == nil || user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "请先登录"})
		return
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		return model.BindBuiltInOAuthIdentityWithTx(
			tx,
			model.BuiltInOAuthProviderWeChat,
			wechatId,
			user.Id,
		)
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}
