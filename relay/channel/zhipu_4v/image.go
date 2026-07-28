package zhipu_4v

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type zhipuImageRequest struct {
	Model            string `json:"model"`
	Prompt           string `json:"prompt"`
	Quality          string `json:"quality,omitempty"`
	Size             string `json:"size,omitempty"`
	WatermarkEnabled *bool  `json:"watermark_enabled,omitempty"`
	UserID           string `json:"user_id,omitempty"`
}

type zhipuImageResponse struct {
	Created       *int64            `json:"created,omitempty"`
	Data          []zhipuImageData  `json:"data,omitempty"`
	ContentFilter any               `json:"content_filter,omitempty"`
	Usage         *dto.Usage        `json:"usage,omitempty"`
	Error         *zhipuImageError  `json:"error,omitempty"`
	RequestID     string            `json:"request_id,omitempty"`
	ExtendParam   map[string]string `json:"extendParam,omitempty"`
}

type zhipuImageError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type zhipuImageData struct {
	Url      string `json:"url,omitempty"`
	ImageUrl string `json:"image_url,omitempty"`
	B64Json  string `json:"b64_json,omitempty"`
	B64Image string `json:"b64_image,omitempty"`
}

type openAIImagePayload struct {
	Created int64             `json:"created"`
	Data    []openAIImageData `json:"data"`
}

type openAIImageData struct {
	Url     string `json:"url,omitempty"`
	B64Json string `json:"b64_json,omitempty"`
}

func zhipu4vImageHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, responseFormat string) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var zhipuResp zhipuImageResponse
	if err := common.Unmarshal(responseBody, &zhipuResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if zhipuResp.Error != nil && zhipuResp.Error.Message != "" {
		return nil, types.WithOpenAIError(types.OpenAIError{
			Message: zhipuResp.Error.Message,
			Type:    "zhipu_image_error",
			Code:    zhipuResp.Error.Code,
		}, resp.StatusCode)
	}

	payload := openAIImagePayload{}
	if zhipuResp.Created != nil && *zhipuResp.Created != 0 {
		payload.Created = *zhipuResp.Created
	} else {
		payload.Created = info.StartTime.Unix()
	}
	for _, data := range zhipuResp.Data {
		url := data.Url
		if url == "" {
			url = data.ImageUrl
		}

		var b64 string
		if responseFormat == "b64_json" || url == "" {
			switch {
			case data.B64Json != "":
				b64 = data.B64Json
			case data.B64Image != "":
				b64 = data.B64Image
			case url != "":
				_, downloaded, err := service.GetImageFromUrl(url)
				if err != nil {
					return nil, types.NewOpenAIError(fmt.Errorf("zhipu image base64 conversion failed: %w", err), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
				}
				b64 = downloaded
			}
		}

		if url == "" && b64 == "" {
			logger.LogWarn(c, "zhipu_image_missing_data")
			return nil, types.NewOpenAIError(fmt.Errorf("zhipu image response contains no image data"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}

		imageData := openAIImageData{
			Url:     url,
			B64Json: b64,
		}
		if responseFormat == "b64_json" {
			imageData.Url = ""
		}
		payload.Data = append(payload.Data, imageData)
	}
	if info != nil && info.PriceData.UsePrice && len(payload.Data) > 0 {
		info.PriceData.AddOtherRatio("n", float64(len(payload.Data)))
	}

	jsonResp, err := common.Marshal(payload)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	service.IOCopyBytesGracefully(c, resp, jsonResp)

	return &dto.Usage{}, nil
}
