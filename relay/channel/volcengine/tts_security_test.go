package volcengine

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertAudioRequestKeepsValidatedVolcengineFieldsAuthoritative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "customer-speech-alias",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: "real-app|real-token",
		},
	}
	request := dto.AudioRequest{
		Model:          "seed-tts-1.1",
		Input:          "bill this complete text",
		Voice:          "alloy",
		ResponseFormat: "opus",
		Metadata: []byte(`{
			"app":{"appid":"attacker-app","token":"attacker-token","cluster":"attacker-cluster"},
			"user":{"uid":"attacker-user"},
			"audio":{"voice_type":"attacker-voice","encoding":"pcm","speed_ratio":0.1,"rate":16000,"bitrate":128},
			"request":{"reqid":"attacker-request","text":"x","operation":"query","model":"cheaper-model","text_type":"ssml"}
		}`),
	}

	reader, err := (&Adaptor{}).ConvertAudioRequest(c, info, request)
	require.NoError(t, err)

	var converted VolcengineTTSRequest
	require.NoError(t, common.DecodeJson(reader, &converted))
	require.Equal(t, "real-app", converted.App.AppID)
	require.Equal(t, "real-token", converted.App.Token)
	require.Equal(t, "volcano_tts", converted.App.Cluster)
	require.Equal(t, "openai_relay_user", converted.User.UID)
	require.Equal(t, openAIToVolcengineVoiceMap["alloy"], converted.Audio.VoiceType)
	require.Equal(t, "ogg_opus", converted.Audio.Encoding)
	require.Equal(t, 1.0, converted.Audio.SpeedRatio)
	require.Equal(t, 16000, converted.Audio.Rate, "safe provider-specific metadata remains supported")
	require.Equal(t, 128, converted.Audio.Bitrate)
	require.NotEqual(t, "attacker-request", converted.Request.ReqID)
	require.Equal(t, "bill this complete text", converted.Request.Text)
	require.Equal(t, "submit", converted.Request.Operation)
	require.Equal(t, "seed-tts-1.1", converted.Request.Model)
	require.Equal(t, "ssml", converted.Request.TextType)
	require.Equal(t, "ogg_opus", c.GetString(contextKeyResponseFormat))
	require.True(t, info.IsStream)
}

func TestConvertAudioRequestValidatesVolcengineProtocolOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "customer-speech-alias",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: "app|token",
		},
	}

	for _, test := range []struct {
		name           string
		responseFormat string
		speed          *float64
		wantErr        string
	}{
		{name: "default format and speed"},
		{name: "opus maps to ogg opus", responseFormat: "opus"},
		{name: "wav is unsupported on official streaming endpoint", responseFormat: "wav", wantErr: "does not support wav"},
		{name: "unsupported flac", responseFormat: "flac", wantErr: "unsupported Volcengine TTS response format"},
		{name: "speed below provider minimum", speed: common.GetPointer(0.09), wantErr: "speed must be between"},
		{name: "speed above provider maximum", speed: common.GetPointer(2.01), wantErr: "speed must be between"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			reader, err := (&Adaptor{}).ConvertAudioRequest(c, info, dto.AudioRequest{
				Model:          "seed-tts-1.1",
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
			var converted VolcengineTTSRequest
			require.NoError(t, common.DecodeJson(reader, &converted))
			require.Equal(t, 1.0, converted.Audio.SpeedRatio)
			if test.responseFormat == "" {
				require.Equal(t, "mp3", converted.Audio.Encoding)
			}
		})
	}
}

func TestConvertAudioRequestUsesHTTPForCustomVolcengineBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "customer-speech-alias",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:         "app|token",
			ChannelBaseUrl: "https://volcengine-proxy.example",
		},
	}

	_, err := (&Adaptor{}).ConvertAudioRequest(c, info, dto.AudioRequest{Model: "seed-tts-1.1", Input: "hello", Voice: "alloy"})
	require.NoError(t, err)
	require.False(t, info.IsStream)
}
