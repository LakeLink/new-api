// 用于迁移检测的旧键，该文件下个版本会删除

package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// MigrateConsoleSetting 迁移旧的控制台相关配置到 console_setting.*
func MigrateConsoleSetting(c *gin.Context) {
	// 读取全部 option
	opts, err := model.AllOption()
	if err != nil {
		common.SysError("failed to get all options: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "获取配置失败，请稍后重试"})
		return
	}
	// 建立 map
	valMap := map[string]string{}
	present := map[string]bool{}
	for _, o := range opts {
		valMap[o.Key] = o.Value
		present[o.Key] = true
	}

	updates := make(map[string]string)
	deleteKeys := make([]string, 0, 5)

	// 处理 APIInfo
	if v, ok := valMap["ApiInfo"]; ok {
		deleteKeys = append(deleteKeys, "ApiInfo")
		if v != "" {
			var arr []map[string]interface{}
			if err := common.UnmarshalJsonStr(v, &arr); err != nil {
				common.ApiErrorMsg(c, "旧版 API 信息格式无效，请修正后重试")
				return
			}
			if len(arr) > 50 {
				arr = arr[:50]
			}
			bytes, err := common.Marshal(arr)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			updates["console_setting.api_info"] = string(bytes)
		}
	}

	// Announcements 直接搬
	if v, ok := valMap["Announcements"]; ok {
		deleteKeys = append(deleteKeys, "Announcements")
		if v != "" {
			updates["console_setting.announcements"] = v
		}
	}
	// FAQ 转换
	if v, ok := valMap["FAQ"]; ok {
		deleteKeys = append(deleteKeys, "FAQ")
		if v != "" {
			var arr []map[string]interface{}
			if err := common.UnmarshalJsonStr(v, &arr); err != nil {
				common.ApiErrorMsg(c, "旧版 FAQ 格式无效，请修正后重试")
				return
			}
			out := []map[string]interface{}{}
			for _, item := range arr {
				q, _ := item["question"].(string)
				if q == "" {
					q, _ = item["title"].(string)
				}
				a, _ := item["answer"].(string)
				if a == "" {
					a, _ = item["content"].(string)
				}
				if q != "" && a != "" {
					out = append(out, map[string]interface{}{"question": q, "answer": a})
				}
			}
			if len(out) > 50 {
				out = out[:50]
			}
			bytes, err := common.Marshal(out)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			updates["console_setting.faq"] = string(bytes)
		}
	}

	// Uptime Kuma 迁移到新的 groups 结构（console_setting.uptime_kuma_groups）
	url := valMap["UptimeKumaUrl"]
	slug := valMap["UptimeKumaSlug"]
	if (url == "") != (slug == "") {
		common.ApiErrorMsg(c, "旧版 Uptime Kuma 配置不完整，请同时设置 URL 和 Slug 后重试")
		return
	}
	if url != "" && slug != "" {
		// 仅当同时存在 URL 与 Slug 时才进行迁移
		groups := []map[string]interface{}{
			{
				"id":           1,
				"categoryName": "old",
				"url":          url,
				"slug":         slug,
				"description":  "",
			},
		}
		bytes, err := common.Marshal(groups)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		updates["console_setting.uptime_kuma_groups"] = string(bytes)
	}
	if present["UptimeKumaUrl"] {
		deleteKeys = append(deleteKeys, "UptimeKumaUrl")
	}
	if present["UptimeKumaSlug"] {
		deleteKeys = append(deleteKeys, "UptimeKumaSlug")
	}

	if err := model.UpdateOptionsBulk(updates); err != nil {
		common.ApiError(c, err)
		return
	}
	if len(deleteKeys) > 0 {
		if err := model.DB.Where("key IN ?", deleteKeys).Delete(&model.Option{}).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}

	// 重新加载 OptionMap
	model.InitOptionMap()
	common.SysLog("console setting migrated")
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "migrated"})
}
