package diag

import "regexp"

var (
	logSecretKV   = regexp.MustCompile(`(?i)(password|passphrase|secret|private[_ -]?key|token)(\s*[=:]\s*|%3[dD])([^\s&;,]+)`)
	logJSONSecret = regexp.MustCompile(`(?i)("(?:password|passphrase|secret|privateKey|token)"\s*:\s*")[^"]*(")`)
	logBearer     = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+|\bbearer\s+)([^\s,;]+)`)
)

func RedactLogText(text string) string {
	text = logJSONSecret.ReplaceAllString(text, `${1}[REDACTED]${2}`)
	text = logBearer.ReplaceAllString(text, `${1}[REDACTED]`)
	text = logSecretKV.ReplaceAllString(text, `${1}${2}[REDACTED]`)
	return homePattern.ReplaceAllString(text, "~")
}
