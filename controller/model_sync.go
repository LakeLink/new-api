package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 上游地址
const (
	upstreamModelsURL  = "https://basellm.github.io/llm-metadata/api/newapi/models.json"
	upstreamVendorsURL = "https://basellm.github.io/llm-metadata/api/newapi/vendors.json"
)

func normalizeLocale(locale string) (string, bool) {
	l := strings.ToLower(strings.TrimSpace(locale))
	switch l {
	case "en", "zh-CN", "zh-TW", "ja":
		return l, true
	default:
		return "", false
	}
}

func getUpstreamBase() string {
	return common.GetEnvOrDefaultString("SYNC_UPSTREAM_BASE", "https://basellm.github.io/llm-metadata")
}

func getUpstreamURLs(locale string) (modelsURL, vendorsURL string) {
	base := strings.TrimRight(getUpstreamBase(), "/")
	if l, ok := normalizeLocale(locale); ok && l != "" {
		return fmt.Sprintf("%s/api/i18n/%s/newapi/models.json", base, l),
			fmt.Sprintf("%s/api/i18n/%s/newapi/vendors.json", base, l)
	}
	return fmt.Sprintf("%s/api/newapi/models.json", base), fmt.Sprintf("%s/api/newapi/vendors.json", base)
}

type upstreamEnvelope[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    []T    `json:"data"`
}

func decodeUpstreamEnvelope[T any](data []byte, out *upstreamEnvelope[T]) error {
	if out == nil {
		return errors.New("upstream response destination is nil")
	}
	var envelope struct {
		Success *bool  `json:"success"`
		Message string `json:"message"`
		Data    []T    `json:"data"`
	}
	if err := common.Unmarshal(data, &envelope); err != nil {
		var items []T
		if arrayErr := common.Unmarshal(data, &items); arrayErr != nil {
			return errors.New("invalid upstream JSON response")
		}
		if items == nil {
			return errors.New("invalid upstream JSON array")
		}
		*out = upstreamEnvelope[T]{Success: true, Data: items}
		return nil
	}
	if envelope.Success != nil && !*envelope.Success {
		return errors.New("upstream reported an unsuccessful response")
	}
	if envelope.Success == nil && envelope.Data == nil {
		return errors.New("invalid upstream response envelope")
	}
	*out = upstreamEnvelope[T]{
		Success: true,
		Message: envelope.Message,
		Data:    envelope.Data,
	}
	return nil
}

type upstreamModel struct {
	Description string          `json:"description"`
	Endpoints   json.RawMessage `json:"endpoints"`
	Icon        string          `json:"icon"`
	ModelName   string          `json:"model_name"`
	NameRule    int             `json:"name_rule"`
	Status      int             `json:"status"`
	Tags        string          `json:"tags"`
	VendorName  string          `json:"vendor_name"`
}

type upstreamVendor struct {
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Name        string `json:"name"`
	Status      int    `json:"status"`
}

var (
	etagCache  = make(map[string]string)
	bodyCache  = make(map[string][]byte)
	cacheMutex sync.RWMutex
)

type overwriteField struct {
	ModelName string   `json:"model_name"`
	Fields    []string `json:"fields"`
}

type syncRequest struct {
	Overwrite []overwriteField `json:"overwrite"`
	Locale    string           `json:"locale"`
}

func newHTTPClient() *http.Client {
	timeoutSec := common.GetEnvOrDefault("SYNC_HTTP_TIMEOUT_SECONDS", 10)
	timeout := common.SafeIntervalDuration(timeoutSec, time.Second, 10*time.Second, "model sync HTTP timeout")
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		if strings.HasSuffix(host, "github.io") {
			if conn, err := dialer.DialContext(ctx, "tcp4", addr); err == nil {
				return conn, nil
			}
			return dialer.DialContext(ctx, "tcp6", addr)
		}
		return dialer.DialContext(ctx, network, addr)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	if relayClient := service.GetHttpClient(); relayClient != nil {
		client.CheckRedirect = relayClient.CheckRedirect
	}
	return client
}

var (
	httpClientOnce sync.Once
	httpClient     *http.Client
)

func getHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		httpClient = newHTTPClient()
	})
	return httpClient
}

func fetchJSON[T any](ctx context.Context, url string, out *upstreamEnvelope[T]) error {
	if out == nil {
		return errors.New("upstream response destination is nil")
	}
	var lastErr error
	attempts := common.GetEnvOrDefault("SYNC_HTTP_RETRY", 3)
	if attempts < 1 {
		attempts = 1
	} else if attempts > 10 {
		attempts = 10
	}
	baseDelay := 200 * time.Millisecond
	maxMB := common.GetEnvOrDefault("SYNC_HTTP_MAX_MB", 10)
	if maxMB <= 0 {
		maxMB = 10
	}
	maxBytes := common.BytesFromMegabytes(maxMB)
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return service.SanitizeNetworkError(err)
		}
		// ETag conditional request
		cacheMutex.RLock()
		if et := etagCache[url]; et != "" {
			req.Header.Set("If-None-Match", et)
		}
		cacheMutex.RUnlock()

		resp, err := service.DoUpstreamRequest(getHTTPClient(), req)
		if err != nil {
			lastErr = service.SanitizeNetworkError(err)
		} else {
			func() {
				defer resp.Body.Close()
				switch resp.StatusCode {
				case http.StatusOK:
					buf, readErr := service.ReadResponseBodyWithLimit(resp.Body, maxBytes)
					if readErr != nil {
						lastErr = readErr
						return
					}
					var decoded upstreamEnvelope[T]
					if decodeErr := decodeUpstreamEnvelope(buf, &decoded); decodeErr != nil {
						lastErr = decodeErr
						return
					}
					// Publish the ETag and body only after validating the new
					// representation so a malformed 200 cannot poison later 304s.
					cacheMutex.Lock()
					if et := resp.Header.Get("ETag"); et != "" {
						etagCache[url] = et
					} else {
						delete(etagCache, url)
					}
					bodyCache[url] = append([]byte(nil), buf...)
					cacheMutex.Unlock()
					*out = decoded
					lastErr = nil
				case http.StatusNotModified:
					cacheMutex.RLock()
					buf := append([]byte(nil), bodyCache[url]...)
					cacheMutex.RUnlock()
					if len(buf) == 0 {
						lastErr = errors.New("cache miss for 304 response")
						return
					}
					var decoded upstreamEnvelope[T]
					if decodeErr := decodeUpstreamEnvelope(buf, &decoded); decodeErr != nil {
						lastErr = decodeErr
						return
					}
					*out = decoded
					lastErr = nil
				default:
					lastErr = fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
				}
			}()
		}
		if lastErr == nil {
			return nil
		}
		if attempt+1 >= attempts {
			break
		}
		sleep := baseDelay * time.Duration(1<<attempt)
		jitter := time.Duration(rand.Intn(150)) * time.Millisecond
		timer := time.NewTimer(sleep + jitter)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func ensureVendorID(vendorName string, vendorByName map[string]upstreamVendor, vendorIDCache map[string]int, createdVendors *int) (int, error) {
	if vendorName == "" {
		return 0, nil
	}
	if id, ok := vendorIDCache[vendorName]; ok {
		return id, nil
	}
	var existing model.Vendor
	err := model.DB.Where("name = ?", vendorName).First(&existing).Error
	if err == nil {
		vendorIDCache[vendorName] = existing.Id
		return existing.Id, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("query vendor %q: %w", vendorName, err)
	}
	uv := vendorByName[vendorName]
	v := &model.Vendor{
		Name:        vendorName,
		Description: uv.Description,
		Icon:        coalesce(uv.Icon, ""),
		Status:      chooseStatus(uv.Status, 1),
	}
	if err := v.Insert(); err == nil {
		(*createdVendors)++
		vendorIDCache[vendorName] = v.Id
		return v.Id, nil
	} else {
		// A concurrent sync may have created the same normalized vendor after
		// the lookup. Resolve that race before reporting a real write failure.
		if lookupErr := model.DB.Where("name = ?", vendorName).First(&existing).Error; lookupErr == nil {
			vendorIDCache[vendorName] = existing.Id
			return existing.Id, nil
		}
		return 0, fmt.Errorf("create vendor %q: %w", vendorName, err)
	}
}

// SyncUpstreamModels 同步上游模型与供应商：
// - 默认仅创建「未配置模型」
// - 可通过 overwrite 选择性覆盖更新本地已有模型的字段（前提：sync_official <> 0）
func SyncUpstreamModels(c *gin.Context) {
	var req syncRequest
	// An empty body selects the default sync behavior. Any non-empty malformed
	// body must fail before the handler performs database or upstream work;
	// otherwise a typo in an admin request silently triggers a different,
	// mutating synchronization operation.
	if bindErr := common.DecodeJson(c.Request.Body, &req); bindErr != nil && !errors.Is(bindErr, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}
	// 1) 获取未配置模型列表
	missing, err := model.GetMissingModels()
	if err != nil {
		common.SysError("failed to get missing models: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取模型列表失败，请稍后重试"})
		return
	}

	// 若既无缺失模型需要创建，也未指定覆盖更新字段，则无需请求上游数据，直接返回
	if len(missing) == 0 && len(req.Overwrite) == 0 {
		modelsURL, vendorsURL := getUpstreamURLs(req.Locale)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"created_models":  0,
				"created_vendors": 0,
				"updated_models":  0,
				"skipped_models":  []string{},
				"created_list":    []string{},
				"updated_list":    []string{},
				"source": gin.H{
					"locale":      req.Locale,
					"models_url":  modelsURL,
					"vendors_url": vendorsURL,
				},
			},
		})
		return
	}

	// 2) 拉取上游 vendors 与 models
	timeout := common.SafeIntervalDuration(
		common.GetEnvOrDefault("SYNC_HTTP_TIMEOUT_SECONDS", 15),
		time.Second,
		15*time.Second,
		"model sync request timeout",
	)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	modelsURL, vendorsURL := getUpstreamURLs(req.Locale)
	var vendorsEnv upstreamEnvelope[upstreamVendor]
	var modelsEnv upstreamEnvelope[upstreamModel]
	var fetchErr error
	var vendorFetchErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		vendorFetchErr = fetchJSON(ctx, vendorsURL, &vendorsEnv)
	}()
	go func() {
		defer wg.Done()
		if err := fetchJSON(ctx, modelsURL, &modelsEnv); err != nil {
			fetchErr = err
		}
	}()
	wg.Wait()
	if fetchErr != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取上游模型失败: " + fetchErr.Error(), "locale": req.Locale, "source_urls": gin.H{"models_url": modelsURL, "vendors_url": vendorsURL}})
		return
	}
	if vendorFetchErr != nil {
		// Models contain the vendor name, so synchronization can still proceed
		// without optional vendor descriptions/icons. Keep the degraded result
		// observable instead of silently treating the metadata fetch as healthy.
		common.SysError("failed to fetch upstream vendor metadata: " + vendorFetchErr.Error())
	}

	// 建立映射
	vendorByName := make(map[string]upstreamVendor)
	for _, v := range vendorsEnv.Data {
		if v.Name != "" {
			vendorByName[v.Name] = v
		}
	}
	modelByName := make(map[string]upstreamModel)
	for _, m := range modelsEnv.Data {
		if m.ModelName != "" {
			modelByName[m.ModelName] = m
		}
	}

	// 3) 执行同步：仅创建缺失模型；若上游缺失该模型则跳过
	createdModels := 0
	createdVendors := 0
	updatedModels := 0
	skipped := make([]string, 0)
	createdList := make([]string, 0)
	updatedList := make([]string, 0)

	// 本地缓存：vendorName -> id
	vendorIDCache := make(map[string]int)

	for _, name := range missing {
		up, ok := modelByName[name]
		if !ok {
			skipped = append(skipped, name)
			continue
		}

		// 若本地已存在且设置为不同步，则跳过（极端情况：缺失列表与本地状态不同步时）
		var existing model.Model
		err := model.DB.Where("model_name = ?", name).First(&existing).Error
		if err == nil {
			if existing.SyncOfficial == 0 {
				skipped = append(skipped, name)
				continue
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError("failed to inspect model during upstream sync: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "读取本地模型失败，请稍后重试"})
			return
		}

		// 确保 vendor 存在
		vendorID, err := ensureVendorID(up.VendorName, vendorByName, vendorIDCache, &createdVendors)
		if err != nil {
			common.SysError("failed to synchronize model vendor: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "同步供应商信息失败，请稍后重试"})
			return
		}

		// 创建模型
		mi := &model.Model{
			ModelName:   name,
			Description: up.Description,
			Icon:        up.Icon,
			Tags:        up.Tags,
			VendorID:    vendorID,
			Status:      chooseStatus(up.Status, 1),
			NameRule:    up.NameRule,
		}
		if err := mi.Insert(); err == nil {
			createdModels++
			createdList = append(createdList, name)
		} else {
			// A concurrent sync can win the unique-name insert. Treat that
			// narrow race as already synchronized, but surface every other
			// persistence failure.
			var concurrent model.Model
			if lookupErr := model.DB.Where("model_name = ?", name).First(&concurrent).Error; lookupErr == nil {
				skipped = append(skipped, name)
				continue
			}
			common.SysError("failed to create model during upstream sync: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建本地模型失败，请稍后重试"})
			return
		}
	}

	// 4) 处理可选覆盖（更新本地已有模型的差异字段）
	if len(req.Overwrite) > 0 {
		// vendorIDCache 已用于创建阶段，可复用
		for _, ow := range req.Overwrite {
			up, ok := modelByName[ow.ModelName]
			if !ok {
				continue
			}
			var local model.Model
			if err := model.DB.Where("model_name = ?", ow.ModelName).First(&local).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				common.SysError("failed to read model for upstream overwrite: " + err.Error())
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "读取本地模型失败，请稍后重试"})
				return
			}

			// 跳过被禁用官方同步的模型
			if local.SyncOfficial == 0 {
				continue
			}

			// 映射 vendor
			newVendorID, err := ensureVendorID(up.VendorName, vendorByName, vendorIDCache, &createdVendors)
			if err != nil {
				common.SysError("failed to synchronize overwrite vendor: " + err.Error())
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "同步供应商信息失败，请稍后重试"})
				return
			}

			// 应用字段覆盖（事务）
			err = model.DB.Transaction(func(tx *gorm.DB) error {
				needUpdate := false
				if containsField(ow.Fields, "description") {
					local.Description = up.Description
					needUpdate = true
				}
				if containsField(ow.Fields, "icon") {
					local.Icon = up.Icon
					needUpdate = true
				}
				if containsField(ow.Fields, "tags") {
					local.Tags = up.Tags
					needUpdate = true
				}
				if containsField(ow.Fields, "vendor") {
					local.VendorID = newVendorID
					needUpdate = true
				}
				if containsField(ow.Fields, "name_rule") {
					local.NameRule = up.NameRule
					needUpdate = true
				}
				if containsField(ow.Fields, "status") {
					local.Status = chooseStatus(up.Status, local.Status)
					needUpdate = true
				}
				if !needUpdate {
					return nil
				}
				if err := tx.Save(&local).Error; err != nil {
					return err
				}
				updatedModels++
				updatedList = append(updatedList, ow.ModelName)
				return nil
			})
			if err != nil {
				common.SysError("failed to overwrite model from upstream metadata: " + err.Error())
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "更新本地模型失败，请稍后重试"})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"created_models":  createdModels,
			"created_vendors": createdVendors,
			"updated_models":  updatedModels,
			"skipped_models":  skipped,
			"created_list":    createdList,
			"updated_list":    updatedList,
			"source": gin.H{
				"locale":      req.Locale,
				"models_url":  modelsURL,
				"vendors_url": vendorsURL,
			},
		},
	})
}

func containsField(fields []string, key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, f := range fields {
		if strings.ToLower(strings.TrimSpace(f)) == key {
			return true
		}
	}
	return false
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func chooseStatus(primary, fallback int) int {
	if primary == 0 && fallback != 0 {
		return fallback
	}
	if primary != 0 {
		return primary
	}
	return 1
}

// SyncUpstreamPreview 预览上游与本地的差异（仅用于弹窗选择）
func SyncUpstreamPreview(c *gin.Context) {
	// 1) 拉取上游数据
	timeout := common.SafeIntervalDuration(
		common.GetEnvOrDefault("SYNC_HTTP_TIMEOUT_SECONDS", 15),
		time.Second,
		15*time.Second,
		"model sync preview timeout",
	)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	locale := c.Query("locale")
	modelsURL, vendorsURL := getUpstreamURLs(locale)

	var vendorsEnv upstreamEnvelope[upstreamVendor]
	var modelsEnv upstreamEnvelope[upstreamModel]
	var fetchErr error
	var vendorFetchErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		vendorFetchErr = fetchJSON(ctx, vendorsURL, &vendorsEnv)
	}()
	go func() {
		defer wg.Done()
		if err := fetchJSON(ctx, modelsURL, &modelsEnv); err != nil {
			fetchErr = err
		}
	}()
	wg.Wait()
	if fetchErr != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取上游模型失败: " + fetchErr.Error(), "locale": locale, "source_urls": gin.H{"models_url": modelsURL, "vendors_url": vendorsURL}})
		return
	}
	if vendorFetchErr != nil {
		common.SysError("failed to fetch upstream vendor metadata for preview: " + vendorFetchErr.Error())
	}

	vendorByName := make(map[string]upstreamVendor)
	for _, v := range vendorsEnv.Data {
		if v.Name != "" {
			vendorByName[v.Name] = v
		}
	}
	modelByName := make(map[string]upstreamModel)
	upstreamNames := make([]string, 0, len(modelsEnv.Data))
	for _, m := range modelsEnv.Data {
		if m.ModelName != "" {
			modelByName[m.ModelName] = m
			upstreamNames = append(upstreamNames, m.ModelName)
		}
	}

	// 2) 本地已有模型
	var locals []model.Model
	if len(upstreamNames) > 0 {
		if err := model.DB.Where("model_name IN ? AND sync_official <> 0", upstreamNames).Find(&locals).Error; err != nil {
			common.SysError("failed to query local models for upstream preview: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "读取本地模型失败，请稍后重试"})
			return
		}
	}

	// 本地 vendor 名称映射
	vendorIdSet := make(map[int]struct{})
	for _, m := range locals {
		if m.VendorID != 0 {
			vendorIdSet[m.VendorID] = struct{}{}
		}
	}
	vendorIDs := make([]int, 0, len(vendorIdSet))
	for id := range vendorIdSet {
		vendorIDs = append(vendorIDs, id)
	}
	idToVendorName := make(map[int]string)
	if len(vendorIDs) > 0 {
		var dbVendors []model.Vendor
		if err := model.DB.Where("id IN ?", vendorIDs).Find(&dbVendors).Error; err != nil {
			common.SysError("failed to query local vendors for upstream preview: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "读取本地供应商失败，请稍后重试"})
			return
		}
		for _, v := range dbVendors {
			idToVendorName[v.Id] = v.Name
		}
	}

	// 3) 缺失且上游存在的模型
	missingList, err := model.GetMissingModels()
	if err != nil {
		common.SysError("failed to query missing models for upstream preview: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "读取缺失模型失败，请稍后重试"})
		return
	}
	var missing []string
	for _, name := range missingList {
		if _, ok := modelByName[name]; ok {
			missing = append(missing, name)
		}
	}

	// 4) 计算冲突字段
	type conflictField struct {
		Field    string      `json:"field"`
		Local    interface{} `json:"local"`
		Upstream interface{} `json:"upstream"`
	}
	type conflictItem struct {
		ModelName string          `json:"model_name"`
		Fields    []conflictField `json:"fields"`
	}

	var conflicts []conflictItem
	for _, local := range locals {
		up, ok := modelByName[local.ModelName]
		if !ok {
			continue
		}
		fields := make([]conflictField, 0, 6)
		if strings.TrimSpace(local.Description) != strings.TrimSpace(up.Description) {
			fields = append(fields, conflictField{Field: "description", Local: local.Description, Upstream: up.Description})
		}
		if strings.TrimSpace(local.Icon) != strings.TrimSpace(up.Icon) {
			fields = append(fields, conflictField{Field: "icon", Local: local.Icon, Upstream: up.Icon})
		}
		if strings.TrimSpace(local.Tags) != strings.TrimSpace(up.Tags) {
			fields = append(fields, conflictField{Field: "tags", Local: local.Tags, Upstream: up.Tags})
		}
		// vendor 对比使用名称
		localVendor := idToVendorName[local.VendorID]
		if strings.TrimSpace(localVendor) != strings.TrimSpace(up.VendorName) {
			fields = append(fields, conflictField{Field: "vendor", Local: localVendor, Upstream: up.VendorName})
		}
		if local.NameRule != up.NameRule {
			fields = append(fields, conflictField{Field: "name_rule", Local: local.NameRule, Upstream: up.NameRule})
		}
		if local.Status != chooseStatus(up.Status, local.Status) {
			fields = append(fields, conflictField{Field: "status", Local: local.Status, Upstream: up.Status})
		}
		if len(fields) > 0 {
			conflicts = append(conflicts, conflictItem{ModelName: local.ModelName, Fields: fields})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"missing":   missing,
			"conflicts": conflicts,
			"source": gin.H{
				"locale":      locale,
				"models_url":  modelsURL,
				"vendors_url": vendorsURL,
			},
		},
	})
}
