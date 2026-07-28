package controller

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type Setup struct {
	Status       bool   `json:"status"`
	RootInit     bool   `json:"root_init"`
	DatabaseType string `json:"database_type"`
}

type SetupRequest struct {
	Username           string `json:"username"`
	Password           string `json:"password"`
	ConfirmPassword    string `json:"confirmPassword"`
	SelfUseModeEnabled bool   `json:"SelfUseModeEnabled"`
	DemoSiteEnabled    bool   `json:"DemoSiteEnabled"`
}

func GetSetup(c *gin.Context) {
	setup := Setup{
		Status: constant.Setup.Load(),
	}
	if setup.Status {
		c.JSON(200, gin.H{
			"success": true,
			"data":    setup,
		})
		return
	}
	setupRecord, err := model.GetSetup()
	if err != nil {
		common.SysLog("GetSetup failed to read setup marker: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	if setupRecord != nil {
		constant.Setup.Store(true)
		setup.Status = true
		c.JSON(200, gin.H{"success": true, "data": setup})
		return
	}
	setup.RootInit, err = model.RootUserExists()
	if err != nil {
		common.SysLog("GetSetup failed to check root user: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}
	setup.DatabaseType = string(common.MainDatabaseType())
	c.JSON(200, gin.H{
		"success": true,
		"data":    setup,
	})
}

func PostSetup(c *gin.Context) {
	// Check if setup is already completed
	if constant.Setup.Load() {
		c.JSON(200, gin.H{
			"success": false,
			"message": "系统已经初始化完成",
		})
		return
	}

	// Check if root user already exists
	rootExists, err := model.RootUserExists()
	if err != nil {
		common.SysLog("PostSetup failed to check root user: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}

	var req SetupRequest
	err = c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": "请求参数有误",
		})
		return
	}

	// If root doesn't exist, validate and create admin account
	var rootUser *model.User
	if !rootExists {
		req.Username = strings.TrimSpace(req.Username)
		if req.Username == "" {
			c.JSON(200, gin.H{
				"success": false,
				"message": "用户名不能为空",
			})
			return
		}
		// Validate username length: max 12 characters to align with model.User validation
		if len(req.Username) > 12 {
			c.JSON(200, gin.H{
				"success": false,
				"message": "用户名长度不能超过12个字符",
			})
			return
		}
		// Validate password
		if req.Password != req.ConfirmPassword {
			c.JSON(200, gin.H{
				"success": false,
				"message": "两次输入的密码不一致",
			})
			return
		}

		if len(req.Password) < 8 {
			c.JSON(200, gin.H{
				"success": false,
				"message": "密码长度至少为8个字符",
			})
			return
		}

		// Create root user
		hashedPassword, err := common.Password2Hash(req.Password)
		if err != nil {
			c.JSON(200, gin.H{
				"success": false,
				"message": "系统错误: " + err.Error(),
			})
			return
		}
		rootUser = &model.User{
			Username:    req.Username,
			Password:    hashedPassword,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
			AccessToken: nil,
			Quota:       100000000,
		}
	}

	err = model.InitializeSetup(rootUser, req.SelfUseModeEnabled, req.DemoSiteEnabled)
	if err != nil {
		if errors.Is(err, model.ErrSetupAlreadyCompleted) {
			constant.Setup.Store(true)
			c.JSON(200, gin.H{
				"success": false,
				"message": "系统已经初始化完成",
			})
			return
		}
		common.SysLog("PostSetup failed to initialize system: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		return
	}

	constant.Setup.Store(true)

	c.JSON(200, gin.H{
		"success": true,
		"message": "系统初始化成功",
	})
}
