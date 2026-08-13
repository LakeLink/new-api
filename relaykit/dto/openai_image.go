package dto

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// MaxImageN caps the image generation count. Without this bound a huge or
// wrapped-negative n overflows quota calculation into a negative charge.
const MaxImageN = 128

// MaxXAIImageN is xAI's documented image batch limit.
const MaxXAIImageN = 10

// MaxXAIImageEditInputs is xAI's documented multi-image edit limit.
const MaxXAIImageEditInputs = 3

// XAIImageBillingRatioKey identifies the complete xAI image request cost
// multiplier. It includes output count, output resolution, and edit inputs, so
// it must not be combined with the generic "n" multiplier.
const XAIImageBillingRatioKey = "xai_image_request"

// MaxSiliconFlowImageBatchSize is SiliconFlow's documented native
// batch_size limit. The generic image validator uses it because validation and
// pre-consume happen before channel selection, while batch_size is accepted as
// a top-level passthrough field.
const MaxSiliconFlowImageBatchSize = 4

type ImageRequest struct {
	Model             string          `json:"model"`
	Prompt            string          `json:"prompt" binding:"required"`
	N                 *uint           `json:"n,omitempty"`
	Size              string          `json:"size,omitempty"`
	Quality           string          `json:"quality,omitempty"`
	AspectRatio       *string         `json:"aspect_ratio,omitempty"`
	Resolution        *string         `json:"resolution,omitempty"`
	ResponseFormat    string          `json:"response_format,omitempty"`
	Style             json.RawMessage `json:"style,omitempty"`
	User              json.RawMessage `json:"user,omitempty"`
	ExtraFields       json.RawMessage `json:"extra_fields,omitempty"`
	Background        json.RawMessage `json:"background,omitempty"`
	Moderation        json.RawMessage `json:"moderation,omitempty"`
	OutputFormat      json.RawMessage `json:"output_format,omitempty"`
	OutputCompression json.RawMessage `json:"output_compression,omitempty"`
	PartialImages     json.RawMessage `json:"partial_images,omitempty"`
	Stream            *bool           `json:"stream,omitempty"`
	Images            json.RawMessage `json:"images,omitempty"`
	Mask              json.RawMessage `json:"mask,omitempty"`
	InputFidelity     json.RawMessage `json:"input_fidelity,omitempty"`
	StorageOptions    json.RawMessage `json:"storage_options,omitempty"`
	// InputImageCount is derived from validated JSON references or multipart
	// files. It is billing metadata and must never be forwarded upstream.
	InputImageCount int   `json:"-"`
	Watermark       *bool `json:"watermark,omitempty"`
	// zhipu 4v
	WatermarkEnabled json.RawMessage `json:"watermark_enabled,omitempty"`
	UserId           json.RawMessage `json:"user_id,omitempty"`
	Image            json.RawMessage `json:"image,omitempty"`
	// 用匿名参数接收额外参数
	Extra map[string]json.RawMessage `json:"-"`
}

func (i *ImageRequest) UnmarshalJSON(data []byte) error {
	// 先解析成 map[string]interface{}
	var rawMap map[string]json.RawMessage
	if err := kitutil.Unmarshal(data, &rawMap); err != nil {
		return err
	}

	// 用 struct tag 获取所有已定义字段名
	knownFields := GetJSONFieldNames(reflect.TypeOf(*i))

	// 再正常解析已定义字段
	type Alias ImageRequest
	var known Alias
	if err := kitutil.Unmarshal(data, &known); err != nil {
		return err
	}
	*i = ImageRequest(known)

	// 提取多余字段
	i.Extra = make(map[string]json.RawMessage)
	for k, v := range rawMap {
		if _, ok := knownFields[k]; !ok {
			i.Extra[k] = v
		}
	}
	return nil
}

// 序列化时需要重新把字段平铺
func (r ImageRequest) MarshalJSON() ([]byte, error) {
	// 将已定义字段转为 map
	type Alias ImageRequest
	alias := Alias(r)
	base, err := kitutil.Marshal(alias)
	if err != nil {
		return nil, err
	}

	var baseMap map[string]json.RawMessage
	if err := kitutil.Unmarshal(base, &baseMap); err != nil {
		return nil, err
	}

	// 不能合并ExtraFields！！！！！！！！
	// 合并 ExtraFields
	//for k, v := range r.Extra {
	//	if _, exists := baseMap[k]; !exists {
	//		baseMap[k] = v
	//	}
	//}

	return kitutil.Marshal(baseMap)
}

func GetJSONFieldNames(t reflect.Type) map[string]struct{} {
	fields := make(map[string]struct{})
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// 跳过匿名字段（例如 ExtraFields）
		if field.Anonymous {
			continue
		}

		tag := field.Tag.Get("json")
		if tag == "-" || tag == "" {
			continue
		}

		// 取逗号前字段名（排除 omitempty 等）
		name := tag
		if commaIdx := indexComma(tag); commaIdx != -1 {
			name = tag[:commaIdx]
		}
		fields[name] = struct{}{}
	}
	return fields
}

func indexComma(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return i
		}
	}
	return -1
}

func (i *ImageRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var sizeRatio = 1.0
	var qualityRatio = 1.0

	if strings.HasPrefix(i.Model, "dall-e") {
		// Size
		if i.Size == "256x256" {
			sizeRatio = 0.8
		} else if i.Size == "512x512" {
			sizeRatio = 0.9
		} else if i.Size == "1024x1024" {
			sizeRatio = 1
		} else if i.Size == "1024x1792" || i.Size == "1792x1024" {
			sizeRatio = 2
		}

		if i.Model == "dall-e-3" && i.Quality == "hd" {
			qualityRatio = 2.0
			if i.Size == "1024x1792" || i.Size == "1792x1024" {
				qualityRatio = 1.5
			}
		}
	}

	imageN := uint(1)
	if i.N != nil && *i.N > 0 {
		imageN = *i.N
	}
	maxTokens := 1584
	imageInputTokens := 0
	imageOutputTokens := 0
	if imageTokens, ok := OpenAIImageOutputTokens(i.Model, i.Quality, i.Size); ok {
		partialImages := 0
		if len(i.PartialImages) > 0 && string(i.PartialImages) != "null" {
			var parsed *uint
			if kitutil.Unmarshal(i.PartialImages, &parsed) == nil && parsed != nil &&
				*parsed <= MaxOpenAIImagePartialImages {
				partialImages = int(*parsed)
			}
		}
		perImageTokens := imageTokens + partialImages*OpenAIImagePartialOutputTokens
		if uint64(perImageTokens) <= uint64(^uint(0))/uint64(imageN) {
			maxTokens = perImageTokens * int(imageN)
			imageOutputTokens = maxTokens
		}
		if i.InputImageCount > 0 {
			inputFidelity := ""
			if len(i.InputFidelity) > 0 && string(i.InputFidelity) != "null" {
				_ = kitutil.Unmarshal(i.InputFidelity, &inputFidelity)
			}
			if perInputTokens, ok := OpenAIImageInputTokens(i.Model, inputFidelity); ok &&
				perInputTokens <= int(^uint(0)>>1)/i.InputImageCount {
				imageInputTokens = perInputTokens * i.InputImageCount
			}
		}
	}

	billingRatios := map[string]float64{"n": float64(imageN)}
	if IsXAIImageModel(i.Model) {
		outputRatio := 1.0
		inputRatio := 0.1
		if i.Model == "grok-imagine-image-quality" {
			inputRatio = 0.2
			if i.Resolution != nil && strings.EqualFold(strings.TrimSpace(*i.Resolution), "2k") {
				outputRatio = 1.4
			}
		}
		// xAI charges each output plus each edit input. Express the additive
		// provider prices as one multiplier over the model's built-in 1K output
		// price so fixed-price pre-consume reserves the complete request.
		billingRatios = map[string]float64{
			XAIImageBillingRatioKey: float64(imageN)*outputRatio + float64(i.InputImageCount)*inputRatio,
		}
	}

	// Keep output count separate from ImagePriceRatio so size/quality and count
	// remain independent billing dimensions. xAI is the exception because its
	// edit input fee is additive rather than multiplicative; the complete
	// provider-price ratio above represents that request exactly.
	return &types.TokenCountMeta{
		CombineText:       i.Prompt,
		MaxTokens:         maxTokens,
		ImagePriceRatio:   sizeRatio * qualityRatio,
		ImageInputTokens:  imageInputTokens,
		ImageOutputTokens: imageOutputTokens,
		BillingRatios:     billingRatios,
	}
}

func IsXAIImageModel(model string) bool {
	switch model {
	case "grok-imagine-image", "grok-imagine-image-quality":
		return true
	default:
		return false
	}
}

func (i *ImageRequest) IsStream(c *http.Request) bool {
	return i.Stream != nil && *i.Stream
}

func (i *ImageRequest) SetModelName(modelName string) {
	if modelName != "" {
		i.Model = modelName
	}
}

type ImageResponse struct {
	Data     []ImageData     `json:"data"`
	Created  int64           `json:"created"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}
type ImageData struct {
	Url           string `json:"url"`
	B64Json       string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt"`
}
