package model

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id        int    `json:"id"`
	UserID    int    `json:"user_id" gorm:"index"`
	Username  string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	UseGroup  string `json:"use_group" gorm:"index;size:64;default:''"`
	TokenID   int    `json:"token_id" gorm:"index;default:0"`
	ChannelID int    `json:"channel_id" gorm:"index;default:0"`
	NodeName  string `json:"node_name" gorm:"index;size:64;default:''"`
	TokenUsed int    `json:"token_used" gorm:"default:0"`
	Count     int    `json:"count" gorm:"default:0"`
	Quota     int    `json:"quota" gorm:"default:0"`
	// BillingEventID is set only for durable, one-row-per-billing-event exports.
	// Legacy/hourly cache rows keep it NULL, allowing their existing aggregation
	// behavior while a unique non-NULL event ID makes crash replay idempotent.
	BillingEventID *string `json:"-" gorm:"type:varchar(64);uniqueIndex"`
}

type QuotaDataLogParams struct {
	UserID    int
	Username  string
	ModelName string
	Quota     int
	CreatedAt int64
	TokenUsed int
	UseGroup  string
	TokenID   int
	ChannelID int
	NodeName  string
}

func UpdateQuotaData() {
	for {
		if common.GetLegacyOptionBool("DataExportEnabled", &common.DataExportEnabled) {
			common.SysLog("正在更新数据看板数据...")
			SaveQuotaDataCache()
		}
		time.Sleep(common.SafeIntervalDuration(
			common.GetLegacyOptionInt("DataExportInterval", &common.DataExportInterval),
			time.Minute,
			5*time.Minute,
			"data export",
		))
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

func logQuotaDataCache(quotaData *QuotaData) {
	if quotaData == nil {
		return
	}
	quotaData.Count = saturatingQuotaAggregate(0, quotaData.Count, "quota data request count")
	quotaData.Quota = saturatingQuotaAggregate(0, quotaData.Quota, "quota data quota")
	quotaData.TokenUsed = saturatingQuotaAggregate(0, quotaData.TokenUsed, "quota data token usage")
	key := fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d\x00%s",
		quotaData.UserID,
		quotaData.Username,
		quotaData.ModelName,
		quotaData.CreatedAt,
		quotaData.UseGroup,
		quotaData.TokenID,
		quotaData.ChannelID,
		quotaData.NodeName,
	)
	count := quotaData.Count
	quota := quotaData.Quota
	tokenUsed := quotaData.TokenUsed
	cachedQuotaData, ok := CacheQuotaData[key]
	if ok {
		cachedQuotaData.Count = saturatingQuotaAggregate(cachedQuotaData.Count, count, "quota data request count")
		cachedQuotaData.Quota = saturatingQuotaAggregate(cachedQuotaData.Quota, quota, "quota data quota")
		cachedQuotaData.TokenUsed = saturatingQuotaAggregate(cachedQuotaData.TokenUsed, tokenUsed, "quota data token usage")
		quotaData = cachedQuotaData
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(params QuotaDataLogParams) {
	// 只精确到小时
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)
	quotaData := &QuotaData{
		UserID:    params.UserID,
		Username:  params.Username,
		ModelName: params.ModelName,
		CreatedAt: createdAt,
		UseGroup:  params.UseGroup,
		TokenID:   params.TokenID,
		ChannelID: params.ChannelID,
		NodeName:  params.NodeName,
		Count:     1,
		Quota:     params.Quota,
		TokenUsed: params.TokenUsed,
	}

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(quotaData)
}

// RecordQuotaDataEvent persists one analytics row behind the same deterministic
// billing event ID used by the consume-log outbox. It is safe to call whether
// the log was newly inserted or found during replay: a crash can no longer
// leave a committed billing log with permanently missing quota analytics.
func RecordQuotaDataEvent(eventID string, params QuotaDataLogParams) error {
	if eventID == "" {
		return errors.New("quota data billing event id is empty")
	}
	if len(eventID) > 64 {
		return errors.New("quota data billing event id exceeds 64 bytes")
	}
	createdAt := params.CreatedAt
	if createdAt <= 0 {
		createdAt = common.GetTimestamp()
	}
	createdAt -= createdAt % 3600
	event := QuotaData{
		UserID:         params.UserID,
		Username:       params.Username,
		ModelName:      params.ModelName,
		CreatedAt:      createdAt,
		UseGroup:       params.UseGroup,
		TokenID:        params.TokenID,
		ChannelID:      params.ChannelID,
		NodeName:       params.NodeName,
		TokenUsed:      params.TokenUsed,
		Count:          1,
		Quota:          params.Quota,
		BillingEventID: &eventID,
	}
	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "billing_event_id"}},
		DoNothing: true,
	}).Create(&event).Error; err != nil {
		return err
	}

	var persisted QuotaData
	if err := DB.Where("billing_event_id = ?", eventID).First(&persisted).Error; err != nil {
		return err
	}
	if persisted.UserID != event.UserID ||
		persisted.Username != event.Username ||
		persisted.ModelName != event.ModelName ||
		persisted.CreatedAt != event.CreatedAt ||
		persisted.UseGroup != event.UseGroup ||
		persisted.TokenID != event.TokenID ||
		persisted.ChannelID != event.ChannelID ||
		persisted.NodeName != event.NodeName ||
		persisted.TokenUsed != event.TokenUsed ||
		persisted.Count != event.Count ||
		persisted.Quota != event.Quota {
		return errors.New("quota data billing event id reused with different analytics")
	}
	return nil
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	pending := CacheQuotaData
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	size := len(pending)
	failed := make(map[string]*QuotaData)
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for key, quotaData := range pending {
		quotaDataDB := &QuotaData{}
		query := DB.Table("quota_data").
			Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and channel_id = ? and node_name = ?",
				quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.ChannelID, quotaData.NodeName).
			Where("billing_event_id IS NULL").
			First(quotaDataDB)
		var err error
		if quotaDataDB.Id > 0 {
			err = increaseQuotaData(quotaDataDB.Id, quotaData)
		} else if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			err = DB.Table("quota_data").Create(quotaData).Error
		} else {
			err = query.Error
		}
		if err != nil {
			failed[key] = quotaData
			common.SysLog(fmt.Sprintf("save quota data cache error: %s", err))
		}
	}

	// Logging continues while database I/O is in progress. Merge failed rows
	// back into the live cache so neither those retries nor newly arrived
	// counters are lost.
	CacheQuotaDataLock.Lock()
	for _, quotaData := range failed {
		logQuotaDataCache(quotaData)
	}
	CacheQuotaDataLock.Unlock()
	common.SysLog(fmt.Sprintf("保存数据看板数据完成，成功%d条，待重试%d条", size-len(failed), len(failed)))
}

func increaseQuotaData(id int, quotaData *QuotaData) error {
	if id <= 0 || quotaData == nil {
		return errors.New("invalid quota data aggregate")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var persisted QuotaData
		if err := lockForUpdate(tx).
			Where("id = ? AND billing_event_id IS NULL", id).
			First(&persisted).Error; err != nil {
			return err
		}
		count := saturatingQuotaAggregate(persisted.Count, quotaData.Count, "quota data request count")
		quota := saturatingQuotaAggregate(persisted.Quota, quotaData.Quota, "quota data quota")
		tokenUsed := saturatingQuotaAggregate(persisted.TokenUsed, quotaData.TokenUsed, "quota data token usage")
		return tx.Model(&persisted).Updates(map[string]interface{}{
			"count":      count,
			"quota":      quota,
			"token_used": tokenUsed,
		}).Error
	})
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	err = DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}
