package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime/multipart"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

const (
	openAIImageMaxFileBytes = int64(50 << 20)
	dallE2MaxFileBytes      = int64(4 << 20)
)

func imageRawProvided(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func decodeImageString(raw json.RawMessage, field string) (string, bool, error) {
	if !imageRawProvided(raw) {
		return "", false, nil
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return "", true, fmt.Errorf("%s must be a string", field)
	}
	return value, true, nil
}

func decodeImageUint(raw json.RawMessage, field string, max uint) (uint, bool, error) {
	if !imageRawProvided(raw) {
		return 0, false, nil
	}
	var value uint
	if err := common.Unmarshal(raw, &value); err != nil || value > max {
		return 0, true, fmt.Errorf("%s must be an integer between 0 and %d", field, max)
	}
	return value, true, nil
}

func validateImageEnum(value, field string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of: %s", field, strings.Join(allowed, ", "))
}

func validateOpenAIImageRef(raw json.RawMessage, field string) error {
	if common.GetJsonType(raw) != "object" {
		return fmt.Errorf("%s must be an object", field)
	}
	var ref map[string]json.RawMessage
	if err := common.Unmarshal(raw, &ref); err != nil {
		return fmt.Errorf("%s must be an object: %w", field, err)
	}
	for key := range ref {
		if key != "image_url" && key != "file_id" {
			return fmt.Errorf("%s contains unsupported field %q", field, key)
		}
	}
	imageURL, hasURL, err := decodeImageString(ref["image_url"], field+".image_url")
	if err != nil {
		return err
	}
	fileID, hasFileID, err := decodeImageString(ref["file_id"], field+".file_id")
	if err != nil {
		return err
	}
	if hasURL == hasFileID {
		return fmt.Errorf("%s must contain exactly one of image_url or file_id", field)
	}
	if (hasURL && strings.TrimSpace(imageURL) == "") || (hasFileID && strings.TrimSpace(fileID) == "") {
		return fmt.Errorf("%s image reference must not be empty", field)
	}
	return nil
}

func validateOpenAIImageRefs(raw json.RawMessage) (int, error) {
	if common.GetJsonType(raw) != "array" {
		return 0, errors.New("images must be an array")
	}
	var refs []json.RawMessage
	if err := common.Unmarshal(raw, &refs); err != nil {
		return 0, fmt.Errorf("images must be an array: %w", err)
	}
	if len(refs) < 1 || len(refs) > dto.MaxOpenAIImageEditInputs {
		return 0, fmt.Errorf("images must contain between 1 and %d image references", dto.MaxOpenAIImageEditInputs)
	}
	for index, ref := range refs {
		if err := validateOpenAIImageRef(ref, fmt.Sprintf("images[%d]", index)); err != nil {
			return 0, err
		}
	}
	return len(refs), nil
}

func responsesImageToolString(definition map[string]any, field string) (string, bool, error) {
	value, exists := definition[field]
	if !exists {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("%s must be a string", field)
	}
	return text, true, nil
}

func validateResponsesImageGenerationTool(definition map[string]any, index int, stream bool) error {
	allowed := map[string]bool{
		"type": true, "model": true, "action": true, "background": true,
		"input_fidelity": true, "input_image_mask": true, "moderation": true,
		"output_compression": true, "output_format": true, "partial_images": true,
		"quality": true, "size": true,
	}
	for field := range definition {
		if !allowed[field] {
			return fmt.Errorf("tools[%d] contains unsupported image_generation field %q", index, field)
		}
	}

	model, hasModel, err := responsesImageToolString(definition, "model")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if !hasModel {
		model = "gpt-image-1"
	}
	if !dto.IsOpenAIResponsesImageModel(model) {
		return fmt.Errorf("tools[%d].model is not a supported GPT Image model", index)
	}
	action, hasAction, err := responsesImageToolString(definition, "action")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if hasAction {
		if err := validateImageEnum(action, fmt.Sprintf("tools[%d].action", index), "auto", "generate", "edit"); err != nil {
			return err
		}
	}
	background, hasBackground, err := responsesImageToolString(definition, "background")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if hasBackground {
		if err := validateImageEnum(background, fmt.Sprintf("tools[%d].background", index), "auto", "opaque", "transparent"); err != nil {
			return err
		}
		if dto.OpenAIImageModelKind(model) == dto.OpenAIImageModelGPT2 && background == "transparent" {
			return fmt.Errorf("tools[%d].background transparent is not supported for %s", index, model)
		}
	}
	inputFidelity, hasInputFidelity, err := responsesImageToolString(definition, "input_fidelity")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if hasInputFidelity {
		kind := dto.OpenAIImageModelKind(model)
		if kind == dto.OpenAIImageModelGPT2 || kind == dto.OpenAIImageModelGPT1Mini {
			return fmt.Errorf("tools[%d].input_fidelity is not supported for %s", index, model)
		}
		if action == "generate" {
			return fmt.Errorf("tools[%d].input_fidelity is not supported for action generate", index)
		}
		if err := validateImageEnum(inputFidelity, fmt.Sprintf("tools[%d].input_fidelity", index), "high", "low"); err != nil {
			return err
		}
	}
	if mask, exists := definition["input_image_mask"]; exists {
		raw, err := common.Marshal(mask)
		if err != nil {
			return fmt.Errorf("encode tools[%d].input_image_mask: %w", index, err)
		}
		if err := validateOpenAIImageRef(raw, fmt.Sprintf("tools[%d].input_image_mask", index)); err != nil {
			return err
		}
	}
	moderation, hasModeration, err := responsesImageToolString(definition, "moderation")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if hasModeration {
		if err := validateImageEnum(moderation, fmt.Sprintf("tools[%d].moderation", index), "auto", "low"); err != nil {
			return err
		}
	}
	outputFormat, hasOutputFormat, err := responsesImageToolString(definition, "output_format")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if hasOutputFormat {
		if err := validateImageEnum(outputFormat, fmt.Sprintf("tools[%d].output_format", index), "png", "jpeg", "webp"); err != nil {
			return err
		}
		if background == "transparent" && outputFormat == "jpeg" {
			return fmt.Errorf("tools[%d].background transparent requires output_format png or webp", index)
		}
	}
	if compression, exists := definition["output_compression"]; exists {
		number, ok := compression.(float64)
		if !ok || number < 0 || number > 100 || number != math.Trunc(number) {
			return fmt.Errorf("tools[%d].output_compression must be an integer between 0 and 100", index)
		}
		if outputFormat != "jpeg" && outputFormat != "webp" {
			return fmt.Errorf("tools[%d].output_compression requires output_format jpeg or webp", index)
		}
	}
	if partial, exists := definition["partial_images"]; exists {
		number, ok := partial.(float64)
		if !ok || number < 0 || number > dto.MaxOpenAIImagePartialImages || number != math.Trunc(number) {
			return fmt.Errorf(
				"tools[%d].partial_images must be an integer between 0 and %d",
				index,
				dto.MaxOpenAIImagePartialImages,
			)
		}
		if number > 0 && !stream {
			return fmt.Errorf("tools[%d].partial_images greater than 0 requires stream=true", index)
		}
	}
	quality, hasQuality, err := responsesImageToolString(definition, "quality")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if hasQuality {
		if err := validateImageEnum(quality, fmt.Sprintf("tools[%d].quality", index), "auto", "high", "medium", "low"); err != nil {
			return err
		}
	}
	size, hasSize, err := responsesImageToolString(definition, "size")
	if err != nil {
		return fmt.Errorf("tools[%d].%w", index, err)
	}
	if hasSize {
		if dto.OpenAIImageModelKind(model) == dto.OpenAIImageModelGPT2 {
			if !dto.ValidOpenAIGPTImage2Size(size) {
				return fmt.Errorf("tools[%d].size is invalid for %s", index, model)
			}
		} else if size != "auto" && size != "1024x1024" && size != "1536x1024" && size != "1024x1536" {
			return fmt.Errorf("tools[%d].size is invalid for %s", index, model)
		}
	}
	return nil
}

// ValidateOpenAIImageRequest validates OpenAI-defined image models while
// retaining the gateway's wider generic limits for custom providers.
func ValidateOpenAIImageRequest(request *dto.ImageRequest, relayMode int, jsonEdit bool) error {
	if request == nil {
		return errors.New("image request is required")
	}
	request.InputImageCount = 0
	if strings.TrimSpace(request.Model) == "" || request.Model != strings.TrimSpace(request.Model) {
		return errors.New("model is required and must not contain surrounding whitespace")
	}
	if request.Prompt == "" {
		return errors.New("prompt is required")
	}
	if strings.Contains(request.Size, "×") {
		return errors.New("size must use 'x' instead of the multiplication sign '×'")
	}
	if request.N == nil {
		request.N = common.GetPointer(uint(1))
	}
	if *request.N < 1 || *request.N > dto.MaxImageN {
		return fmt.Errorf("n must be an integer between 1 and %d", dto.MaxImageN)
	}

	kind := dto.OpenAIImageModelKind(request.Model)
	if kind == dto.OpenAIImageModelUnknown {
		return nil
	}
	if request.AspectRatio != nil || request.Resolution != nil || imageRawProvided(request.StorageOptions) {
		return errors.New("xAI image parameters are not supported for OpenAI image models")
	}
	if *request.N > dto.MaxOpenAIImageN {
		return fmt.Errorf("n must be an integer between 1 and %d for OpenAI image models", dto.MaxOpenAIImageN)
	}

	maxPromptChars := 32_000
	switch kind {
	case dto.OpenAIImageModelDallE2:
		maxPromptChars = 1_000
	case dto.OpenAIImageModelDallE3:
		maxPromptChars = 4_000
	}
	if utf8.RuneCountInString(request.Prompt) > maxPromptChars {
		return fmt.Errorf("prompt must not exceed %d characters for model %s", maxPromptChars, request.Model)
	}

	isEdit := relayMode == relayconstant.RelayModeImagesEdits
	isGPT := kind >= dto.OpenAIImageModelGPT1
	if isEdit && kind == dto.OpenAIImageModelDallE3 {
		return errors.New("dall-e-3 does not support image edits")
	}
	if !isGPT && request.Stream != nil && *request.Stream {
		return errors.New("stream is only supported for GPT Image models")
	}
	if kind == dto.OpenAIImageModelDallE3 && *request.N != 1 {
		return errors.New("n must be 1 for dall-e-3")
	}

	if isGPT {
		if request.Quality == "" {
			request.Quality = "auto"
		}
		if err := validateImageEnum(request.Quality, "quality", "auto", "high", "medium", "low"); err != nil {
			return err
		}
		if kind == dto.OpenAIImageModelGPT2 {
			if !dto.ValidOpenAIGPTImage2Size(request.Size) {
				return errors.New("size is invalid for gpt-image-2")
			}
		} else if request.Size != "" && request.Size != "auto" &&
			request.Size != "1024x1024" && request.Size != "1536x1024" && request.Size != "1024x1536" {
			return errors.New("size must be one of auto, 1024x1024, 1536x1024, or 1024x1536")
		}
		if request.ResponseFormat != "" {
			return errors.New("response_format is not supported for GPT Image models")
		}
		if imageRawProvided(request.Style) {
			return errors.New("style is not supported for GPT Image models")
		}

		background, hasBackground, err := decodeImageString(request.Background, "background")
		if err != nil {
			return err
		}
		if hasBackground {
			if err := validateImageEnum(background, "background", "auto", "opaque", "transparent"); err != nil {
				return err
			}
			if kind == dto.OpenAIImageModelGPT2 && background == "transparent" {
				return errors.New("transparent background is not supported for gpt-image-2")
			}
		}
		moderation, hasModeration, err := decodeImageString(request.Moderation, "moderation")
		if err != nil {
			return err
		}
		if hasModeration {
			if err := validateImageEnum(moderation, "moderation", "auto", "low"); err != nil {
				return err
			}
		}
		outputFormat, hasOutputFormat, err := decodeImageString(request.OutputFormat, "output_format")
		if err != nil {
			return err
		}
		if hasOutputFormat {
			if err := validateImageEnum(outputFormat, "output_format", "png", "jpeg", "webp"); err != nil {
				return err
			}
			if background == "transparent" && outputFormat == "jpeg" {
				return errors.New("background transparent requires output_format png or webp")
			}
		}
		_, hasCompression, err := decodeImageUint(request.OutputCompression, "output_compression", 100)
		if err != nil {
			return err
		}
		if hasCompression && outputFormat != "jpeg" && outputFormat != "webp" {
			return errors.New("output_compression requires output_format jpeg or webp")
		}
		partialImages, hasPartialImages, err := decodeImageUint(
			request.PartialImages,
			"partial_images",
			dto.MaxOpenAIImagePartialImages,
		)
		if err != nil {
			return err
		}
		if hasPartialImages && partialImages > 0 && (request.Stream == nil || !*request.Stream) {
			return errors.New("partial_images greater than 0 requires stream=true")
		}

		inputFidelity, hasInputFidelity, err := decodeImageString(request.InputFidelity, "input_fidelity")
		if err != nil {
			return err
		}
		if hasInputFidelity {
			if !isEdit {
				return errors.New("input_fidelity is only supported for image edits")
			}
			if kind == dto.OpenAIImageModelGPT2 || kind == dto.OpenAIImageModelGPT1Mini {
				return fmt.Errorf("input_fidelity is not supported for model %s", request.Model)
			}
			if err := validateImageEnum(inputFidelity, "input_fidelity", "high", "low"); err != nil {
				return err
			}
		}
	} else {
		if imageRawProvided(request.Background) || imageRawProvided(request.Moderation) ||
			imageRawProvided(request.OutputFormat) || imageRawProvided(request.OutputCompression) ||
			imageRawProvided(request.PartialImages) || imageRawProvided(request.InputFidelity) {
			return errors.New("GPT Image-only parameters are not supported for DALL-E models")
		}
		if request.ResponseFormat != "" {
			if err := validateImageEnum(request.ResponseFormat, "response_format", "url", "b64_json"); err != nil {
				return err
			}
		}
	}

	switch kind {
	case dto.OpenAIImageModelDallE2:
		if request.Size == "" {
			request.Size = "1024x1024"
		}
		if request.Size != "256x256" && request.Size != "512x512" && request.Size != "1024x1024" {
			return errors.New("size must be one of 256x256, 512x512, or 1024x1024 for dall-e-2")
		}
		if request.Quality == "" {
			request.Quality = "standard"
		}
		if request.Quality != "standard" {
			return errors.New("quality must be standard for dall-e-2")
		}
		if imageRawProvided(request.Style) {
			return errors.New("style is not supported for dall-e-2")
		}
	case dto.OpenAIImageModelDallE3:
		if request.Size == "" {
			request.Size = "1024x1024"
		}
		if request.Size != "1024x1024" && request.Size != "1024x1792" && request.Size != "1792x1024" {
			return errors.New("size must be one of 1024x1024, 1024x1792, or 1792x1024 for dall-e-3")
		}
		if request.Quality == "" {
			request.Quality = "standard"
		}
		if request.Quality != "standard" && request.Quality != "hd" {
			return errors.New("quality must be standard or hd for dall-e-3")
		}
		style, hasStyle, err := decodeImageString(request.Style, "style")
		if err != nil {
			return err
		}
		if hasStyle {
			if err := validateImageEnum(style, "style", "vivid", "natural"); err != nil {
				return err
			}
		}
	}

	if !isEdit {
		if imageRawProvided(request.Images) || imageRawProvided(request.Image) || imageRawProvided(request.Mask) {
			return errors.New("image inputs are only supported for image edits")
		}
		return nil
	}
	if !jsonEdit {
		return nil
	}
	if !isGPT {
		return errors.New("JSON image edits are only supported for GPT Image models")
	}
	if imageRawProvided(request.Image) {
		return errors.New("JSON image edits must use the images array")
	}
	if !imageRawProvided(request.Images) {
		return errors.New("images is required for a JSON image edit")
	}
	inputCount, err := validateOpenAIImageRefs(request.Images)
	if err != nil {
		return err
	}
	request.InputImageCount = inputCount
	if imageRawProvided(request.Mask) {
		if err := validateOpenAIImageRef(request.Mask, "mask"); err != nil {
			return err
		}
		request.InputImageCount++
	}
	return nil
}

func isOpenAIImageFileField(name string) bool {
	if name == "image" || name == "image[]" {
		return true
	}
	if !strings.HasPrefix(name, "image[") || !strings.HasSuffix(name, "]") {
		return false
	}
	index := strings.TrimSuffix(strings.TrimPrefix(name, "image["), "]")
	if index == "" {
		return false
	}
	_, err := strconv.ParseUint(index, 10, 31)
	return err == nil
}

// ValidateOpenAIImageMultipartFiles validates the edit files before billing
// and before the multipart body is reconstructed for the provider.
func ValidateOpenAIImageMultipartFiles(request *dto.ImageRequest, form *multipart.Form) error {
	if request == nil || form == nil {
		return errors.New("multipart image edit form is required")
	}
	var images []*multipart.FileHeader
	var masks []*multipart.FileHeader
	for field, files := range form.File {
		switch {
		case isOpenAIImageFileField(field):
			images = append(images, files...)
		case field == "mask":
			masks = append(masks, files...)
		default:
			return fmt.Errorf("unsupported image edit file field %q", field)
		}
	}
	if len(images) == 0 {
		return errors.New("image is required")
	}

	kind := dto.OpenAIImageModelKind(request.Model)
	maxImages := dto.MaxImageN
	maxBytes := openAIImageMaxFileBytes
	allowedExtensions := map[string]bool{".png": true, ".webp": true, ".jpg": true, ".jpeg": true}
	if kind == dto.OpenAIImageModelDallE2 {
		maxImages = 1
		maxBytes = dallE2MaxFileBytes
		allowedExtensions = map[string]bool{".png": true}
	} else if kind >= dto.OpenAIImageModelGPT1 {
		maxImages = dto.MaxOpenAIImageEditInputs
	}
	if len(images) > maxImages {
		return fmt.Errorf("image edit accepts at most %d input images", maxImages)
	}
	for index, image := range images {
		if image == nil {
			return fmt.Errorf("image %d is invalid", index)
		}
		if image.Size <= 0 || image.Size >= maxBytes {
			return fmt.Errorf("image %d must be smaller than %d bytes", index, maxBytes)
		}
		if !allowedExtensions[strings.ToLower(filepath.Ext(image.Filename))] {
			return fmt.Errorf("image %d has an unsupported file type", index)
		}
	}
	request.InputImageCount = len(images)
	if len(masks) > 1 {
		return errors.New("only one mask file is supported")
	}
	if len(masks) == 1 {
		mask := masks[0]
		if mask == nil || strings.ToLower(filepath.Ext(mask.Filename)) != ".png" {
			return errors.New("mask must be a PNG file")
		}
		if mask.Size <= 0 || mask.Size >= dallE2MaxFileBytes {
			return fmt.Errorf("mask must be smaller than %d bytes", dallE2MaxFileBytes)
		}
		request.InputImageCount++
	}
	return nil
}
