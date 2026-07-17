package traytext

import "testing"

func TestForLocaleUsesTunnelBoardIdentity(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN", "zh-TW", "zh-HK", "ru", "unknown"} {
		strings := ForLocale(locale)
		if strings.AppTitle != "TunnelBoard" || strings.IconTooltip != "TunnelBoard" {
			t.Fatalf("ForLocale(%q) identity = %#v", locale, strings)
		}
	}
}
