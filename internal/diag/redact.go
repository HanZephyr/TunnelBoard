package diag

import "regexp"

var (
	logSecretKV   = regexp.MustCompile(`(?i)(password|passphrase|secret|private[_ -]?key|token)(\s*[=:]\s*|%3[dD])([^\s&;,]+)`)
	logJSONSecret = regexp.MustCompile(`(?i)("(?:password|passphrase|secret|privateKey|token|authorization|cookie)"\s*:\s*")[^"]*(")`)
	logBearer     = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+|\bbearer\s+)([^\s,;]+)`)
	logPrivateKey = regexp.MustCompile(`(?is)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?(-----END [^-\r\n]*PRIVATE KEY-----|$)`)
)

func RedactLogText(text string) string {
	text = logPrivateKey.ReplaceAllString(text, `[REDACTED PRIVATE KEY]`)
	text = logJSONSecret.ReplaceAllString(text, `${1}[REDACTED]${2}`)
	text = logBearer.ReplaceAllString(text, `${1}[REDACTED]`)
	text = logSecretKV.ReplaceAllString(text, `${1}${2}[REDACTED]`)
	return homePattern.ReplaceAllString(text, "~")
}
