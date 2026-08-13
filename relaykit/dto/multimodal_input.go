package dto

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

func collectMultimodalTokenInputs(input any, texts *[]string, files *[]*types.FileMeta) {
	switch value := input.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			*texts = append(*texts, value)
		}
	case []any:
		for _, item := range value {
			collectMultimodalTokenInputs(item, texts, files)
		}
	case []string:
		for _, item := range value {
			collectMultimodalTokenInputs(item, texts, files)
		}
	case map[string]any:
		collectMultimodalMapTokenInputs(value, texts, files)
	case map[string]string:
		converted := make(map[string]any, len(value))
		for key, item := range value {
			converted[key] = item
		}
		collectMultimodalMapTokenInputs(converted, texts, files)
	}
}

func collectMultimodalMapTokenInputs(input map[string]any, texts *[]string, files *[]*types.FileMeta) {
	if text, ok := input["text"].(string); ok {
		collectMultimodalTokenInputs(text, texts, files)
	}

	fileFields := []struct {
		name     string
		fileType types.FileType
	}{
		{name: "image", fileType: types.FileTypeImage},
		{name: "audio", fileType: types.FileTypeAudio},
		{name: "video", fileType: types.FileTypeVideo},
		{name: "pdf", fileType: types.FileTypeFile},
	}
	for _, field := range fileFields {
		data, ok := input[field.name].(string)
		if !ok || strings.TrimSpace(data) == "" {
			continue
		}
		*files = append(*files, types.NewFileMeta(field.fileType, types.NewFileSourceFromData(data, "")))
	}

	if content, ok := input["content"]; ok {
		collectMultimodalTokenInputs(content, texts, files)
	}
}
