package gemini

var ModelList = []string{
	// stable version
	"gemini-3.6-flash", "gemini-3.5-flash", "gemini-3.5-flash-lite", "gemini-3.1-flash-lite",
	"gemini-2.5-flash", "gemini-2.5-pro", "gemini-2.5-flash-lite",
	// latest version
	"gemini-flash-latest", "gemini-flash-lite-latest", "gemini-pro-latest",
	// preview version
	"gemini-2.5-flash-preview-tts", "gemini-2.5-pro-preview-tts",
	"gemini-2.5-flash-image", "gemini-3-flash-preview", "gemini-3.1-pro-preview",
	"gemini-3.1-pro-preview-customtools",
	"gemini-3.1-flash-image", "gemini-3.1-flash-lite-image", "gemini-3-pro-image",
	"gemini-3.1-flash-tts-preview",
	"gemini-2.5-computer-use-preview-10-2025",
	// gemma models
	"gemma-4-31b-it", "gemma-4-26b-a4b-it",
	"gemma-3-1b-it", "gemma-3-4b-it", "gemma-3-12b-it",
	"gemma-3-27b-it", "gemma-3n-e4b-it", "gemma-3n-e2b-it",
	// embedding models
	"gemini-embedding-2",
	// imagen models
	"imagen-4.0-generate-001", "imagen-4.0-ultra-generate-001",
	"imagen-4.0-fast-generate-001",
	// veo models
	"veo-3.1-generate-preview", "veo-3.1-fast-generate-preview", "veo-3.1-lite-generate-preview",
	// robotics models
	"gemini-robotics-er-1.6-preview",
	// other models
	"aqa",
}

var SafetySettingList = []string{
	"HARM_CATEGORY_HARASSMENT",
	"HARM_CATEGORY_HATE_SPEECH",
	"HARM_CATEGORY_SEXUALLY_EXPLICIT",
	"HARM_CATEGORY_DANGEROUS_CONTENT",
	//"HARM_CATEGORY_CIVIC_INTEGRITY", This item is deprecated!
}

var ChannelName = "google gemini"
