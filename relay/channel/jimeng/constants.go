package jimeng

const (
	ChannelName = "jimeng"
)

// ModelList intentionally contains only the synchronous image model supported
// by this adaptor. Volcengine marks Image 2.1 as "下线中"; current Image
// 3.0/3.1/4.0/4.6 models use the asynchronous
// CVSync2AsyncSubmitTask/CVSync2AsyncGetResult protocol and must not be
// advertised through the synchronous OpenAI Images relay.
var ModelList = []string{
	"jimeng_high_aes_general_v21_L",
}
