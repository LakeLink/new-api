package helper

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

const (
	xaiStorageExpiryMinSeconds = uint(3_600)
	xaiStorageExpiryMaxSeconds = uint(2_592_000)
)

func validateXAIImageReference(raw json.RawMessage, field string) error {
	if common.GetJsonType(raw) != "object" {
		return fmt.Errorf("%s must be an object", field)
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("%s must be an object: %w", field, err)
	}
	for key := range fields {
		if key != "url" && key != "image_url" && key != "file_id" && key != "type" {
			return fmt.Errorf("%s contains unsupported field %q", field, key)
		}
	}

	imageURL, hasURL, err := decodeImageString(fields["url"], field+".url")
	if err != nil {
		return err
	}
	compatibleImageURL, hasCompatibleURL, err := decodeImageString(fields["image_url"], field+".image_url")
	if err != nil {
		return err
	}
	if hasURL && hasCompatibleURL {
		return fmt.Errorf("%s must not contain both url and image_url", field)
	}
	if hasCompatibleURL {
		imageURL = compatibleImageURL
		hasURL = true
	}
	fileID, hasFileID, err := decodeImageString(fields["file_id"], field+".file_id")
	if err != nil {
		return err
	}
	if hasURL == hasFileID {
		return fmt.Errorf("%s must contain exactly one of url or file_id", field)
	}

	referenceType, hasType, err := decodeImageString(fields["type"], field+".type")
	if err != nil {
		return err
	}
	if hasURL {
		if imageURL == "" || imageURL != strings.TrimSpace(imageURL) {
			return fmt.Errorf("%s.url must not be empty or contain surrounding whitespace", field)
		}
		if strings.HasPrefix(imageURL, "data:image/") {
			separator := strings.Index(imageURL, ";base64,")
			if separator < len("data:image/") || separator+len(";base64,") == len(imageURL) {
				return fmt.Errorf("%s.url must be a valid base64 image data URI", field)
			}
			switch strings.ToLower(imageURL[len("data:"):separator]) {
			case "image/jpeg", "image/png", "image/webp":
			default:
				return fmt.Errorf("%s.url data URI must contain a JPEG, PNG, or WebP image", field)
			}
			decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(imageURL[separator+len(";base64,"):]))
			if decoded, err := io.Copy(io.Discard, decoder); err != nil || decoded == 0 {
				return fmt.Errorf("%s.url must be a valid base64 image data URI", field)
			}
		} else {
			parsed, err := url.Parse(imageURL)
			if err != nil ||
				(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) ||
				parsed.Host == "" ||
				parsed.User != nil {
				return fmt.Errorf("%s.url must be an HTTP(S) URL or base64 image data URI", field)
			}
		}
		if hasType && referenceType != "image_url" {
			return fmt.Errorf("%s.type must be image_url for a URL reference", field)
		}
		return nil
	}

	if fileID == "" || fileID != strings.TrimSpace(fileID) {
		return fmt.Errorf("%s.file_id must not be empty or contain surrounding whitespace", field)
	}
	if hasType && referenceType != "file_id" {
		return fmt.Errorf("%s.type must be file_id for a file reference", field)
	}
	return nil
}

func validateXAIStorageOptions(raw json.RawMessage) error {
	if !imageRawProvided(raw) {
		return nil
	}
	if common.GetJsonType(raw) != "object" {
		return errors.New("storage_options must be an object")
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("storage_options must be an object: %w", err)
	}
	for key := range fields {
		if key != "filename" && key != "expires_after" && key != "public_url" {
			return fmt.Errorf("storage_options contains unsupported field %q", key)
		}
	}

	filename, hasFilename, err := decodeImageString(fields["filename"], "storage_options.filename")
	if err != nil {
		return err
	}
	if !hasFilename || strings.TrimSpace(filename) == "" {
		return errors.New("storage_options.filename is required")
	}

	var fileExpiry *uint
	if rawExpiry := fields["expires_after"]; imageRawProvided(rawExpiry) {
		if err := common.Unmarshal(rawExpiry, &fileExpiry); err != nil || fileExpiry == nil ||
			*fileExpiry < xaiStorageExpiryMinSeconds || *fileExpiry > xaiStorageExpiryMaxSeconds {
			return fmt.Errorf(
				"storage_options.expires_after must be an integer between %d and %d",
				xaiStorageExpiryMinSeconds,
				xaiStorageExpiryMaxSeconds,
			)
		}
	}

	rawPublicURL := fields["public_url"]
	if !imageRawProvided(rawPublicURL) {
		return nil
	}
	switch common.GetJsonType(rawPublicURL) {
	case "boolean":
		var enabled bool
		if err := common.Unmarshal(rawPublicURL, &enabled); err != nil {
			return errors.New("storage_options.public_url must be a boolean or object")
		}
		return nil
	case "object":
		var publicFields map[string]json.RawMessage
		if err := common.Unmarshal(rawPublicURL, &publicFields); err != nil {
			return fmt.Errorf("storage_options.public_url must be an object: %w", err)
		}
		for key := range publicFields {
			if key != "expires_after" {
				return fmt.Errorf("storage_options.public_url contains unsupported field %q", key)
			}
		}
		var publicExpiry *uint
		if err := common.Unmarshal(publicFields["expires_after"], &publicExpiry); err != nil || publicExpiry == nil ||
			*publicExpiry < xaiStorageExpiryMinSeconds || *publicExpiry > xaiStorageExpiryMaxSeconds {
			return fmt.Errorf(
				"storage_options.public_url.expires_after must be an integer between %d and %d",
				xaiStorageExpiryMinSeconds,
				xaiStorageExpiryMaxSeconds,
			)
		}
		if fileExpiry != nil && *publicExpiry > *fileExpiry {
			return errors.New("storage_options.public_url.expires_after must not exceed storage_options.expires_after")
		}
		return nil
	default:
		return errors.New("storage_options.public_url must be a boolean or object")
	}
}

// ValidateXAIImageRequest validates the native xAI JSON image protocol and
// derives the edit input count used for fixed-price reservation.
func ValidateXAIImageRequest(request *dto.ImageRequest, relayMode int) error {
	if request == nil {
		return errors.New("xAI image request is required")
	}
	request.InputImageCount = 0
	if !dto.IsXAIImageModel(request.Model) {
		return fmt.Errorf("xAI image model %q is not supported", request.Model)
	}
	if request.Prompt == "" {
		return errors.New("prompt is required")
	}
	if request.N == nil {
		request.N = common.GetPointer(uint(1))
	}
	if *request.N < 1 || *request.N > dto.MaxXAIImageN {
		return fmt.Errorf("n must be an integer between 1 and %d for xAI image models", dto.MaxXAIImageN)
	}
	if request.ResponseFormat != "" && request.ResponseFormat != "url" && request.ResponseFormat != "b64_json" {
		return errors.New("response_format must be url or b64_json for xAI image models")
	}
	if request.AspectRatio != nil {
		switch *request.AspectRatio {
		case "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2",
			"19.5:9", "9:19.5", "20:9", "9:20", "auto":
		default:
			return errors.New("aspect_ratio is invalid for xAI image models")
		}
	}
	if request.Resolution != nil {
		if *request.Resolution != "1k" && *request.Resolution != "2k" {
			return errors.New("resolution must be 1k or 2k for xAI image models")
		}
	}
	if err := validateXAIStorageOptions(request.StorageOptions); err != nil {
		return err
	}
	if imageRawProvided(request.User) {
		var user string
		if common.GetJsonType(request.User) != "string" || common.Unmarshal(request.User, &user) != nil {
			return errors.New("user must be a string for xAI image models")
		}
	}

	if request.Size != "" || request.Quality != "" || imageRawProvided(request.Style) ||
		imageRawProvided(request.ExtraFields) ||
		imageRawProvided(request.Background) || imageRawProvided(request.Moderation) ||
		imageRawProvided(request.OutputFormat) || imageRawProvided(request.OutputCompression) ||
		imageRawProvided(request.PartialImages) || request.Stream != nil ||
		imageRawProvided(request.Mask) || imageRawProvided(request.InputFidelity) ||
		request.Watermark != nil || imageRawProvided(request.WatermarkEnabled) ||
		imageRawProvided(request.UserId) {
		return errors.New("request contains parameters unsupported by the xAI image API")
	}
	for field := range request.Extra {
		return fmt.Errorf("xAI image request contains unsupported field %q", field)
	}

	switch relayMode {
	case relayconstant.RelayModeImagesGenerations:
		if imageRawProvided(request.Image) || imageRawProvided(request.Images) {
			return errors.New("image inputs are only supported for xAI image edits")
		}
		return nil
	case relayconstant.RelayModeImagesEdits:
	default:
		return errors.New("unsupported xAI image relay mode")
	}

	hasImage := imageRawProvided(request.Image)
	hasImages := imageRawProvided(request.Images)
	if hasImage == hasImages {
		return errors.New("xAI image edits require exactly one of image or images")
	}
	if hasImage {
		if request.AspectRatio != nil {
			return errors.New("aspect_ratio is not supported for single-image xAI edits")
		}
		if err := validateXAIImageReference(request.Image, "image"); err != nil {
			return err
		}
		request.InputImageCount = 1
		return nil
	}

	if common.GetJsonType(request.Images) != "array" {
		return errors.New("images must be an array")
	}
	var references []json.RawMessage
	if err := common.Unmarshal(request.Images, &references); err != nil {
		return fmt.Errorf("images must be an array: %w", err)
	}
	if len(references) < 2 || len(references) > dto.MaxXAIImageEditInputs {
		return fmt.Errorf("images must contain between 2 and %d image references", dto.MaxXAIImageEditInputs)
	}
	for index, reference := range references {
		if err := validateXAIImageReference(reference, fmt.Sprintf("images[%d]", index)); err != nil {
			return err
		}
	}
	request.InputImageCount = len(references)
	return nil
}
