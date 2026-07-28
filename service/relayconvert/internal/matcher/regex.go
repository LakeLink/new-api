package matcher

import (
	"regexp"
	"sync"
)

var compiledRegexCache sync.Map // map[string]*regexp.Regexp

func MatchAnyRegex(patterns []string, s string) bool {
	if len(patterns) == 0 || s == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		cached, ok := compiledRegexCache.Load(pattern)
		re, valid := cached.(*regexp.Regexp)
		if !ok || !valid || re == nil {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				// Treat invalid patterns as non-matching to avoid breaking runtime traffic.
				continue
			}
			re = compiled
			compiledRegexCache.Store(pattern, re)
		}
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
