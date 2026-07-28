package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func UnmarshalJsonStr(data string, v any) error {
	return json.Unmarshal(StringToByteSlice(data), v)
}

func DecodeJson(reader io.Reader, v any) error {
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(v); err != nil {
		return err
	}

	// Decode exactly one JSON value. json.Decoder.Decode otherwise accepts a
	// valid prefix and silently leaves trailing garbage or a second value
	// unread, which lets malformed request and upstream payloads pass
	// validation depending on whether they were decoded from a stream.
	remaining := io.MultiReader(decoder.Buffered(), reader)
	var buffer [4096]byte
	for {
		n, err := remaining.Read(buffer[:])
		for _, character := range buffer[:n] {
			switch character {
			case ' ', '\t', '\r', '\n':
				continue
			default:
				return fmt.Errorf("invalid trailing JSON data")
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read trailing JSON data: %w", err)
		}
	}
}

func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func GetJsonType(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "unknown"
	}
	firstChar := trimmed[0]
	switch firstChar {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

// JsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func JsonRawMessageToString(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] != '"' {
		return string(trimmed)
	}
	var value string
	if err := Unmarshal(trimmed, &value); err != nil {
		return string(trimmed)
	}
	return value
}
