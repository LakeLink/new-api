package minimax

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertAudioRequestKeepsValidatedMiniMaxFieldsAuthoritative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "customer-speech-alias",
	}
	request := dto.AudioRequest{
		Model:          "speech-2.8-hd",
		Input:          "bill this complete text",
		Voice:          "English_expressive_narrator",
		ResponseFormat: "flac",
		Metadata: []byte(`{
			"model":"speech-01-turbo",
			"text":"x",
			"stream":true,
			"stream_options":{"exclude_aggregated_audio":true},
			"voice_setting":{"voice_id":"attacker_voice","speed":2,"pitch":4},
			"audio_setting":{"format":"wav","sample_rate":32000},
			"output_format":"url"
		}`),
	}

	reader, err := (&Adaptor{}).ConvertAudioRequest(c, info, request)
	require.NoError(t, err)

	var converted MiniMaxTTSRequest
	require.NoError(t, common.DecodeJson(reader, &converted))
	require.Equal(t, "speech-2.8-hd", converted.Model)
	require.Equal(t, "bill this complete text", converted.Text)
	require.False(t, converted.Stream)
	require.Nil(t, converted.StreamOptions)
	require.Equal(t, "English_expressive_narrator", converted.VoiceSetting.VoiceID)
	require.Equal(t, 1.0, converted.VoiceSetting.Speed)
	require.NotNil(t, converted.VoiceSetting.Pitch)
	require.Equal(t, 4, *converted.VoiceSetting.Pitch, "safe provider-specific metadata remains supported")
	require.NotNil(t, converted.AudioSetting)
	require.Equal(t, "flac", converted.AudioSetting.Format)
	require.NotNil(t, converted.AudioSetting.SampleRate)
	require.Equal(t, 32000, *converted.AudioSetting.SampleRate)
	require.Equal(t, "hex", converted.OutputFormat)
	require.Equal(t, "flac", c.GetString("response_format"))
}

func TestConvertAudioRequestPreservesExplicitZeroMiniMaxOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech}

	reader, err := (&Adaptor{}).ConvertAudioRequest(c, info, dto.AudioRequest{
		Model: "speech-2.8-hd",
		Input: "hello",
		Voice: "alloy",
		Metadata: []byte(`{
			"subtitle_enable":false,
			"aigc_watermark":false,
			"voice_setting":{"pitch":0,"text_normalization":false},
			"audio_setting":{"force_cbr":false}
		}`),
	})
	require.NoError(t, err)

	var converted MiniMaxTTSRequest
	require.NoError(t, common.DecodeJson(reader, &converted))
	require.NotNil(t, converted.SubtitleEnable)
	require.False(t, *converted.SubtitleEnable)
	require.NotNil(t, converted.AigcWatermark)
	require.False(t, *converted.AigcWatermark)
	require.NotNil(t, converted.VoiceSetting.Pitch)
	require.Zero(t, *converted.VoiceSetting.Pitch)
	require.NotNil(t, converted.VoiceSetting.TextNormalization)
	require.False(t, *converted.VoiceSetting.TextNormalization)
	require.NotNil(t, converted.AudioSetting)
	require.NotNil(t, converted.AudioSetting.ForceCbr)
	require.False(t, *converted.AudioSetting.ForceCbr)
}

func TestConvertAudioRequestValidatesMiniMaxProtocolOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "customer-speech-alias",
	}

	for _, test := range []struct {
		name           string
		responseFormat string
		speed          *float64
		wantErr        string
	}{
		{name: "default format and speed"},
		{name: "pcm is supported", responseFormat: "pcm"},
		{name: "unsupported opus", responseFormat: "opus", wantErr: "unsupported MiniMax TTS response format"},
		{name: "speed below provider minimum", speed: common.GetPointer(0.49), wantErr: "speed must be between"},
		{name: "speed above provider maximum", speed: common.GetPointer(2.01), wantErr: "speed must be between"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			reader, err := (&Adaptor{}).ConvertAudioRequest(c, info, dto.AudioRequest{
				Model:          "speech-2.8-hd",
				Input:          "hello",
				Voice:          "alloy",
				ResponseFormat: test.responseFormat,
				Speed:          test.speed,
			})
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			var converted MiniMaxTTSRequest
			require.NoError(t, common.DecodeJson(reader, &converted))
			require.Equal(t, 1.0, converted.VoiceSetting.Speed)
			if test.responseFormat == "" {
				require.Equal(t, "mp3", converted.AudioSetting.Format)
			}
		})
	}
}

func TestHandleTTSResponseUsesActualMiniMaxCharacterCountAndFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("response_format", "mp3")
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(3)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`{"data":{"audio":"0102","status":2},"extra_info":{"usage_characters":7,"audio_format":"wav"},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		)),
	}

	usageValue, apiErr := handleTTSResponse(c, resp, info)
	require.Nil(t, apiErr)
	usage, ok := usageValue.(*dto.Usage)
	require.True(t, ok)
	require.Equal(t, 7, usage.PromptTokens)
	require.Equal(t, 7, usage.TotalTokens)
	require.Equal(t, "audio/wav", recorder.Header().Get("Content-Type"))
	require.Equal(t, []byte{1, 2}, recorder.Body.Bytes())
}
