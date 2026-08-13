package matcher

import (
	"regexp"
	"sync"
)

var compiledRegexCache sync.Map

func MatchAnyRegex(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		compiled, ok := compiledRegexCache.Load(pattern)
		regex, _ := compiled.(*regexp.Regexp)
		if !ok || regex == nil {
			nextRegex, err := regexp.Compile(pattern)
			if err != nil {
				continue
			}
			regex = nextRegex
			compiledRegexCache.Store(pattern, regex)
		}
		if regex.MatchString(value) {
			return true
		}
	}
	return false
}
