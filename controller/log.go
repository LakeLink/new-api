package controller

import (
	"bufio"
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
	upstreamRequestId := c.Query("upstream_request_id")
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
		UpstreamRequestId:  upstreamRequestId,
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
	streamLogExport(c, opts, true)
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
	upstreamRequestId := c.Query("upstream_request_id")
	expr := c.Query("expr")
	logs, total, err := model.GetUserLogsWithOptions(model.LogQueryOptions{
		UserId:            userId,
		LogType:           logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         modelName,
		TokenName:         tokenName,
		StartIdx:          pageInfo.GetStartIdx(),
		Num:               pageInfo.GetPageSize(),
		Group:             group,
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Expr:              expr,
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
	streamLogExport(c, opts, false)
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
	upstreamRequestId := c.Query("upstream_request_id")
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
		UpstreamRequestId:  upstreamRequestId,
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
	upstreamRequestId := c.Query("upstream_request_id")
	expr := c.Query("expr")
	quotaNum, err := model.SumUsedQuotaWithOptions(model.LogQueryOptions{
		LogType:           logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         modelName,
		Username:          username,
		TokenName:         tokenName,
		Channel:           channel,
		Group:             group,
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Expr:              expr,
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

// DeleteHistoryLogs is the legacy synchronous log cleanup endpoint (DELETE /api/log/).
// It deletes directly instead of going through the async system task. It is kept only
// for the classic frontend; the default frontend uses POST /api/system-task/log-cleanup.
// TODO: remove this handler (and its route) once the classic frontend is removed.
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
	limitParam := strings.ToLower(strings.TrimSpace(c.Query("limit")))
	exportAll := limitParam == "all"
	limit, _ := strconv.Atoi(limitParam)
	if !exportAll && (limit <= 0 || limit > model.LogExportLimit) {
		limit = model.LogExportLimit
	}
	return model.LogQueryOptions{
		LogType:           logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         c.Query("model_name"),
		Username:          c.Query("username"),
		TokenName:         c.Query("token_name"),
		Channel:           channel,
		Group:             c.Query("group"),
		RequestId:         c.Query("request_id"),
		UpstreamRequestId: c.Query("upstream_request_id"),
		Expr:              c.Query("expr"),
		Num:               limit,
		NoLimit:           exportAll,
	}
}

func checkLogExportPermission(c *gin.Context) bool {
	if c.GetInt("role") < common.LogExportPermission {
		common.ApiError(c, errors.New("无权导出日志"))
		return false
	}
	return true
}

type logExportFormat struct {
	name        string
	contentType string
	extension   string
}

func getLogExportFormat(c *gin.Context) (logExportFormat, error) {
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "jsonl")))
	switch format {
	case "jsonl", "ndjson":
		return logExportFormat{
			name:        "jsonl",
			contentType: "application/x-ndjson; charset=utf-8",
			extension:   "jsonl",
		}, nil
	case "json":
		return logExportFormat{
			name:        "json",
			contentType: "application/json; charset=utf-8",
			extension:   "json",
		}, nil
	case "csv":
		return logExportFormat{
			name:        "csv",
			contentType: "text/csv; charset=utf-8",
			extension:   "csv",
		}, nil
	default:
		return logExportFormat{}, errors.New("unsupported export format")
	}
}

func streamLogExport(c *gin.Context, opts model.LogQueryOptions, isAdmin bool) {
	format, err := getLogExportFormat(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	bufferedWriter := bufio.NewWriterSize(c.Writer, 32*1024)
	var csvWriter *csv.Writer
	rowCount := 0
	jsonArrayStarted := false
	streamStarted := false

	onReady := func() error {
		streamStarted = true
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="call-logs-%s.%s"`, time.Now().UTC().Format("20060102-150405"), format.extension))
		c.Header("Content-Type", format.contentType)
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Status(http.StatusOK)

		switch format.name {
		case "json":
			jsonArrayStarted = true
			return bufferedWriter.WriteByte('[')
		case "csv":
			if _, err := bufferedWriter.WriteString("\xEF\xBB\xBF"); err != nil {
				return err
			}
			csvWriter = csv.NewWriter(bufferedWriter)
			return writeLogCSVHeader(csvWriter)
		default:
			return nil
		}
	}

	onRow := func(log *model.Log) error {
		rowCount++
		if err := writeLogExportRow(bufferedWriter, csvWriter, format.name, log, rowCount); err != nil {
			return err
		}
		if rowCount%1000 == 0 {
			return flushLogExport(bufferedWriter, csvWriter, c)
		}
		return nil
	}

	if isAdmin {
		err = model.StreamExportAllLogsWithOptions(opts, onReady, onRow)
	} else {
		err = model.StreamExportUserLogsWithOptions(opts, onReady, onRow)
	}
	if err != nil {
		if streamStarted || c.Writer.Written() {
			common.SysError("failed to stream log export: " + err.Error())
		} else {
			common.ApiError(c, err)
		}
		return
	}

	if format.name == "json" && jsonArrayStarted {
		if err = bufferedWriter.WriteByte(']'); err != nil {
			common.SysError("failed to finish log export: " + err.Error())
			return
		}
	}
	if err = flushLogExport(bufferedWriter, csvWriter, c); err != nil {
		common.SysError("failed to flush log export: " + err.Error())
	}
}

func writeLogExportRow(bufferedWriter *bufio.Writer, csvWriter *csv.Writer, format string, log *model.Log, rowCount int) error {
	switch format {
	case "jsonl":
		line, err := common.Marshal(log)
		if err != nil {
			return err
		}
		if _, err = bufferedWriter.Write(line); err != nil {
			return err
		}
		return bufferedWriter.WriteByte('\n')
	case "json":
		if rowCount > 1 {
			if err := bufferedWriter.WriteByte(','); err != nil {
				return err
			}
		}
		data, err := common.Marshal(log)
		if err != nil {
			return err
		}
		_, err = bufferedWriter.Write(data)
		return err
	case "csv":
		return writeLogCSVRow(csvWriter, log)
	default:
		return errors.New("unsupported export format")
	}
}

func flushLogExport(bufferedWriter *bufio.Writer, csvWriter *csv.Writer, c *gin.Context) error {
	if csvWriter != nil {
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			return err
		}
	}
	if err := bufferedWriter.Flush(); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

func writeLogCSVHeader(writer *csv.Writer) error {
	return writer.Write([]string{
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
	})
}

func writeLogCSVRow(writer *csv.Writer, log *model.Log) error {
	return writer.Write([]string{
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
	})
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
