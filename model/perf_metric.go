package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PerfMetric stores aggregated relay performance metrics for the model square.
type PerfMetric struct {
	Id             int     `json:"id" gorm:"primaryKey"`
	ModelName      string  `json:"model_name" gorm:"size:128;index:idx_perf_model_bucket,priority:1"`
	Group          string  `json:"group" gorm:"column:group;size:64"`
	BucketTs       int64   `json:"bucket_ts" gorm:"index:idx_perf_model_bucket,priority:2;index:idx_perf_bucket_ts"`
	IdentityHash   *string `json:"-" gorm:"type:char(64);uniqueIndex:ux_perf_metric_identity_hash"`
	RequestCount   int64   `json:"-" gorm:"default:0"`
	SuccessCount   int64   `json:"-" gorm:"default:0"`
	TotalLatencyMs int64   `json:"-" gorm:"default:0"`
	TtftSumMs      int64   `json:"-" gorm:"default:0"`
	TtftCount      int64   `json:"-" gorm:"default:0"`
	OutputTokens   int64   `json:"-" gorm:"default:0"`
	GenerationMs   int64   `json:"-" gorm:"default:0"`
}

func (PerfMetric) TableName() string {
	return "perf_metrics"
}

func (metric *PerfMetric) BeforeCreate(_ *gorm.DB) error {
	if metric.ModelName == "" || utf8.RuneCountInString(metric.ModelName) > 128 {
		return errors.New("invalid performance metric model name")
	}
	if utf8.RuneCountInString(metric.Group) > 64 {
		return errors.New("performance metric group is too long")
	}
	if err := validatePerfMetricCounters(metric); err != nil {
		return err
	}
	identityHash := crossDatabaseIdentityHash(
		metric.ModelName,
		metric.Group,
		strconv.FormatInt(metric.BucketTs, 10),
	)
	metric.IdentityHash = &identityHash
	return nil
}

func UpsertPerfMetric(metric *PerfMetric) error {
	if metric == nil {
		return nil
	}
	if err := validatePerfMetricCounters(metric); err != nil {
		return err
	}
	if metric.RequestCount == 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "identity_hash"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"request_count":    saturatingPerfMetricUpdate("request_count", metric.RequestCount),
			"success_count":    saturatingPerfMetricUpdate("success_count", metric.SuccessCount),
			"total_latency_ms": saturatingPerfMetricUpdate("total_latency_ms", metric.TotalLatencyMs),
			"ttft_sum_ms":      saturatingPerfMetricUpdate("ttft_sum_ms", metric.TtftSumMs),
			"ttft_count":       saturatingPerfMetricUpdate("ttft_count", metric.TtftCount),
			"output_tokens":    saturatingPerfMetricUpdate("output_tokens", metric.OutputTokens),
			"generation_ms":    saturatingPerfMetricUpdate("generation_ms", metric.GenerationMs),
		}),
	}).Create(metric).Error
}

func validatePerfMetricCounters(metric *PerfMetric) error {
	counters := map[string]int64{
		"request_count":    metric.RequestCount,
		"success_count":    metric.SuccessCount,
		"total_latency_ms": metric.TotalLatencyMs,
		"ttft_sum_ms":      metric.TtftSumMs,
		"ttft_count":       metric.TtftCount,
		"output_tokens":    metric.OutputTokens,
		"generation_ms":    metric.GenerationMs,
	}
	for name, value := range counters {
		if value < 0 {
			return fmt.Errorf("performance metric %s cannot be negative", name)
		}
	}
	if metric.SuccessCount > metric.RequestCount {
		return errors.New("performance metric success count exceeds request count")
	}
	if metric.TtftCount > metric.RequestCount {
		return errors.New("performance metric TTFT count exceeds request count")
	}
	return nil
}

func saturatingPerfMetricUpdate(column string, delta int64) clause.Expr {
	qualifiedColumn := "perf_metrics." + column
	return gorm.Expr(
		"CASE WHEN "+qualifiedColumn+" > ? THEN ? ELSE "+qualifiedColumn+" + ? END",
		math.MaxInt64-delta,
		int64(math.MaxInt64),
		delta,
	)
}

func GetPerfMetrics(modelName string, group string, startTs int64, endTs int64) ([]PerfMetric, error) {
	var metrics []PerfMetric
	query := DB.Model(&PerfMetric{}).
		Where("model_name = ? AND bucket_ts >= ? AND bucket_ts <= ?", modelName, startTs, endTs)
	if group != "" {
		query = query.Where(commonGroupCol+" = ?", group)
	}
	err := query.Order("bucket_ts ASC").Find(&metrics).Error
	return metrics, err
}

type PerfMetricSummary struct {
	ModelName      string `json:"model_name"`
	RequestCount   int64  `json:"request_count"`
	SuccessCount   int64  `json:"success_count"`
	TotalLatencyMs int64  `json:"total_latency_ms"`
	OutputTokens   int64  `json:"output_tokens"`
	GenerationMs   int64  `json:"generation_ms"`
}

type PerfMetricSummaryBucket struct {
	ModelName      string `json:"model_name"`
	BucketTs       int64  `json:"bucket_ts"`
	RequestCount   int64  `json:"request_count"`
	SuccessCount   int64  `json:"success_count"`
	TotalLatencyMs int64  `json:"total_latency_ms"`
	OutputTokens   int64  `json:"output_tokens"`
	GenerationMs   int64  `json:"generation_ms"`
}

func GetPerfMetricsSummaryAll(startTs int64, endTs int64, groups []string) ([]PerfMetricSummary, error) {
	var summaries []PerfMetricSummary
	query := DB.Model(&PerfMetric{}).
		Select("model_name, SUM(request_count) as request_count, SUM(success_count) as success_count, SUM(total_latency_ms) as total_latency_ms, SUM(output_tokens) as output_tokens, SUM(generation_ms) as generation_ms").
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs)
	if groups != nil {
		if len(groups) == 0 {
			return summaries, nil
		}
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.
		Group("model_name").
		Having("SUM(request_count) > 0").
		Find(&summaries).Error
	return summaries, err
}

func GetPerfMetricsSummaryBucketsAll(startTs int64, endTs int64, groups []string) ([]PerfMetricSummaryBucket, error) {
	var summaries []PerfMetricSummaryBucket
	query := DB.Model(&PerfMetric{}).
		Select("model_name, bucket_ts, SUM(request_count) as request_count, SUM(success_count) as success_count, SUM(total_latency_ms) as total_latency_ms, SUM(output_tokens) as output_tokens, SUM(generation_ms) as generation_ms").
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs)
	if groups != nil {
		if len(groups) == 0 {
			return summaries, nil
		}
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.
		Group("model_name, bucket_ts").
		Having("SUM(request_count) > 0").
		Order("bucket_ts ASC").
		Find(&summaries).Error
	return summaries, err
}

func DeletePerfMetricsBefore(cutoffTs int64) error {
	if cutoffTs <= 0 {
		return nil
	}
	return DB.Where("bucket_ts < ?", cutoffTs).Delete(&PerfMetric{}).Error
}

func PerfMetricStartTime(hours int) int64 {
	if hours <= 0 || int64(hours) > math.MaxInt64/int64(time.Hour) {
		hours = 24
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
}
