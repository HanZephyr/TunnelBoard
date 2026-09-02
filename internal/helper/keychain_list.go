package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
)

type keychainCertificate struct {
	sha1   string
	sha256 string
}

func listKeychainCertificates(ctx context.Context, runner CommandRunner, keychain string) ([]keychainCertificate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runner == nil {
		runner = execCommandRunner{}
	}
	out, err := runner.Run(ctx, "/usr/bin/security", "find-certificate", "-a", "-p", "-Z", keychain)
	if err != nil {
		return nil, fmt.Errorf("helper: enumerate keychain %s: %w: %s", keychain, err, strings.TrimSpace(string(out)))
	}
	return parseKeychainCertificateList(out), nil
}

// parseKeychainCertificateList 解析 `security find-certificate -a -p -Z` 输出：
//
//	SHA-1 hash: 1234abcd...
//	-----BEGIN CERTIFICATE-----
//	...
//	-----END CERTIFICATE-----
//
// SHA-1 原样保留（delete-certificate -Z 需要）；SHA-256 由 DER 现场计算。
func parseKeychainCertificateList(out []byte) []keychainCertificate {
	var (
		entries []keychainCertificate
		sha1Hex string
		pemBuf  strings.Builder
	)
	flush := func() {
		if pemBuf.Len() == 0 {
			return
		}
		block, _ := pem.Decode([]byte(pemBuf.String()))
		if block != nil && block.Type == "CERTIFICATE" {
			sum := sha256.Sum256(block.Bytes)
			entries = append(entries, keychainCertificate{
				sha1:   sha1Hex,
				sha256: hex.EncodeToString(sum[:]),
			})
		}
		pemBuf.Reset()
		sha1Hex = ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, found := strings.CutPrefix(line, "SHA-1 hash:"); found {
			flush()
			sha1Hex = strings.TrimSpace(rest)
			continue
		}
		pemBuf.WriteString(line)
		pemBuf.WriteString("\n")
	}
	flush()
	return entries
}
