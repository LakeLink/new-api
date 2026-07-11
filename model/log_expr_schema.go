package model

import "github.com/QuantumNous/new-api/common"

type LogExprFieldDocumentation struct {
	Names         []string `json:"names"`
	Type          string   `json:"type"`
	Scope         string   `json:"scope"`
	Description   string   `json:"description"`
	DescriptionZh string   `json:"descriptionZh,omitempty"`
}

type LogExprOperatorDocumentation struct {
	Syntax        string `json:"syntax"`
	Description   string `json:"description"`
	DescriptionZh string `json:"descriptionZh,omitempty"`
}

type LogExprExampleDocumentation struct {
	Title         string `json:"title"`
	TitleZh       string `json:"titleZh,omitempty"`
	Expression    string `json:"expression"`
	Description   string `json:"description"`
	DescriptionZh string `json:"descriptionZh,omitempty"`
	Scope         string `json:"scope,omitempty"`
}

type LogExprLimitsDocumentation struct {
	MaxLength       int `json:"maxLength"`
	MaxNodes        int `json:"maxNodes"`
	MaxStringLength int `json:"maxStringLength"`
	MaxInItems      int `json:"maxInItems"`
}

type LogExprSchema struct {
	Intro         string                         `json:"intro"`
	IntroZh       string                         `json:"introZh,omitempty"`
	QuickSyntax   string                         `json:"quickSyntax"`
	QuickSyntaxZh string                         `json:"quickSyntaxZh,omitempty"`
	Safety        string                         `json:"safety"`
	SafetyZh      string                         `json:"safetyZh,omitempty"`
	Fields        []LogExprFieldDocumentation    `json:"fields"`
	Operators     []LogExprOperatorDocumentation `json:"operators"`
	Examples      []LogExprExampleDocumentation  `json:"examples"`
	Limits        LogExprLimitsDocumentation     `json:"limits"`
}

func GetLogExprSchema(includeAdminFields bool) LogExprSchema {
	fields := []LogExprFieldDocumentation{
		{Names: []string{"id"}, Type: "Number", Scope: "admin", Description: "Log record ID.", DescriptionZh: "日志记录 ID。"},
		{Names: []string{"user_id"}, Type: "Number", Scope: "all", Description: "User ID that owns the log.", DescriptionZh: "拥有该日志的用户 ID。"},
		{Names: []string{"created_at", "createdAt", "timestamp"}, Type: "Number", Scope: "all", Description: "Creation time as Unix seconds; date(...) is also accepted.", DescriptionZh: "创建时间，支持 Unix 秒级时间戳或 date(...)。"},
		{Names: []string{"type", "log_type"}, Type: "Number", Scope: "all", Description: "Log type code: 1 top-up, 2 consume, 3 manage, 4 system, 5 error, 6 refund, 7 login.", DescriptionZh: "日志类型：1 充值，2 消费，3 管理，4 系统，5 错误，6 退款，7 登录。"},
		{Names: []string{"content"}, Type: "String", Scope: "all", Description: "Main log content or error message text.", DescriptionZh: "主要日志内容或错误消息文本。"},
		{Names: []string{"token_name", "token"}, Type: "String", Scope: "all", Description: "API token name recorded on the log.", DescriptionZh: "日志中记录的 API 令牌名称。"},
		{Names: []string{"model_name", "model"}, Type: "String", Scope: "all", Description: "Requested model name.", DescriptionZh: "请求的模型名称。"},
		{Names: []string{"quota"}, Type: "Number", Scope: "all", Description: "Charged quota amount.", DescriptionZh: "本次记录扣费额度。"},
		{Names: []string{"prompt_tokens"}, Type: "Number", Scope: "all", Description: "Prompt or input token count.", DescriptionZh: "提示词或输入 token 数。"},
		{Names: []string{"completion_tokens"}, Type: "Number", Scope: "all", Description: "Completion or output token count.", DescriptionZh: "补全或输出 token 数。"},
		{Names: []string{"use_time"}, Type: "Number", Scope: "all", Description: "Response time in seconds.", DescriptionZh: "响应耗时，单位为秒。"},
		{Names: []string{"is_stream", "stream"}, Type: "Boolean", Scope: "all", Description: "Whether the request used streaming.", DescriptionZh: "请求是否使用流式响应。"},
		{Names: []string{"today", "yesterday"}, Type: "Boolean", Scope: "all", Description: "Shortcut filters for the current or previous local day.", DescriptionZh: "当前或上一个本地自然日的快捷筛选条件。"},
		{Names: []string{"token_id"}, Type: "Number", Scope: "all", Description: "Numeric API token ID.", DescriptionZh: "API 令牌的数字 ID。"},
		{Names: []string{"group"}, Type: "String", Scope: "all", Description: "Billing or request group name.", DescriptionZh: "计费或请求分组名称。"},
		{Names: []string{"ip"}, Type: "String", Scope: "all", Description: "Client IP address if IP logging is enabled.", DescriptionZh: "开启 IP 记录后保存的客户端 IP 地址。"},
		{Names: []string{"request_id", "requestId"}, Type: "String", Scope: "all", Description: "Request ID for tracing one call.", DescriptionZh: "用于追踪单次调用的 Request ID。"},
		{Names: []string{"upstream_request_id", "upstreamRequestId"}, Type: "String", Scope: "all", Description: "Upstream provider request ID for tracing one call.", DescriptionZh: "用于追踪单次调用的上游提供商 Request ID。"},
		{Names: []string{"other"}, Type: "String", Scope: "admin", Description: "Additional metadata saved with the log.", DescriptionZh: "随日志保存的额外元数据。"},
		{Names: []string{"username"}, Type: "String", Scope: "admin", Description: "Username associated with the log.", DescriptionZh: "日志关联的用户名。"},
		{Names: []string{"channel", "channel_id"}, Type: "Number", Scope: "admin", Description: "Channel ID used by the request.", DescriptionZh: "请求使用的渠道 ID。"},
		{Names: []string{"channel_name", "channelName"}, Type: "String", Scope: "admin", Description: "Channel name used by the request.", DescriptionZh: "请求使用的渠道名称。"},
	}

	visibleFields := make([]LogExprFieldDocumentation, 0, len(fields))
	for _, field := range fields {
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) && field.Names[0] == "id" {
			continue
		}
		if !logExprCanJoinChannels() && field.Names[0] == "channel_name" {
			continue
		}
		if !includeAdminFields && field.Scope == "admin" {
			continue
		}
		visibleFields = append(visibleFields, field)
	}

	examples := []LogExprExampleDocumentation{
		{Title: "GPT consumption logs", TitleZh: "GPT 消费日志", Expression: `model_name contains "gpt" && type == 2`, Description: "Find consumption records for GPT-family models.", DescriptionZh: "查找 GPT 系列模型的消费记录。", Scope: "all"},
		{Title: "GPT logs today", TitleZh: "今日 GPT 日志", Expression: `model_name contains "gpt-5.5" and today`, Description: "Find matching model logs from the current local day.", DescriptionZh: "查找当前本地自然日内匹配该模型的日志。", Scope: "all"},
		{Title: "One day by date", TitleZh: "按一天日期筛选", Expression: `created_at >= date("2025-01-01") && created_at < date("2025-01-02")`, Description: "Use date(...) for readable day boundaries; add a timezone argument when needed.", DescriptionZh: "使用 date(...) 写可读日期；需要时可添加时区参数。", Scope: "all"},
		{Title: "High quota usage", TitleZh: "高额度消费", Expression: `quota > 1000 && type == 2`, Description: "Find expensive consumption records.", DescriptionZh: "查找额度消耗较高的消费记录。", Scope: "all"},
		{Title: "Large token requests", TitleZh: "大 token 请求", Expression: `prompt_tokens > 8000 || completion_tokens > 2000`, Description: "Find calls with unusually large input or output token counts.", DescriptionZh: "查找输入或输出 token 数异常大的调用。", Scope: "all"},
		{Title: "Streaming Claude calls", TitleZh: "流式 Claude 调用", Expression: `is_stream == true && model_name contains "claude"`, Description: "Find streamed requests for Claude-family models.", DescriptionZh: "查找 Claude 系列模型的流式请求。", Scope: "all"},
		{Title: "One request ID", TitleZh: "单个 Request ID", Expression: `request_id == "req_xxx"`, Description: "Jump to a single traced request.", DescriptionZh: "直接定位一条可追踪请求。", Scope: "all"},
		{Title: "Specific groups", TitleZh: "指定分组", Expression: `group in ["default", "vip"]`, Description: "Find records from one of several groups.", DescriptionZh: "查找属于多个分组之一的记录。", Scope: "all"},
		{Title: "Model families", TitleZh: "模型家族", Expression: `model_name startsWith "gpt-4" || model_name startsWith "claude"`, Description: "Compare several model prefixes in one search.", DescriptionZh: "在同一次搜索中比较多个模型前缀。", Scope: "all"},
		{Title: "Errors and rate limits", TitleZh: "错误和限流", Expression: `content contains "timeout" || other contains "429"`, Description: "Look for timeout messages or upstream rate-limit metadata.", DescriptionZh: "查找超时消息或上游限流元数据。", Scope: "admin"},
		{Title: "Exclude embeddings", TitleZh: "排除 Embedding", Expression: `not (model_name contains "embedding") && type == 2`, Description: "Keep normal consumption logs while hiding embedding calls.", DescriptionZh: "保留普通消费日志并隐藏 embedding 调用。", Scope: "all"},
		{Title: "Named tokens after a time", TitleZh: "某时间后的具名令牌", Expression: `token_name != "" && created_at >= date("2025-01-01")`, Description: "Find logs that have a token name after a readable date.", DescriptionZh: "查找某个可读日期之后带令牌名称的日志。", Scope: "all"},
		{Title: "One client IP", TitleZh: "单个客户端 IP", Expression: `ip == "1.2.3.4"`, Description: "Find requests recorded from one client IP address.", DescriptionZh: "查找来自某个客户端 IP 的请求。", Scope: "all"},
		{Title: "Admin: user on channel", TitleZh: "管理员：用户和渠道", Expression: `username == "alice" && channel == 12`, Description: "For admins, filter one user on a numeric channel ID.", DescriptionZh: "管理员可按用户名和数字渠道 ID 筛选。", Scope: "admin"},
		{Title: "Admin: channel name", TitleZh: "管理员：渠道名称", Expression: `channel_name contains "openai" && type == 2`, Description: "For admins, filter by channel name and log type.", DescriptionZh: "管理员可按渠道名称和日志类型筛选。", Scope: "admin"},
	}

	visibleExamples := make([]LogExprExampleDocumentation, 0, len(examples))
	for _, example := range examples {
		if !logExprCanJoinChannels() && example.Title == "Admin: channel name" {
			continue
		}
		if !includeAdminFields && example.Scope == "admin" {
			continue
		}
		visibleExamples = append(visibleExamples, example)
	}

	return LogExprSchema{
		Intro:         "Expression search is parsed from the AST and translated into SQL with an allowed field list, placeholders, and escaped LIKE patterns.",
		IntroZh:       "表达式搜索会从 AST 解析表达式，并使用允许字段列表、SQL 参数占位符和转义后的 LIKE 模式生成查询。",
		QuickSyntax:   "Strings use double quotes, numbers are integers, booleans are true or false, and nil checks null values. Boolean fields can be written directly, such as is_stream, or compared explicitly with true or false.",
		QuickSyntaxZh: "字符串使用双引号，数字为整数，布尔值为 true 或 false，nil 可用于空值判断。布尔字段可以直接写 is_stream，也可以显式比较 true 或 false。",
		Safety:        `Only listed fields are allowed. Literal values are bound as SQL parameters and LIKE wildcards are escaped. Regular expressions, arithmetic, arbitrary functions, and field-to-field comparisons are unsupported. Use parentheses to group logic, for example not (model_name contains "embedding") && type == 2.`,
		SafetyZh:      `仅允许使用列出的字段。字面量会绑定为 SQL 参数，LIKE 通配符会被转义。不支持正则表达式、算术运算、任意函数和字段间比较。使用括号组合逻辑，例如 not (model_name contains "embedding") && type == 2。`,
		Fields:        visibleFields,
		Operators: []LogExprOperatorDocumentation{
			{Syntax: "&&, ||, !, and, or, not", Description: "Combine conditions with boolean logic and parentheses.", DescriptionZh: "使用布尔逻辑和括号组合多个条件。"},
			{Syntax: "==, !=, >, >=, <, <=", Description: "Compare a field with a string, integer, boolean, nil, or date(...) literal.", DescriptionZh: "将字段与字符串、整数、布尔值、nil 或 date(...) 字面量比较。"},
			{Syntax: "contains, startsWith, endsWith", Description: "Match string fields with SQL LIKE using escaped wildcards.", DescriptionZh: "对字符串字段执行 SQL LIKE 匹配，并自动转义通配符。"},
			{Syntax: "in, not in", Description: "Match a field against a literal array with up to 100 values.", DescriptionZh: "将字段与最多 100 个字面量组成的数组进行匹配。"},
			{Syntax: "nil", Description: "Use nil only with == or != to check for null values.", DescriptionZh: "nil 只能与 == 或 != 一起用于空值判断。"},
		},
		Examples: visibleExamples,
		Limits: LogExprLimitsDocumentation{
			MaxLength:       maxLogExprLength,
			MaxNodes:        maxLogExprNodes,
			MaxStringLength: maxLogExprStringLength,
			MaxInItems:      maxLogExprInItems,
		},
	}
}
