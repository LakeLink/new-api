package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"gorm.io/gorm"
)

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if strings.Contains(value, "%") {
		condition, pattern, err := buildLogLikeCondition(column, value)
		if err != nil {
			return nil, err
		}
		return tx.Where(condition, pattern), nil
	}
	return tx.Where(column+" = ?", value), nil
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern, err := sanitizeClickHouseLikePattern(value)
		if err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}

	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func sanitizeClickHouseLikePattern(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `_`, `\_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

type Log struct {
	Id                int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2;index:idx_user_created_at_id,priority:3;index:idx_type_created_at_id,priority:3"`
	UserId            int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1;index:idx_user_created_at_id,priority:1"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type;index:idx_user_created_at_id,priority:2;index:idx_type_created_at_id,priority:2"`
	Type              int    `json:"type" gorm:"index:idx_created_at_type;index:idx_type_created_at_id,priority:1"`
	Content           string `json:"content"`
	Username          string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName         string `json:"token_name" gorm:"index;default:''"`
	ModelName         string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota             int    `json:"quota" gorm:"default:0"`
	PromptTokens      int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens  int    `json:"completion_tokens" gorm:"default:0"`
	UseTime           int    `json:"use_time" gorm:"default:0"`
	IsStream          bool   `json:"is_stream"`
	ChannelId         int    `json:"channel" gorm:"index"`
	ChannelName       string `json:"channel_name" gorm:"->"`
	TokenId           int    `json:"token_id" gorm:"default:0;index"`
	Group             string `json:"group" gorm:"index"`
	Ip                string `json:"ip" gorm:"index;default:''"`
	RequestId         string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);index:idx_logs_upstream_request_id;default:''"`
	Other             string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
	LogTypeLogin   = 7
)

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

func createLog(log *Log) error {
	ensureLogRequestId(log)
	return LOG_DB.Create(log).Error
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "request_id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].Id = startIdx + i + 1
	}
}

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		formatUserLog(logs[i], startIdx+i+1)
	}
	assignDisplayLogIds(logs, startIdx)
}

func formatUserLog(log *Log, displayId int) {
	log.ChannelName = ""
	var otherMap map[string]interface{}
	otherMap, _ = common.StrToMap(log.Other)
	if otherMap != nil {
		// Remove admin-only debug fields.
		delete(otherMap, "admin_info")
		delete(otherMap, "audit_info")
		// delete(otherMap, "reject_reason")
		delete(otherMap, "stream_status")
	}
	log.Other = common.MapToJsonStr(otherMap)
	log.Id = displayId
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order(order).Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLogWithAdminInfo 记录操作日志，并将管理员相关信息存入 Other.admin_info，
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo map[string]interface{}) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if len(adminInfo) > 0 {
		other := map[string]interface{}{
			"admin_info": adminInfo,
		}
		log.Other = common.MapToJsonStr(other)
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// buildOpField 构建语言无关的操作描述（写入 Other.op）。
// 前端依据 action(稳定操作标识) + params(结构化参数) 在渲染期用 i18n 本地化展示，
// 因此不在数据库中存储自然语言句子。
func buildOpField(action string, params map[string]interface{}) map[string]interface{} {
	op := map[string]interface{}{
		"action": action,
	}
	if len(params) > 0 {
		op["params"] = params
	}
	return op
}

// RecordLoginLog 记录用户登录成功的审计日志（type=LogTypeLogin）。
// username 由调用方传入（登录流程已持有用户对象），避免额外的数据库查询。
// content 为英文兜底文本（用于导出/经典前端）；action+params 供前端本地化渲染。
// extra 可携带 login_method、user_agent 等附加信息（普通用户可见）。
func RecordLoginLog(userId int, username string, content string, ip string, action string, params map[string]interface{}, extra map[string]interface{}) {
	other := map[string]interface{}{}
	for k, v := range extra {
		other[k] = v
	}
	other["op"] = buildOpField(action, params)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeLogin,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record login log: " + err.Error())
	}
}

// RecordOperationAuditLog 记录管理/高危操作审计日志（type=LogTypeManage）。
// logUserId 为日志归属者，管理审计日志应归属实际操作者；目标资源/用户放入
// action params。username 内部按 logUserId 查询。content 为英文兜底文本（导出/经典前端用）。
// action+params 写入 Other.op，供前端本地化渲染（普通用户可见，不含敏感信息）。
// adminInfo 存放操作者身份（写入 Other.admin_info，普通用户查询时剥离）；
// auditInfo 存放路由/方法/结果等中间件兜底信息（写入 Other.audit_info，普通用户查询时剥离）。
func RecordOperationAuditLog(logUserId int, content string, ip string, action string, params map[string]interface{}, adminInfo map[string]interface{}, auditInfo map[string]interface{}) {
	username, _ := GetUsernameById(logUserId, false)
	other := map[string]interface{}{
		"op": buildOpField(action, params),
	}
	if len(adminInfo) > 0 {
		other["admin_info"] = adminInfo
	}
	if len(auditInfo) > 0 {
		other["audit_info"] = auditInfo
	}
	log := &Log{
		UserId:    logUserId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeManage,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record operation audit log: " + err.Error())
	}
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	}
	other := map[string]interface{}{
		"admin_info": adminInfo,
	}
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Ip:        callerIp,
		Other:     common.MapToJsonStr(other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(content)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := common.MapToJsonStr(other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
}

type RecordConsumeLogParams struct {
	ChannelId        int                    `json:"channel_id"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	Quota            int                    `json:"quota"`
	Content          string                 `json:"content"`
	TokenId          int                    `json:"token_id"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Other            map[string]interface{} `json:"other"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	createdAt := common.GetTimestamp()
	otherStr := common.MapToJsonStr(params.Other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	if common.DataExportEnabled {
		LogQuotaData(QuotaDataLogParams{
			UserID:    userId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			TokenUsed: params.PromptTokens + params.CompletionTokens,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  common.NodeName,
		})
	}
}

type RecordTaskBillingLogParams struct {
	UserId    int
	LogType   int
	Content   string
	ChannelId int
	ModelName string
	Quota     int
	TokenId   int
	Group     string
	Other     map[string]interface{}
	NodeName  string // 任务发起节点；为空时回退当前节点
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	createdAt := common.GetTimestamp()
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: createdAt,
		Type:      params.LogType,
		Content:   params.Content,
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		Other:     common.MapToJsonStr(params.Other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
	}
	if params.LogType == LogTypeConsume && common.DataExportEnabled {
		nodeName := params.NodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		LogQuotaData(QuotaDataLogParams{
			UserID:    params.UserId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  nodeName,
		})
	}
}

type LogQueryOptions struct {
	Context            context.Context
	LogType            int
	StartTimestamp     int64
	EndTimestamp       int64
	ModelName          string
	Username           string
	TokenName          string
	StartIdx           int
	Num                int
	Channel            int
	Group              string
	RequestId          string
	UpstreamRequestId  string
	Expr               string
	UserId             int
	IncludeAdminFields bool
	NoLimit            bool
}

func logQueryDB(opts LogQueryOptions) *gorm.DB {
	if opts.Context == nil {
		return LOG_DB
	}
	return LOG_DB.WithContext(opts.Context)
}

func publicLogQueryError(err error, operation string, message string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	common.SysError(operation + ": " + err.Error())
	return errors.New(message)
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	return GetAllLogsWithOptions(LogQueryOptions{
		LogType:            logType,
		StartTimestamp:     startTimestamp,
		EndTimestamp:       endTimestamp,
		ModelName:          modelName,
		Username:           username,
		TokenName:          tokenName,
		StartIdx:           startIdx,
		Num:                num,
		Channel:            channel,
		Group:              group,
		RequestId:          requestId,
		UpstreamRequestId:  upstreamRequestId,
		IncludeAdminFields: true,
	})
}

func GetAllLogsWithOptions(opts LogQueryOptions) (logs []*Log, total int64, err error) {
	exprFilter, err := compileLogExprFilter(opts.Expr, opts.IncludeAdminFields)
	if err != nil {
		return nil, 0, err
	}
	tx := logQueryDB(opts).Model(&Log{})
	if opts.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", opts.LogType)
	}
	tx = applyLogFilters(tx, opts, exprFilter)
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	order := "logs.created_at desc, logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	query := tx.Order(order)
	if !opts.NoLimit {
		query = query.Limit(opts.Num).Offset(opts.StartIdx)
	}
	err = query.Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, opts.StartIdx)
	}

	if err = fillLogChannelNames(opts.Context, logs); err != nil {
		return logs, total, err
	}

	return logs, total, err
}

func fillLogChannelNames(ctx context.Context, logs []*Log) error {
	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() == 0 {
		return nil
	}

	var channels []struct {
		Id   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if common.MemoryCacheEnabled {
		// Cache get channel
		for _, channelId := range channelIds.Items() {
			if cacheChannel, err := CacheGetChannel(channelId); err == nil {
				channels = append(channels, struct {
					Id   int    `gorm:"column:id"`
					Name string `gorm:"column:name"`
				}{
					Id:   channelId,
					Name: cacheChannel.Name,
				})
			}
		}
	} else {
		// Bulk query channels from DB
		db := DB
		if ctx != nil {
			db = db.WithContext(ctx)
		}
		if err := db.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
			return err
		}
	}
	channelMap := make(map[int]string, len(channels))
	for _, channel := range channels {
		channelMap[channel.Id] = channel.Name
	}
	for i := range logs {
		logs[i].ChannelName = channelMap[logs[i].ChannelId]
	}
	return nil
}

const logSearchCountLimit = 10000
const LogExportLimit = 20000

var logExportBatchSize = 1000

type LogExportRowHandler func(log *Log) error
type LogExportReadyHandler func() error

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	return GetUserLogsWithOptions(LogQueryOptions{
		UserId:            userId,
		LogType:           logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         modelName,
		TokenName:         tokenName,
		StartIdx:          startIdx,
		Num:               num,
		Group:             group,
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
	})
}

func GetUserLogsWithOptions(opts LogQueryOptions) (logs []*Log, total int64, err error) {
	exprFilter, err := compileLogExprFilter(opts.Expr, opts.IncludeAdminFields)
	if err != nil {
		return nil, 0, err
	}
	tx := logQueryDB(opts).Model(&Log{}).Where("logs.user_id = ?", opts.UserId)
	if opts.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", opts.LogType)
	}
	tx = applyLogFilters(tx, opts, exprFilter)
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		return nil, 0, publicLogQueryError(err, "failed to count user logs", "查询日志失败")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	query := tx.Order(order)
	if !opts.NoLimit {
		query = query.Limit(opts.Num).Offset(opts.StartIdx)
	}
	err = query.Find(&logs).Error
	if err != nil {
		return nil, 0, publicLogQueryError(err, "failed to search user logs", "查询日志失败")
	}

	formatUserLogs(logs, opts.StartIdx)
	return logs, total, err
}

func ExportAllLogsWithOptions(opts LogQueryOptions) (logs []*Log, err error) {
	opts.StartIdx = 0
	normalizeLogExportLimit(&opts)

	exprFilter, err := compileLogExprFilter(opts.Expr, opts.IncludeAdminFields)
	if err != nil {
		return nil, err
	}
	tx := logQueryDB(opts).Model(&Log{})
	if opts.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", opts.LogType)
	}
	tx = applyLogFilters(tx, opts, exprFilter)
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	tx = tx.Order(order)
	if !opts.NoLimit {
		tx = tx.Limit(opts.Num)
	}
	if err = tx.Find(&logs).Error; err != nil {
		return nil, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, 0)
	}
	if err = fillLogChannelNames(opts.Context, logs); err != nil {
		return logs, err
	}
	return logs, nil
}

func ExportUserLogsWithOptions(opts LogQueryOptions) (logs []*Log, err error) {
	opts.StartIdx = 0
	normalizeLogExportLimit(&opts)

	exprFilter, err := compileLogExprFilter(opts.Expr, opts.IncludeAdminFields)
	if err != nil {
		return nil, err
	}
	tx := logQueryDB(opts).Model(&Log{}).Where("logs.user_id = ?", opts.UserId)
	if opts.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", opts.LogType)
	}
	tx = applyLogFilters(tx, opts, exprFilter)
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	tx = tx.Order(order)
	if !opts.NoLimit {
		tx = tx.Limit(opts.Num)
	}
	if err = tx.Find(&logs).Error; err != nil {
		return nil, publicLogQueryError(err, "failed to export user logs", "导出日志失败")
	}

	formatUserLogs(logs, 0)
	return logs, nil
}

func StreamExportAllLogsWithOptions(opts LogQueryOptions, onReady LogExportReadyHandler, handle LogExportRowHandler) error {
	opts.StartIdx = 0
	normalizeLogExportLimit(&opts)

	exprFilter, err := compileLogExprFilter(opts.Expr, opts.IncludeAdminFields)
	if err != nil {
		return err
	}
	tx := logQueryDB(opts).Model(&Log{})
	if opts.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", opts.LogType)
	}
	tx = applyLogFilters(tx, opts, exprFilter)

	channelNames, err := newLogChannelNameResolver(opts.Context)
	if err != nil {
		return err
	}
	return streamExportLogs(tx, opts, onReady, func(index int, log *Log) error {
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			log.Id = index
		}
		channelNames.Fill(log)
		return handle(log)
	})
}

func StreamExportUserLogsWithOptions(opts LogQueryOptions, onReady LogExportReadyHandler, handle LogExportRowHandler) error {
	opts.StartIdx = 0
	normalizeLogExportLimit(&opts)

	exprFilter, err := compileLogExprFilter(opts.Expr, opts.IncludeAdminFields)
	if err != nil {
		return err
	}
	tx := logQueryDB(opts).Model(&Log{}).Where("logs.user_id = ?", opts.UserId)
	if opts.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", opts.LogType)
	}
	tx = applyLogFilters(tx, opts, exprFilter)

	return streamExportLogs(tx, opts, onReady, func(index int, log *Log) error {
		formatUserLog(log, index)
		return handle(log)
	})
}

func normalizeLogExportLimit(opts *LogQueryOptions) {
	if !opts.NoLimit && (opts.Num <= 0 || opts.Num > LogExportLimit) {
		opts.Num = LogExportLimit
	}
}

func streamExportLogs(tx *gorm.DB, opts LogQueryOptions, onReady LogExportReadyHandler, handle func(index int, log *Log) error) error {
	order := "logs.id desc"
	usingClickHouse := common.UsingLogDatabase(common.DatabaseTypeClickHouse)
	if usingClickHouse {
		order = clickHouseLogOrder("logs.")
	}
	tx = tx.Select("logs.*")

	batchSize := logExportBatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}
	remaining := opts.Num
	if !opts.NoLimit && remaining < batchSize {
		batchSize = remaining
	}

	index := 0
	ready := false
	var cursorId int
	var cursorCreatedAt int64
	var cursorRequestId string
	hasCursor := false

	for {
		if opts.Context != nil {
			if err := opts.Context.Err(); err != nil {
				return err
			}
		}

		pageSize := batchSize
		if !opts.NoLimit && remaining < pageSize {
			pageSize = remaining
		}
		if pageSize <= 0 {
			break
		}

		pageQuery := tx
		if hasCursor {
			if usingClickHouse {
				pageQuery = pageQuery.Where(
					"(logs.created_at < ?) OR (logs.created_at = ? AND logs.request_id < ?)",
					cursorCreatedAt,
					cursorCreatedAt,
					cursorRequestId,
				)
			} else {
				pageQuery = pageQuery.Where("logs.id < ?", cursorId)
			}
		}

		var page []*Log
		if err := pageQuery.Order(order).Limit(pageSize).Find(&page).Error; err != nil {
			return err
		}
		if !ready {
			ready = true
			if onReady != nil {
				if err := onReady(); err != nil {
					return err
				}
			}
		}
		if len(page) == 0 {
			break
		}
		last := page[len(page)-1]
		nextCursorId := last.Id
		nextCursorCreatedAt := last.CreatedAt
		nextCursorRequestId := last.RequestId

		for _, log := range page {
			index++
			if err := handle(index, log); err != nil {
				return err
			}
		}

		cursorId = nextCursorId
		cursorCreatedAt = nextCursorCreatedAt
		cursorRequestId = nextCursorRequestId
		hasCursor = true
		if !opts.NoLimit {
			remaining -= len(page)
		}
		if len(page) < pageSize {
			break
		}
	}
	return nil
}

type logChannelNameResolver struct {
	names map[int]string
}

func newLogChannelNameResolver(ctx context.Context) (*logChannelNameResolver, error) {
	resolver := &logChannelNameResolver{
		names: make(map[int]string),
	}
	var channels []struct {
		Id   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}
	if err := db.Table("channels").Select("id, name").Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		resolver.names[channel.Id] = channel.Name
	}
	return resolver, nil
}

func (r *logChannelNameResolver) Fill(log *Log) {
	if log.ChannelId == 0 {
		return
	}
	if name, ok := r.names[log.ChannelId]; ok {
		log.ChannelName = name
	}
}

type Stat struct {
	Quota int `json:"quota"`
	Rpm   int `json:"rpm"`
	Tpm   int `json:"tpm"`
}

func logContainsPattern(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}

	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return "%" + replacer.Replace(input) + "%", true
}

func applyLogContainsFilter(tx *gorm.DB, column string, value string) *gorm.DB {
	value = strings.TrimSpace(value)
	if value == "" {
		return tx
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return tx.Where(column+" LIKE ?", "%"+escapeClickHouseLikeLiteral(value)+"%")
	}
	pattern, _ := logContainsPattern(value)
	return tx.Where(column+" LIKE ? ESCAPE '!'", pattern)
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat, err error) {
	return SumUsedQuotaWithOptions(LogQueryOptions{
		LogType:            logType,
		StartTimestamp:     startTimestamp,
		EndTimestamp:       endTimestamp,
		ModelName:          modelName,
		Username:           username,
		TokenName:          tokenName,
		Channel:            channel,
		Group:              group,
		IncludeAdminFields: true,
	})
}

func SumUsedQuotaWithOptions(opts LogQueryOptions) (stat Stat, err error) {
	exprFilter, err := compileLogExprFilter(opts.Expr, opts.IncludeAdminFields)
	if err != nil {
		return stat, err
	}
	db := logQueryDB(opts)
	tx := db.Table("logs").Select(logCoalesceInt("sum(logs.quota)") + " quota")

	// 为rpm和tpm创建单独的查询
	tokensExpr := logCoalesceInt("sum(logs.prompt_tokens)") + " + " + logCoalesceInt("sum(logs.completion_tokens)")
	rpmTpmQuery := db.Table("logs").Select("count(*) rpm, " + tokensExpr + " tpm")

	// The expression is parsed once above, including one consistent evaluation
	// of today/yesterday, and the compiled parameterized predicate is then
	// applied to both aggregate queries.
	tx = applyLogStatFilters(tx, opts, true, exprFilter)
	rpmTpmQuery = applyLogStatFilters(rpmTpmQuery, opts, true, exprFilter)
	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("logs.created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	if err := tx.Scan(&stat).Error; err != nil {
		return stat, publicLogQueryError(err, "failed to query log stat", "查询统计数据失败")
	}
	if err := rpmTpmQuery.Scan(&stat).Error; err != nil {
		return stat, publicLogQueryError(err, "failed to query rpm/tpm stat", "查询统计数据失败")
	}

	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if nil != ctx.Err() {
		return 0, ctx.Err()
	}

	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse DELETE is a heavy mutation that rewrites data parts, so
		// per-batch mutations would be pathologically slow. Remove all matching
		// rows in a single synchronous mutation regardless of limit; the reported
		// count lets the caller's progress loop complete in one pass.
		total, err := CountOldLog(ctx, targetTimestamp)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			"ALTER TABLE logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	result := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
	if nil != result.Error {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func DeleteOldLog(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}

	var total int64 = 0

	for {
		if nil != ctx.Err() {
			return total, ctx.Err()
		}

		rowsAffected, err := DeleteOldLogBatch(ctx, targetTimestamp, limit)
		if nil != err {
			return total, err
		}

		total += rowsAffected

		if rowsAffected < int64(limit) {
			break
		}
	}

	return total, nil
}
