package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

type ThinkingContentInfo struct {
	IsFirstThinkingContent  bool
	SendLastThinkingContent bool
	HasSentThinkingContent  bool
}

const (
	LastMessageTypeNone     = "none"
	LastMessageTypeText     = "text"
	LastMessageTypeTools    = "tools"
	LastMessageTypeThinking = "thinking"
)

type ClaudeConvertInfo struct {
	LastMessagesType string
	Index            int
	Usage            *dto.Usage
	FinishReason     string
	Done             bool

	ToolCallBaseIndex      int
	ToolCallMaxIndexOffset int
}

type RerankerInfo struct {
	Documents       []any
	ReturnDocuments bool
	// CohereSearchUnits is validated provider billing metadata. It remains nil
	// for all other rerank providers.
	CohereSearchUnits *float64
}

type BuildInToolInfo struct {
	ToolName          string
	CallCount         int
	SearchContextSize string
	// Image-generation settings are frozen from the exact outbound request.
	// Response settlement must not read the pre-override client payload.
	ImageModel         string
	ImageQuality       string
	ImageSize          string
	ImagePartialImages int
	ImageOutputs       []ImageGenerationOutputInfo
	ImagePartialCount  int
}

type ImageGenerationOutputInfo struct {
	Quality string
	Size    string
}

type ResponsesUsageInfo struct {
	BuiltInTools map[string]*BuildInToolInfo
	// SeenOutputItems prevents a duplicated streaming output-item event from
	// charging the same provider-side call more than once. Some compatible
	// providers retry SSE frames even though OpenAI emits one done event per
	// output item.
	SeenOutputItems map[string]struct{}
	// SeenPartialImages deduplicates retried partial-image SSE frames.
	SeenPartialImages map[string]struct{}
	// MaxToolCalls is the cap from the exact outbound Responses payload after
	// channel filtering and parameter overrides. Settlement must not validate
	// provider counters against the stale client request.
	MaxToolCalls *uint
}

// WebSearchTool returns the single configured Responses web-search tool.
// A response web_search_call does not identify which request definition ran,
// so multiple current/preview definitions are ambiguous for billing.
func (r *ResponsesUsageInfo) WebSearchTool() (string, *BuildInToolInfo, error) {
	if r == nil {
		return "", nil, nil
	}

	configuredType := ""
	var configuredTool *BuildInToolInfo
	for toolType, tool := range r.BuiltInTools {
		_, recognized := dto.ResponsesWebSearchPricingKey(toolType)
		if !recognized {
			if strings.HasPrefix(toolType, "web_search") {
				return "", nil, fmt.Errorf("unsupported Responses web-search tool type %q", toolType)
			}
			continue
		}
		if configuredType != "" {
			return "", nil, fmt.Errorf("multiple Responses web-search tools are ambiguous: %q and %q", configuredType, toolType)
		}
		if tool == nil {
			return "", nil, fmt.Errorf("Responses web-search tool %q has no usage counter", toolType)
		}
		configuredType = toolType
		configuredTool = tool
	}
	return configuredType, configuredTool, nil
}

type ChannelMeta struct {
	ChannelType          int
	ChannelId            int
	ChannelIsMultiKey    bool
	ChannelMultiKeyIndex int
	ChannelBaseUrl       string
	ApiType              int
	ApiVersion           string
	ApiKey               string
	Organization         string
	ChannelCreateTime    int64
	ParamOverride        map[string]interface{}
	HeadersOverride      map[string]interface{}
	ChannelSetting       dto.ChannelSettings
	ChannelOtherSettings dto.ChannelOtherSettings
	UpstreamModelName    string
	IsModelMapped        bool
	SupportStreamOptions bool // 是否支持流式选项
}

type TokenCountMeta struct {
	//promptTokens int
	estimatePromptTokens int
}

type RelayInfo struct {
	TokenId           int
	TokenKey          string
	TokenGroup        string
	UserId            int
	UsingGroup        string // 使用的分组，当auto跨分组重试时，会变动
	UserGroup         string // 用户所在分组
	TokenUnlimited    bool
	StartTime         time.Time
	FirstResponseTime time.Time
	isFirstResponse   bool
	//SendLastReasoningResponse bool
	IsStream                   bool
	IsGeminiBatchEmbedding     bool
	IsPlayground               bool
	UsePrice                   bool
	RelayMode                  int
	OriginModelName            string
	RequestURLPath             string
	RequestHeaders             map[string]string
	ShouldIncludeUsage         bool
	UpstreamStreamForNonStream bool // 客户端非流式请求是否被转换为上游流式请求
	DisablePing                bool // 是否禁止向下游发送自定义 Ping
	ClientWs                   *websocket.Conn
	TargetWs                   *websocket.Conn
	InputAudioFormat           string
	OutputAudioFormat          string
	RealtimeTools              []dto.RealTimeTool
	IsFirstRequest             bool
	AudioUsage                 bool
	ReasoningEffort            string
	UserSetting                dto.UserSetting
	UserEmail                  string
	UserQuota                  int
	RelayFormat                types.RelayFormat
	SendResponseCount          int
	ReceivedResponseCount      int
	FinalPreConsumedQuota      int // 最终预消耗的配额
	// ForcePreConsume 为 true 时禁用 BillingSession 的信任额度旁路，
	// 强制预扣全额。用于异步任务（视频/音乐生成等），因为请求返回后任务仍在运行，
	// 必须在提交前锁定全额。
	ForcePreConsume bool
	// Billing 是计费会话，封装了预扣费/结算/退款的统一生命周期。
	// 免费模型时为 nil。
	Billing BillingSettler
	// BillingSource indicates whether this request is billed from wallet quota or subscription.
	// "" or "wallet" => wallet; "subscription" => subscription
	BillingSource string
	// SubscriptionId is the user_subscriptions.id used when BillingSource == "subscription"
	SubscriptionId int
	// SubscriptionPreConsumed is the amount pre-consumed on subscription item (quota units or 1)
	SubscriptionPreConsumed int64
	// SubscriptionPostDelta is the post-consume delta applied to amount_used (quota units; can be negative).
	SubscriptionPostDelta int64
	// SubscriptionWalletOverflow is the settlement amount charged to the wallet
	// after a wallet-overflow-enabled subscription reached its quota boundary.
	SubscriptionWalletOverflow int
	// SubscriptionPlanId / SubscriptionPlanTitle are used for logging/UI display.
	SubscriptionPlanId    int
	SubscriptionPlanTitle string
	// RequestId is used for idempotent pre-consume/refund
	RequestId string
	// SubscriptionAmountTotal / SubscriptionAmountUsedAfterPreConsume are used to compute remaining in logs.
	SubscriptionAmountTotal               int64
	SubscriptionAmountUsedAfterPreConsume int64
	IsClaudeBetaQuery                     bool // /v1/messages?beta=true
	IsChannelTest                         bool // channel test request
	RetryIndex                            int
	LastError                             *types.NewAPIError
	RuntimeHeadersOverride                map[string]interface{}
	UseRuntimeHeadersOverride             bool
	ParamOverrideAudit                    []string

	// UpstreamRequestBodySize is the byte size of the marshaled upstream request
	// body. It is set when the body is wrapped in a BodyStorage (see
	// relay/common/outbound_body.go), so that DoApiRequest can populate
	// http.Request.ContentLength manually (net/http only auto-detects it for
	// *bytes.Reader/Buffer/strings.Reader). 0 means "let net/http decide".
	UpstreamRequestBodySize int64

	PriceData types.PriceData

	// QuotaClamp is set (non-nil) when a quota conversion saturated at the
	// int32 bound (or NaN fallback) while computing this request's charge.
	// It is surfaced onto the consume/task log's admin_info for auditing.
	QuotaClamp *common.QuotaClamp
	// MaxServerToolReservationQuota is the largest conservative server-tool
	// component included in a preflight reservation across channel retries. It
	// is not actual usage and must be excluded from invalid-usage fallback
	// settlement; verified response counters are charged separately.
	MaxServerToolReservationQuota int
	// FinalRequestEstimateReady indicates that PriceData and any tiered snapshot
	// describe the exact outbound request for the successful attempt. This
	// prevents a larger reservation retained from an earlier retry from becoming
	// the fallback charge when the final provider returns malformed usage.
	FinalRequestEstimateReady bool

	// TieredBillingSnapshot is a frozen snapshot of tiered billing rules
	// captured at pre-consume time. Non-nil only when billing mode is "tiered_expr".
	TieredBillingSnapshot *billingexpr.BillingSnapshot
	BillingRequestInput   *billingexpr.RequestInput

	Request dto.Request

	// RequestConversionChain records request format conversions in order, e.g.
	// ["openai", "openai_responses"] or ["openai", "claude"].
	RequestConversionChain []types.RelayFormat
	// EffectiveClaudeWebSearchMaxUses preserves the max_uses limit in the
	// converted Claude request so settlement can validate provider-reported
	// server-tool usage against the request that was actually sent upstream.
	EffectiveClaudeWebSearchMaxUses *uint
	// 最终请求到上游的格式。可由 adaptor 显式设置；
	// 若为空，调用 GetFinalRequestRelayFormat 会回退到 RequestConversionChain 的最后一项或 RelayFormat。
	FinalRequestRelayFormat types.RelayFormat

	StreamStatus *StreamStatus

	// RelayCancelCtx/RelayCancelFunc allow admin-initiated termination of active requests.
	RelayCancelCtx  context.Context
	RelayCancelFunc context.CancelFunc
	// OnOutputChunk is called each time an SSE chunk is sent to the client (streaming only).
	OnOutputChunk func()

	ThinkingContentInfo
	TokenCountMeta
	*ClaudeConvertInfo
	*RerankerInfo
	*ResponsesUsageInfo
	*ChannelMeta
	*TaskRelayInfo
}

// GetRelayContext returns the cancellation context owned by the relay request,
// falling back to the caller-provided context for non-relay usages.
func (info *RelayInfo) GetRelayContext(fallback context.Context) context.Context {
	if info != nil && info.RelayCancelCtx != nil {
		return info.RelayCancelCtx
	}
	if fallback != nil {
		return fallback
	}
	return context.Background()
}

func (info *RelayInfo) InitChannelMeta(c *gin.Context) {
	channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
	paramOverride := common.GetContextKeyStringMap(c, constant.ContextKeyChannelParamOverride)
	headerOverride := common.GetContextKeyStringMap(c, constant.ContextKeyChannelHeaderOverride)
	apiType, _ := common.ChannelType2APIType(channelType)
	channelMeta := &ChannelMeta{
		ChannelType:          channelType,
		ChannelId:            common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		ChannelIsMultiKey:    common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey),
		ChannelMultiKeyIndex: common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex),
		ChannelBaseUrl:       common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl),
		ApiType:              apiType,
		ApiVersion:           c.GetString("api_version"),
		ApiKey:               common.GetContextKeyString(c, constant.ContextKeyChannelKey),
		Organization:         c.GetString("channel_organization"),
		ChannelCreateTime:    c.GetInt64("channel_create_time"),
		ParamOverride:        paramOverride,
		HeadersOverride:      headerOverride,
		UpstreamModelName:    common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
		IsModelMapped:        false,
		SupportStreamOptions: false,
	}

	if channelType == constant.ChannelTypeAzure {
		channelMeta.ApiVersion = GetAPIVersion(c)
	}
	if channelType == constant.ChannelTypeVertexAi {
		channelMeta.ApiVersion = c.GetString("region")
	}

	channelSetting, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if ok {
		channelMeta.ChannelSetting = channelSetting
	}

	channelOtherSettings, ok := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	if ok {
		channelMeta.ChannelOtherSettings = channelOtherSettings
	}

	if streamSupportedChannels[channelMeta.ChannelType] {
		channelMeta.SupportStreamOptions = true
	}

	info.ChannelMeta = channelMeta

	// reset some fields based on channel meta
	// 重置某些字段，例如模型名称等
	if info.Request != nil {
		info.Request.SetModelName(info.OriginModelName)
	}
}

func (info *RelayInfo) ToString() string {
	if info == nil {
		return "RelayInfo<nil>"
	}

	// Basic info
	b := &strings.Builder{}
	fmt.Fprintf(b, "RelayInfo{ ")
	fmt.Fprintf(b, "RelayFormat: %s, ", info.RelayFormat)
	fmt.Fprintf(b, "RelayMode: %d, ", info.RelayMode)
	fmt.Fprintf(b, "IsStream: %t, ", info.IsStream)
	fmt.Fprintf(b, "IsPlayground: %t, ", info.IsPlayground)
	fmt.Fprintf(b, "RequestURLPath: %q, ", info.RequestURLPath)
	fmt.Fprintf(b, "OriginModelName: %q, ", info.OriginModelName)
	fmt.Fprintf(b, "EstimatePromptTokens: %d, ", info.estimatePromptTokens)
	fmt.Fprintf(b, "ShouldIncludeUsage: %t, ", info.ShouldIncludeUsage)
	fmt.Fprintf(b, "DisablePing: %t, ", info.DisablePing)
	fmt.Fprintf(b, "SendResponseCount: %d, ", info.SendResponseCount)
	fmt.Fprintf(b, "FinalPreConsumedQuota: %d, ", info.FinalPreConsumedQuota)

	// User & token info (mask secrets)
	fmt.Fprintf(b, "User{ Id: %d, Email: %q, Group: %q, UsingGroup: %q, Quota: %d }, ",
		info.UserId, common.MaskEmail(info.UserEmail), info.UserGroup, info.UsingGroup, info.UserQuota)
	fmt.Fprintf(b, "Token{ Id: %d, Unlimited: %t, Key: ***masked*** }, ", info.TokenId, info.TokenUnlimited)

	// Time info
	latencyMs := info.FirstResponseTime.Sub(info.StartTime).Milliseconds()
	fmt.Fprintf(b, "Timing{ Start: %s, FirstResponse: %s, LatencyMs: %d }, ",
		info.StartTime.Format(time.RFC3339Nano), info.FirstResponseTime.Format(time.RFC3339Nano), latencyMs)

	// Audio / realtime
	if info.InputAudioFormat != "" || info.OutputAudioFormat != "" || len(info.RealtimeTools) > 0 || info.AudioUsage {
		fmt.Fprintf(b, "Realtime{ AudioUsage: %t, InFmt: %q, OutFmt: %q, Tools: %d }, ",
			info.AudioUsage, info.InputAudioFormat, info.OutputAudioFormat, len(info.RealtimeTools))
	}

	// Reasoning
	if info.ReasoningEffort != "" {
		fmt.Fprintf(b, "ReasoningEffort: %q, ", info.ReasoningEffort)
	}

	// Price data (non-sensitive)
	if info.PriceData.UsePrice {
		fmt.Fprintf(b, "PriceData{ %s }, ", info.PriceData.ToSetting())
	}

	// Channel metadata (mask ApiKey)
	if info.ChannelMeta != nil {
		cm := info.ChannelMeta
		fmt.Fprintf(b, "ChannelMeta{ Type: %d, Id: %d, IsMultiKey: %t, MultiKeyIndex: %d, BaseURL: %q, ApiType: %d, ApiVersion: %q, Organization: %q, CreateTime: %d, UpstreamModelName: %q, IsModelMapped: %t, SupportStreamOptions: %t, ApiKey: ***masked*** }, ",
			cm.ChannelType, cm.ChannelId, cm.ChannelIsMultiKey, cm.ChannelMultiKeyIndex, SanitizeURLForLog(cm.ChannelBaseUrl), cm.ApiType, cm.ApiVersion, cm.Organization, cm.ChannelCreateTime, cm.UpstreamModelName, cm.IsModelMapped, cm.SupportStreamOptions)
	}

	// Responses usage info (non-sensitive)
	if info.ResponsesUsageInfo != nil && len(info.ResponsesUsageInfo.BuiltInTools) > 0 {
		fmt.Fprintf(b, "ResponsesTools{ ")
		first := true
		for name, tool := range info.ResponsesUsageInfo.BuiltInTools {
			if !first {
				fmt.Fprintf(b, ", ")
			}
			first = false
			if tool != nil {
				fmt.Fprintf(b, "%s: calls=%d", name, tool.CallCount)
			} else {
				fmt.Fprintf(b, "%s: calls=0", name)
			}
		}
		fmt.Fprintf(b, " }, ")
	}

	fmt.Fprintf(b, "}")
	return b.String()
}

// 定义支持流式选项的通道类型
var streamSupportedChannels = map[int]bool{
	constant.ChannelTypeOpenAI:         true,
	constant.ChannelTypeAnthropic:      true,
	constant.ChannelTypeAws:            true,
	constant.ChannelTypeGemini:         true,
	constant.ChannelCloudflare:         true,
	constant.ChannelTypeAzure:          true,
	constant.ChannelTypeVolcEngine:     true,
	constant.ChannelTypeOllama:         true,
	constant.ChannelTypeXai:            true,
	constant.ChannelTypeDeepSeek:       true,
	constant.ChannelTypeBaiduV2:        true,
	constant.ChannelTypeZhipu_v4:       true,
	constant.ChannelTypeAli:            true,
	constant.ChannelTypeSubmodel:       true,
	constant.ChannelTypeCodex:          true,
	constant.ChannelTypeMoonshot:       true,
	constant.ChannelTypeMiniMax:        true,
	constant.ChannelTypeSiliconFlow:    true,
	constant.ChannelTypeAdvancedCustom: true,
}

func GenRelayInfoWs(c *gin.Context, ws *websocket.Conn) *RelayInfo {
	info := genBaseRelayInfo(c, nil)
	info.RelayFormat = types.RelayFormatOpenAIRealtime
	info.ClientWs = ws
	info.InputAudioFormat = "pcm16"
	info.OutputAudioFormat = "pcm16"
	info.IsFirstRequest = true
	return info
}

func GenRelayInfoClaude(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatClaude
	info.ShouldIncludeUsage = false
	info.ClaudeConvertInfo = &ClaudeConvertInfo{
		LastMessagesType: LastMessageTypeNone,
	}
	info.IsClaudeBetaQuery = c.Query("beta") == "true"
	return info
}

func GenRelayInfoRerank(c *gin.Context, request *dto.RerankRequest) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayMode = relayconstant.RelayModeRerank
	info.RelayFormat = types.RelayFormatRerank
	info.RerankerInfo = &RerankerInfo{
		Documents:       request.Documents,
		ReturnDocuments: request.GetReturnDocuments(),
	}
	return info
}

func GenRelayInfoOpenAIAudio(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatOpenAIAudio
	return info
}

func GenRelayInfoEmbedding(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatEmbedding
	return info
}

func GenRelayInfoResponses(c *gin.Context, request *dto.OpenAIResponsesRequest) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayMode = relayconstant.RelayModeResponses
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.ResponsesUsageInfo = NewResponsesUsageInfo(request)
	return info
}

// NewResponsesUsageInfo captures the billable tool definitions from the
// request that will actually be sent upstream. Callers rebuild this after
// channel parameter overrides so response call counters cannot be associated
// with a stale client request.
func NewResponsesUsageInfo(request *dto.OpenAIResponsesRequest) *ResponsesUsageInfo {
	usageInfo := &ResponsesUsageInfo{
		BuiltInTools:      make(map[string]*BuildInToolInfo),
		SeenOutputItems:   make(map[string]struct{}),
		SeenPartialImages: make(map[string]struct{}),
	}
	if request != nil && request.MaxToolCalls != nil {
		maxToolCalls := *request.MaxToolCalls
		usageInfo.MaxToolCalls = &maxToolCalls
	}
	if request != nil && len(request.Tools) > 0 {
		for _, tool := range request.GetToolsMap() {
			toolType := common.Interface2String(tool["type"])
			if toolType == "" {
				continue
			}
			usageInfo.BuiltInTools[toolType] = &BuildInToolInfo{
				ToolName:  toolType,
				CallCount: 0,
			}
			if _, isWebSearch := dto.ResponsesWebSearchPricingKey(toolType); isWebSearch {
				searchContextSize := common.Interface2String(tool["search_context_size"])
				if searchContextSize == "" {
					searchContextSize = "medium"
				}
				usageInfo.BuiltInTools[toolType].SearchContextSize = searchContextSize
			}
			if toolType == "image_generation" {
				imageTool := usageInfo.BuiltInTools[toolType]
				imageTool.ImageModel = common.Interface2String(tool["model"])
				if imageTool.ImageModel == "" {
					imageTool.ImageModel = "gpt-image-1"
				}
				imageTool.ImageQuality = common.Interface2String(tool["quality"])
				if imageTool.ImageQuality == "" {
					imageTool.ImageQuality = "auto"
				}
				imageTool.ImageSize = common.Interface2String(tool["size"])
				if imageTool.ImageSize == "" {
					imageTool.ImageSize = "auto"
				}
				if partial, ok := tool["partial_images"].(float64); ok &&
					partial >= 0 && partial <= dto.MaxOpenAIImagePartialImages {
					imageTool.ImagePartialImages = int(partial)
				}
			}
		}
	}
	return usageInfo
}

func GenRelayInfoGemini(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatGemini
	info.ShouldIncludeUsage = false

	return info
}

func GenRelayInfoImage(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatOpenAIImage
	return info
}

func GenRelayInfoOpenAI(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatOpenAI
	return info
}

func genBaseRelayInfo(c *gin.Context, request dto.Request) *RelayInfo {

	//channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
	//channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	//paramOverride := common.GetContextKeyStringMap(c, constant.ContextKeyChannelParamOverride)

	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	// 当令牌分组为空时，表示使用用户分组
	if tokenGroup == "" {
		tokenGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}

	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}

	isStream := false

	if request != nil {
		isStream = request.IsStream(c)
	}
	c.Set(string(constant.ContextKeyIsStream), isStream)

	// firstResponseTime = time.Now() - 1 second

	reqId := common.GetContextKeyString(c, common.RequestIdKey)
	if reqId == "" {
		reqId = common.NewRequestId()
	}
	info := &RelayInfo{
		Request: request,

		RequestId:  reqId,
		UserId:     common.GetContextKeyInt(c, constant.ContextKeyUserId),
		UsingGroup: common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		UserGroup:  common.GetContextKeyString(c, constant.ContextKeyUserGroup),
		UserQuota:  common.GetContextKeyInt(c, constant.ContextKeyUserQuota),
		UserEmail:  common.GetContextKeyString(c, constant.ContextKeyUserEmail),

		OriginModelName: common.GetContextKeyString(c, constant.ContextKeyOriginalModel),

		TokenId:        common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		TokenKey:       common.GetContextKeyString(c, constant.ContextKeyTokenKey),
		TokenUnlimited: common.GetContextKeyBool(c, constant.ContextKeyTokenUnlimited),
		TokenGroup:     tokenGroup,

		isFirstResponse: true,
		RelayMode:       relayconstant.Path2RelayMode(c.Request.URL.Path),
		RequestURLPath:  c.Request.URL.String(),
		RequestHeaders:  cloneRequestHeaders(c),
		IsStream:        isStream,

		StartTime:         startTime,
		FirstResponseTime: startTime.Add(-time.Second),
		ThinkingContentInfo: ThinkingContentInfo{
			IsFirstThinkingContent:  true,
			SendLastThinkingContent: false,
		},
		TokenCountMeta: TokenCountMeta{
			//promptTokens: common.GetContextKeyInt(c, constant.ContextKeyPromptTokens),
			estimatePromptTokens: common.GetContextKeyInt(c, constant.ContextKeyEstimatedTokens),
		},
	}

	if info.RelayMode == relayconstant.RelayModeUnknown {
		info.RelayMode = c.GetInt("relay_mode")
	}

	if strings.HasPrefix(c.Request.URL.Path, "/pg") {
		info.IsPlayground = true
		info.RequestURLPath = strings.TrimPrefix(info.RequestURLPath, "/pg")
		info.RequestURLPath = "/v1" + info.RequestURLPath
	}

	userSetting, ok := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting)
	if ok {
		info.UserSetting = userSetting
	}

	return info
}

func cloneRequestHeaders(c *gin.Context) map[string]string {
	if c == nil || c.Request == nil {
		return nil
	}
	if len(c.Request.Header) == 0 {
		return nil
	}
	headers := make(map[string]string, len(c.Request.Header))
	for key := range c.Request.Header {
		value := strings.TrimSpace(c.Request.Header.Get(key))
		if value == "" {
			continue
		}
		headers[key] = value
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func GenRelayInfo(c *gin.Context, relayFormat types.RelayFormat, request dto.Request, ws *websocket.Conn) (*RelayInfo, error) {
	var info *RelayInfo
	var err error
	switch relayFormat {
	case types.RelayFormatOpenAI:
		info = GenRelayInfoOpenAI(c, request)
	case types.RelayFormatOpenAIAudio:
		info = GenRelayInfoOpenAIAudio(c, request)
	case types.RelayFormatOpenAIImage:
		info = GenRelayInfoImage(c, request)
	case types.RelayFormatOpenAIRealtime:
		info = GenRelayInfoWs(c, ws)
	case types.RelayFormatClaude:
		info = GenRelayInfoClaude(c, request)
	case types.RelayFormatRerank:
		if request, ok := request.(*dto.RerankRequest); ok {
			info = GenRelayInfoRerank(c, request)
			break
		}
		err = errors.New("request is not a RerankRequest")
	case types.RelayFormatGemini:
		info = GenRelayInfoGemini(c, request)
	case types.RelayFormatEmbedding:
		info = GenRelayInfoEmbedding(c, request)
	case types.RelayFormatOpenAIResponses:
		if request, ok := request.(*dto.OpenAIResponsesRequest); ok {
			info = GenRelayInfoResponses(c, request)
			break
		}
		err = errors.New("request is not a OpenAIResponsesRequest")
	case types.RelayFormatOpenAIResponsesCompaction:
		if request, ok := request.(*dto.OpenAIResponsesCompactionRequest); ok {
			return GenRelayInfoResponsesCompaction(c, request), nil
		}
		return nil, errors.New("request is not a OpenAIResponsesCompactionRequest")
	case types.RelayFormatTask:
		info = genBaseRelayInfo(c, nil)
		info.TaskRelayInfo = &TaskRelayInfo{}
	case types.RelayFormatMjProxy:
		info = genBaseRelayInfo(c, nil)
		info.TaskRelayInfo = &TaskRelayInfo{}
	default:
		err = errors.New("invalid relay format")
	}

	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, errors.New("failed to build relay info")
	}

	info.InitRequestConversionChain()
	return info, nil
}

func (info *RelayInfo) InitRequestConversionChain() {
	if info == nil {
		return
	}
	if len(info.RequestConversionChain) > 0 {
		return
	}
	if info.RelayFormat == "" {
		return
	}
	info.RequestConversionChain = []types.RelayFormat{info.RelayFormat}
}

func (info *RelayInfo) AppendRequestConversion(format types.RelayFormat) {
	if info == nil {
		return
	}
	if format == "" {
		return
	}
	if len(info.RequestConversionChain) == 0 {
		info.RequestConversionChain = []types.RelayFormat{format}
		return
	}
	last := info.RequestConversionChain[len(info.RequestConversionChain)-1]
	if last == format {
		return
	}
	info.RequestConversionChain = append(info.RequestConversionChain, format)
}

func (info *RelayInfo) GetFinalRequestRelayFormat() types.RelayFormat {
	if info == nil {
		return ""
	}
	if info.FinalRequestRelayFormat != "" {
		return info.FinalRequestRelayFormat
	}
	if n := len(info.RequestConversionChain); n > 0 {
		return info.RequestConversionChain[n-1]
	}
	return info.RelayFormat
}

func GenRelayInfoResponsesCompaction(c *gin.Context, request *dto.OpenAIResponsesCompactionRequest) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	if info.RelayMode == relayconstant.RelayModeUnknown {
		info.RelayMode = relayconstant.RelayModeResponsesCompact
	}
	info.RelayFormat = types.RelayFormatOpenAIResponsesCompaction
	return info
}

//func (info *RelayInfo) SetPromptTokens(promptTokens int) {
//	info.promptTokens = promptTokens
//}

func (info *RelayInfo) SetEstimatePromptTokens(promptTokens int) {
	info.estimatePromptTokens = promptTokens
}

func (info *RelayInfo) GetEstimatePromptTokens() int {
	return info.estimatePromptTokens
}

func (info *RelayInfo) SetFirstResponseTime() {
	if info.isFirstResponse {
		info.FirstResponseTime = time.Now()
		info.isFirstResponse = false
	}
}

func (info *RelayInfo) HasSendResponse() bool {
	return info.FirstResponseTime.After(info.StartTime)
}

type TaskRelayInfo struct {
	Action       string
	OriginTaskID string
	// PublicTaskID 是提交时预生成的 task_xxxx 格式公开 ID，
	// 供 DoResponse 在返回给客户端时使用（避免暴露上游真实 ID）。
	PublicTaskID string

	ConsumeQuota bool

	// LockedChannel holds the full channel object when the request is bound to
	// a specific channel (e.g., remix on origin task's channel). Stored as any
	// to avoid an import cycle with model; callers type-assert to *model.Channel.
	LockedChannel any
	// LockedChannelKey pins a remix to the exact provider account that owns the
	// origin task. Provider task IDs are generally account-scoped, so rotating
	// to another key on the same logical channel cannot safely service a remix.
	LockedChannelKey       string
	LockedChannelKeyIndex  int
	LockedChannelKeyPinned bool
	// OriginTaskUpstreamModelName detects a changed channel model mapping before
	// a remix can be submitted and billed under a different upstream model.
	OriginTaskUpstreamModelName string

	// OriginTaskBillingRatios is the immutable billing input copied from the
	// origin task before the retry loop starts. Each submission attempt rebuilds
	// PriceData from this snapshot so provider-specific ratios left by an
	// earlier failed attempt cannot leak into a later retry.
	OriginTaskBillingRatios            map[string]float64
	OriginTaskBillingRatiosInitialized bool
}

type TaskSubmitReq struct {
	Prompt         string                 `json:"prompt"`
	Model          string                 `json:"model,omitempty"`
	Mode           string                 `json:"mode,omitempty"`
	Image          string                 `json:"image,omitempty"`
	Images         []string               `json:"images,omitempty"`
	Size           string                 `json:"size,omitempty"`
	Duration       int                    `json:"duration,omitempty"`
	Seconds        string                 `json:"seconds,omitempty"`
	InputReference string                 `json:"input_reference,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

func (t *TaskSubmitReq) GetPrompt() string {
	return t.Prompt
}

func (t *TaskSubmitReq) HasImage() bool {
	return len(t.Images) > 0
}

func (t *TaskSubmitReq) UnmarshalJSON(data []byte) error {
	type Alias TaskSubmitReq
	aux := &struct {
		Metadata json.RawMessage `json:"metadata,omitempty"`
		Duration json.RawMessage `json:"duration,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}

	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.Duration) > 0 {
		if strings.TrimSpace(string(aux.Duration)) == "null" {
			aux.Duration = nil
		} else {
			var durationInt int
			if err := common.Unmarshal(aux.Duration, &durationInt); err == nil {
				t.Duration = durationInt
			} else {
				var durationStr string
				if err := common.Unmarshal(aux.Duration, &durationStr); err != nil || strings.TrimSpace(durationStr) == "" {
					return fmt.Errorf("duration must be an integer or integer string")
				}
				value, err := strconv.Atoi(durationStr)
				if err != nil {
					return fmt.Errorf("duration must be an integer or integer string: %w", err)
				}
				t.Duration = value
			}
		}
	}

	if len(aux.Metadata) > 0 {
		if strings.TrimSpace(string(aux.Metadata)) == "null" {
			t.Metadata = nil
			return nil
		}
		var metadataStr string
		if err := common.Unmarshal(aux.Metadata, &metadataStr); err == nil {
			if strings.TrimSpace(metadataStr) == "" {
				t.Metadata = nil
				return nil
			}
			var metadataObj map[string]interface{}
			if err := common.Unmarshal([]byte(metadataStr), &metadataObj); err != nil {
				return fmt.Errorf("metadata string must contain a JSON object: %w", err)
			}
			if metadataObj == nil {
				return fmt.Errorf("metadata string must contain a JSON object")
			}
			t.Metadata = metadataObj
			return nil
		}

		var metadataObj map[string]interface{}
		if err := common.Unmarshal(aux.Metadata, &metadataObj); err != nil {
			return fmt.Errorf("metadata must be a JSON object or an encoded JSON object: %w", err)
		}
		if metadataObj == nil {
			return fmt.Errorf("metadata must be a JSON object")
		}
		t.Metadata = metadataObj
	}

	return nil
}
func (t *TaskSubmitReq) UnmarshalMetadata(v any) error {
	metadata := t.Metadata
	if metadata != nil {
		metadataBytes, err := common.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata failed: %w", err)
		}
		err = common.Unmarshal(metadataBytes, v)
		if err != nil {
			return fmt.Errorf("unmarshal metadata to target failed: %w", err)
		}
	}
	return nil
}

type TaskInfo struct {
	Code             int    `json:"code"`
	TaskID           string `json:"task_id"`
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
	Url              string `json:"url,omitempty"`
	RemoteUrl        string `json:"remote_url,omitempty"`
	Progress         string `json:"progress,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"` // 用于按倍率计费
	TotalTokens      int    `json:"total_tokens,omitempty"`      // 用于按倍率计费
}

func FailTaskInfo(reason string) *TaskInfo {
	return &TaskInfo{
		Status: "FAILURE",
		Reason: reason,
	}
}

// RemoveDisabledFields 从请求 JSON 数据中移除渠道设置中禁用的字段
// service_tier/serviceTier: 服务层级字段，可能导致额外计费（OpenAI、Claude、Responses、Gemini API 支持）
// inference_geo: Claude 数据驻留推理区域字段（仅 Claude 支持，默认过滤）
// speed: Claude 推理速度模式字段（仅 Claude 支持，默认过滤）
// store: 数据存储授权字段，涉及用户隐私（OpenAI、Responses、Gemini API 支持，默认允许透传，禁用后可能导致 Codex 无法使用）
// safety_identifier: 安全标识符，用于向 OpenAI 报告违规用户（仅 OpenAI 支持，涉及用户隐私）
// stream_options.include_obfuscation: 响应流混淆控制字段（仅 OpenAI Responses API 支持）
func RemoveDisabledFields(jsonData []byte, channelOtherSettings dto.ChannelOtherSettings, channelPassThroughEnabled bool) ([]byte, error) {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || channelPassThroughEnabled {
		return jsonData, nil
	}
	if !hasRemovableDisabledField(jsonData, channelOtherSettings) {
		return jsonData, nil
	}

	var data map[string]interface{}
	if err := common.Unmarshal(jsonData, &data); err != nil {
		common.SysError("RemoveDisabledFields Unmarshal error :" + err.Error())
		return jsonData, nil
	}

	// 默认移除 service_tier，除非明确允许（避免额外计费风险）
	if !channelOtherSettings.AllowServiceTier {
		if _, exists := data["service_tier"]; exists {
			delete(data, "service_tier")
		}
		if _, exists := data["serviceTier"]; exists {
			delete(data, "serviceTier")
		}
	}

	// 默认移除 inference_geo，除非明确允许（避免在未授权情况下透传数据驻留区域）
	if !channelOtherSettings.AllowInferenceGeo {
		if _, exists := data["inference_geo"]; exists {
			delete(data, "inference_geo")
		}
	}

	// 默认移除 speed，除非明确允许（避免意外切换 Claude 推理速度模式）
	if !channelOtherSettings.AllowSpeed {
		if _, exists := data["speed"]; exists {
			delete(data, "speed")
		}
	}

	// 默认允许 store 透传，除非明确禁用（禁用可能影响 Codex 使用）
	if channelOtherSettings.DisableStore {
		if _, exists := data["store"]; exists {
			delete(data, "store")
		}
	}

	// 默认移除 safety_identifier，除非明确允许（保护用户隐私，避免向 OpenAI 报告用户信息）
	if !channelOtherSettings.AllowSafetyIdentifier {
		if _, exists := data["safety_identifier"]; exists {
			delete(data, "safety_identifier")
		}
	}

	// 默认移除 stream_options.include_obfuscation，除非明确允许（避免关闭响应流混淆保护）
	if !channelOtherSettings.AllowIncludeObfuscation {
		if streamOptionsAny, exists := data["stream_options"]; exists {
			if streamOptions, ok := streamOptionsAny.(map[string]interface{}); ok {
				if _, includeExists := streamOptions["include_obfuscation"]; includeExists {
					delete(streamOptions, "include_obfuscation")
				}
				if len(streamOptions) == 0 {
					delete(data, "stream_options")
				} else {
					data["stream_options"] = streamOptions
				}
			}
		}
	}

	jsonDataAfter, err := common.Marshal(data)
	if err != nil {
		common.SysError("RemoveDisabledFields Marshal error :" + err.Error())
		return jsonData, nil
	}
	return jsonDataAfter, nil
}

func hasRemovableDisabledField(jsonData []byte, channelOtherSettings dto.ChannelOtherSettings) bool {
	values := gjson.GetManyBytes(
		jsonData,
		"service_tier",
		"inference_geo",
		"speed",
		"store",
		"safety_identifier",
		"stream_options.include_obfuscation",
		"serviceTier",
	)

	return (!channelOtherSettings.AllowServiceTier && (values[0].Exists() || values[6].Exists())) ||
		(!channelOtherSettings.AllowInferenceGeo && values[1].Exists()) ||
		(!channelOtherSettings.AllowSpeed && values[2].Exists()) ||
		(channelOtherSettings.DisableStore && values[3].Exists()) ||
		(!channelOtherSettings.AllowSafetyIdentifier && values[4].Exists()) ||
		(!channelOtherSettings.AllowIncludeObfuscation && values[5].Exists())
}

// RemoveGeminiDisabledFields removes disabled fields from Gemini request JSON data
// Currently supports removing functionResponse.id field which Vertex AI does not support
func RemoveGeminiDisabledFields(jsonData []byte) ([]byte, error) {
	if !model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled {
		return jsonData, nil
	}

	var data map[string]interface{}
	if err := common.Unmarshal(jsonData, &data); err != nil {
		common.SysError("RemoveGeminiDisabledFields Unmarshal error: " + err.Error())
		return jsonData, nil
	}

	// Process contents array
	// Handle both camelCase (functionResponse) and snake_case (function_response)
	if contents, ok := data["contents"].([]interface{}); ok {
		for _, content := range contents {
			if contentMap, ok := content.(map[string]interface{}); ok {
				if parts, ok := contentMap["parts"].([]interface{}); ok {
					for _, part := range parts {
						if partMap, ok := part.(map[string]interface{}); ok {
							// Check functionResponse (camelCase)
							if funcResp, ok := partMap["functionResponse"].(map[string]interface{}); ok {
								delete(funcResp, "id")
							}
							// Check function_response (snake_case)
							if funcResp, ok := partMap["function_response"].(map[string]interface{}); ok {
								delete(funcResp, "id")
							}
						}
					}
				}
			}
		}
	}

	jsonDataAfter, err := common.Marshal(data)
	if err != nil {
		common.SysError("RemoveGeminiDisabledFields Marshal error: " + err.Error())
		return jsonData, nil
	}
	return jsonDataAfter, nil
}
