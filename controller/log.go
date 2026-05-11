package controller

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	expr := c.Query("expr")
	logs, total, err := model.GetAllLogsWithOptions(model.LogQueryOptions{
		LogType:            logType,
		StartTimestamp:     startTimestamp,
		EndTimestamp:       endTimestamp,
		ModelName:          modelName,
		Username:           username,
		TokenName:          tokenName,
		StartIdx:           pageInfo.GetStartIdx(),
		Num:                pageInfo.GetPageSize(),
		Channel:            channel,
		Group:              group,
		RequestId:          requestId,
		Expr:               expr,
		IncludeAdminFields: true,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func ExportAllLogs(c *gin.Context) {
	if !checkLogExportPermission(c) {
		return
	}
	opts := getLogQueryOptions(c)
	opts.IncludeAdminFields = true
	logs, err := model.ExportAllLogsWithOptions(opts)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	writeLogExport(c, logs)
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	group := c.Query("group")
	requestId := c.Query("request_id")
	expr := c.Query("expr")
	logs, total, err := model.GetUserLogsWithOptions(model.LogQueryOptions{
		UserId:         userId,
		LogType:        logType,
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ModelName:      modelName,
		TokenName:      tokenName,
		StartIdx:       pageInfo.GetStartIdx(),
		Num:            pageInfo.GetPageSize(),
		Group:          group,
		RequestId:      requestId,
		Expr:           expr,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func ExportUserLogs(c *gin.Context) {
	if !checkLogExportPermission(c) {
		return
	}
	opts := getLogQueryOptions(c)
	opts.UserId = c.GetInt("id")
	opts.Username = ""
	opts.Channel = 0
	logs, err := model.ExportUserLogsWithOptions(opts)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	writeLogExport(c, logs)
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		c.JSON(200, gin.H{
			"success": false,
			"message": "无效的令牌",
		})
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	expr := c.Query("expr")
	stat, err := model.SumUsedQuotaWithOptions(model.LogQueryOptions{
		LogType:            logType,
		StartTimestamp:     startTimestamp,
		EndTimestamp:       endTimestamp,
		ModelName:          modelName,
		Username:           username,
		TokenName:          tokenName,
		Channel:            channel,
		Group:              group,
		RequestId:          requestId,
		Expr:               expr,
		IncludeAdminFields: true,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	expr := c.Query("expr")
	quotaNum, err := model.SumUsedQuotaWithOptions(model.LogQueryOptions{
		LogType:        logType,
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ModelName:      modelName,
		Username:       username,
		TokenName:      tokenName,
		Channel:        channel,
		Group:          group,
		RequestId:      requestId,
		Expr:           expr,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum.Quota,
			"rpm":   quotaNum.Rpm,
			"tpm":   quotaNum.Tpm,
			//"token": tokenNum,
		},
	})
	return
}

func DeleteHistoryLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}
	count, err := model.DeleteOldLog(c.Request.Context(), targetTimestamp, 100)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
	return
}

func getLogQueryOptions(c *gin.Context) model.LogQueryOptions {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	channel, _ := strconv.Atoi(c.Query("channel"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > model.LogExportLimit {
		limit = model.LogExportLimit
	}
	return model.LogQueryOptions{
		LogType:        logType,
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ModelName:      c.Query("model_name"),
		Username:       c.Query("username"),
		TokenName:      c.Query("token_name"),
		Channel:        channel,
		Group:          c.Query("group"),
		RequestId:      c.Query("request_id"),
		Expr:           c.Query("expr"),
		Num:            limit,
	}
}

func checkLogExportPermission(c *gin.Context) bool {
	if c.GetInt("role") < common.LogExportPermission {
		common.ApiError(c, errors.New("无权导出日志"))
		return false
	}
	return true
}

func writeLogExport(c *gin.Context, logs []*model.Log) {
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "jsonl")))
	var (
		data        []byte
		contentType string
		extension   string
		err         error
	)

	switch format {
	case "jsonl", "ndjson":
		data, err = marshalLogsJSONL(logs)
		contentType = "application/x-ndjson; charset=utf-8"
		extension = "jsonl"
	case "json":
		data, err = common.Marshal(logs)
		contentType = "application/json; charset=utf-8"
		extension = "json"
	case "csv":
		data, err = marshalLogsCSV(logs)
		contentType = "text/csv; charset=utf-8"
		extension = "csv"
	default:
		common.ApiError(c, errors.New("unsupported export format"))
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="call-logs-%s.%s"`, time.Now().UTC().Format("20060102-150405"), extension))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, data)
}

func marshalLogsJSONL(logs []*model.Log) ([]byte, error) {
	var buf bytes.Buffer
	for _, log := range logs {
		line, err := common.Marshal(log)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func marshalLogsCSV(logs []*model.Log) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&buf)
	if err := writer.Write([]string{
		"id",
		"user_id",
		"created_at",
		"type",
		"username",
		"token_name",
		"model_name",
		"quota",
		"prompt_tokens",
		"completion_tokens",
		"use_time",
		"is_stream",
		"channel",
		"channel_name",
		"token_id",
		"group",
		"ip",
		"request_id",
		"content",
		"other",
	}); err != nil {
		return nil, err
	}
	for _, log := range logs {
		if err := writer.Write([]string{
			strconv.Itoa(log.Id),
			strconv.Itoa(log.UserId),
			strconv.FormatInt(log.CreatedAt, 10),
			strconv.Itoa(log.Type),
			csvSafeCell(log.Username),
			csvSafeCell(log.TokenName),
			csvSafeCell(log.ModelName),
			strconv.Itoa(log.Quota),
			strconv.Itoa(log.PromptTokens),
			strconv.Itoa(log.CompletionTokens),
			strconv.Itoa(log.UseTime),
			strconv.FormatBool(log.IsStream),
			strconv.Itoa(log.ChannelId),
			csvSafeCell(log.ChannelName),
			strconv.Itoa(log.TokenId),
			csvSafeCell(log.Group),
			csvSafeCell(log.Ip),
			csvSafeCell(log.RequestId),
			csvSafeCell(log.Content),
			csvSafeCell(log.Other),
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func csvSafeCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r', '\n':
		return "'" + value
	default:
		return value
	}
}
