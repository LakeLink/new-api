package operation_setting

import (
	"strings"
	"sync"
)

var DemoSiteEnabled = false
var SelfUseModeEnabled = false

var AutomaticDisableKeywords = []string{
	"Your credit balance is too low",
	"This organization has been disabled.",
	"You exceeded your current quota",
	"Permission denied",
	"The security token included in the request is invalid",
	"Operation not allowed",
	"Your account is not authorized",
}

var automaticDisableKeywordsMutex sync.RWMutex

func AutomaticDisableKeywordsToString() string {
	automaticDisableKeywordsMutex.RLock()
	defer automaticDisableKeywordsMutex.RUnlock()
	return strings.Join(AutomaticDisableKeywords, "\n")
}

func AutomaticDisableKeywordsFromString(s string) {
	keywords := make([]string, 0)
	ak := strings.Split(s, "\n")
	for _, k := range ak {
		k = strings.TrimSpace(k)
		k = strings.ToLower(k)
		if k != "" {
			keywords = append(keywords, k)
		}
	}
	automaticDisableKeywordsMutex.Lock()
	AutomaticDisableKeywords = keywords
	automaticDisableKeywordsMutex.Unlock()
}

func GetAutomaticDisableKeywords() []string {
	automaticDisableKeywordsMutex.RLock()
	defer automaticDisableKeywordsMutex.RUnlock()
	return append([]string(nil), AutomaticDisableKeywords...)
}
