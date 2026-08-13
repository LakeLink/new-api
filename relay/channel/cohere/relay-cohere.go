package cohere

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func requestOpenAI2Cohere(textRequest dto.GeneralOpenAIRequest) *CohereRequest {
	maxTokens := textRequest.GetMaxTokensPointer()
	if maxTokens == nil {
		defaultMaxTokens := uint(4000)
		maxTokens = &defaultMaxTokens
	}
	cohereReq := CohereRequest{
		Model:       textRequest.Model,
		ChatHistory: []ChatHistory{},
		Message:     "",
		Stream:      lo.FromPtrOr(textRequest.Stream, false),
		MaxTokens:   maxTokens,
	}
	if common.CohereSafetySetting != "NONE" {
		cohereReq.SafetyMode = common.CohereSafetySetting
	}
	for _, msg := range textRequest.Messages {
		if msg.Role == "user" {
			cohereReq.Message = msg.StringContent()
		} else {
			var role string
			if msg.Role == "assistant" {
				role = "CHATBOT"
			} else if msg.Role == "system" {
				role = "SYSTEM"
			} else {
				role = "USER"
			}
			cohereReq.ChatHistory = append(cohereReq.ChatHistory, ChatHistory{
				Role:    role,
				Message: msg.StringContent(),
			})
		}
	}

	return &cohereReq
}

func requestConvertRerank2Cohere(rerankRequest dto.RerankRequest) (*CohereRerankRequest, error) {
	query, ok := rerankRequest.QueryString()
	if !ok {
		return nil, errors.New("cohere rerank query must be a non-empty string")
	}
	if rerankRequest.TopN != nil && *rerankRequest.TopN <= 0 {
		return nil, errors.New("cohere rerank top_n must be at least 1")
	}
	if rerankRequest.MaxChunksPerDoc != nil && rerankRequest.MaxChunkPerDoc != nil &&
		*rerankRequest.MaxChunksPerDoc != *rerankRequest.MaxChunkPerDoc {
		return nil, errors.New("cohere rerank max_chunks_per_doc conflicts with legacy max_chunk_per_doc")
	}
	if _, err := relaycommon.EstimateCohereRerankSearchUnits(&rerankRequest); err != nil {
		return nil, err
	}
	cohereReq := CohereRerankRequest{
		Query:           query,
		Documents:       rerankRequest.Documents,
		Model:           rerankRequest.Model,
		TopN:            rerankRequest.TopN,
		ReturnDocuments: rerankRequest.ReturnDocuments,
		MaxChunksPerDoc: rerankRequest.GetMaxChunksPerDoc(),
	}
	return &cohereReq, nil
}

func stopReasonCohere2OpenAI(reason string) string {
	switch reason {
	case "COMPLETE":
		return "stop"
	case "MAX_TOKENS":
		return "max_tokens"
	default:
		return reason
	}
}

func cohereStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	createdTime := common.GetTimestamp()
	usage := &dto.Usage{}
	responseText := ""
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := strings.Index(string(data), "\n"); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	helper.SetEventStreamHeaders(c)
	isFirst := true
	c.Stream(func(w io.Writer) bool {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				common.SysLog("error reading stream: " + err.Error())
			}
			c.Render(-1, common.CustomEvent{Data: "data: [DONE]"})
			return false
		}

		if isFirst {
			isFirst = false
			info.FirstResponseTime = time.Now()
		}
		data := strings.TrimSuffix(scanner.Text(), "\r")
		var cohereResp CohereResponse
		err := common.Unmarshal([]byte(data), &cohereResp)
		if err != nil {
			common.SysLog("error unmarshalling stream response: " + err.Error())
			return true
		}
		var openaiResp dto.ChatCompletionsStreamResponse
		openaiResp.Id = responseId
		openaiResp.Created = createdTime
		openaiResp.Object = "chat.completion.chunk"
		openaiResp.Model = info.UpstreamModelName
		if cohereResp.IsFinished {
			finishReason := stopReasonCohere2OpenAI(cohereResp.FinishReason)
			openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{
				{
					Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
					Index:        0,
					FinishReason: &finishReason,
				},
			}
			if cohereResp.Response != nil {
				usage.PromptTokens = cohereResp.Response.Meta.BilledUnits.InputTokens
				usage.CompletionTokens = cohereResp.Response.Meta.BilledUnits.OutputTokens
			}
		} else {
			openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{
				{
					Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
						Role:    "assistant",
						Content: &cohereResp.Text,
					},
					Index: 0,
				},
			}
			responseText += cohereResp.Text
		}
		jsonStr, err := common.Marshal(openaiResp)
		if err != nil {
			common.SysLog("error marshalling stream response: " + err.Error())
			return true
		}
		c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonStr)})
		return true
	})
	if usage.PromptTokens == 0 {
		usage = service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	}
	return usage, nil
}

func cohereHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	createdTime := common.GetTimestamp()
	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var cohereResp CohereResponseResult
	err = common.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := dto.Usage{}
	usage.PromptTokens = cohereResp.Meta.BilledUnits.InputTokens
	usage.CompletionTokens = cohereResp.Meta.BilledUnits.OutputTokens
	usage.TotalTokens = cohereResp.Meta.BilledUnits.InputTokens + cohereResp.Meta.BilledUnits.OutputTokens

	var openaiResp dto.TextResponse
	openaiResp.Id = cohereResp.ResponseId
	openaiResp.Created = createdTime
	openaiResp.Object = "chat.completion"
	openaiResp.Model = info.UpstreamModelName
	openaiResp.Usage = usage

	openaiResp.Choices = []dto.OpenAITextResponseChoice{
		{
			Index:        0,
			Message:      dto.Message{Content: cohereResp.Text, Role: "assistant"},
			FinishReason: stopReasonCohere2OpenAI(cohereResp.FinishReason),
		},
	}

	jsonResponse, err := common.Marshal(openaiResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)
	return &usage, nil
}

func cohereRerankHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var cohereResp CohereRerankResponseResult
	err = common.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	searchUnits := cohereResp.Meta.BilledUnits.SearchUnits
	if searchUnits == nil {
		return nil, types.NewError(errors.New("cohere rerank response is missing billed_units.search_units"), types.ErrorCodeBadResponseBody)
	}
	if err := relaycommon.ValidateCohereRerankSearchUnits(*searchUnits); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if !info.PriceData.UsePrice {
		return nil, types.NewError(errors.New("cohere rerank requires a fixed price per search unit"), types.ErrorCodeModelPriceError)
	}
	info.CohereSearchUnits = searchUnits
	info.PriceData.AddOtherRatio(relaycommon.CohereRerankSearchUnitsRatioKey, *searchUnits)
	usage := dto.Usage{}
	if cohereResp.Meta.BilledUnits.InputTokens == 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
		usage.CompletionTokens = 0
		usage.TotalTokens = info.GetEstimatePromptTokens()
	} else {
		usage.PromptTokens = cohereResp.Meta.BilledUnits.InputTokens
		usage.CompletionTokens = cohereResp.Meta.BilledUnits.OutputTokens
		usage.TotalTokens = cohereResp.Meta.BilledUnits.InputTokens + cohereResp.Meta.BilledUnits.OutputTokens
	}

	var rerankResp dto.RerankResponse
	rerankResp.Results = cohereResp.Results
	rerankResp.Usage = usage

	jsonResponse, err := common.Marshal(rerankResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return &usage, nil
}
