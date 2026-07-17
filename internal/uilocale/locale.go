package uilocale

import (
	"os"
	"strings"
)

// Normalize maps arbitrary input to one of the supported vue-i18n locale tags.
func Normalize(raw string) string {
	s := strings.TrimSpace(raw)
	switch s {
	case "en", "zh-CN", "zh-TW", "zh-HK", "ru":
		return s
	}
	low := strings.ToLower(s)
	low = strings.ReplaceAll(low, "_", "-")
	switch {
	case strings.Contains(low, "-hk") || strings.Contains(low, "hongkong"):
		return "zh-HK"
	case strings.Contains(low, "-tw") || strings.Contains(low, "hant"):
		return "zh-TW"
	case strings.HasPrefix(low, "zh"):
		return "zh-CN"
	case strings.HasPrefix(low, "ru"):
		return "ru"
	case strings.HasPrefix(low, "en"):
		return "en"
	default:
		return "en"
	}
}

// DetectFromEnv approximates frontend/src/i18n.js detectSystemLocale using OS env.
func DetectFromEnv() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		base := strings.Split(v, ".")[0]
		base = strings.ReplaceAll(base, "_", "-")
		return Normalize(base)
	}
	return "en"
}
