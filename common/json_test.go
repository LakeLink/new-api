package common

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonRawMessageToString(t *testing.T) {
	tests := []struct {
		name string
		data json.RawMessage
		want string
	}{
		{
			name: "object",
			data: json.RawMessage(`{"city":"Paris","days":0,"strict":false}`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "string",
			data: json.RawMessage(`"{\"city\":\"Paris\",\"days\":0,\"strict\":false}"`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "null",
			data: json.RawMessage(`null`),
			want: "",
		},
		{
			name: "empty",
			data: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, JsonRawMessageToString(tt.data))
		})
	}
}

func TestDecodeJsonRequiresExactlyOneValue(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    map[string]int
		wantErr bool
	}{
		{
			name:    "single value",
			payload: `{"count":1}`,
			want:    map[string]int{"count": 1},
		},
		{
			name:    "trailing whitespace",
			payload: " \n{\"count\":1}\t\r\n",
			want:    map[string]int{"count": 1},
		},
		{
			name:    "second value",
			payload: `{"count":1}{"count":2}`,
			wantErr: true,
		},
		{
			name:    "trailing garbage",
			payload: `{"count":1}not-json`,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var decoded map[string]int
			err := DecodeJson(strings.NewReader(test.payload), &decoded)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, decoded)
		})
	}
}
