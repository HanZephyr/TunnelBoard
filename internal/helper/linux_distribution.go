package helper

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	linuxTrustStoreDebian = "debian"
	linuxTrustStoreRHEL   = "rhel"
)

type linuxTrustStore struct {
	family            string
	caPath            string
	refreshExecutable string
	refreshArgs       []string
}

// linuxTrustStoreFromOSRelease 只接受产品承诺的发行版和版本下限。路径和刷新命令
// 都由发行版族固定，主程序及 UI 永远不能通过请求覆盖它们。
func linuxTrustStoreFromOSRelease(content []byte) (linuxTrustStore, error) {
	values := parseOSRelease(string(content))
	id := strings.ToLower(values["ID"])
	major, err := linuxVersionMajor(values["VERSION_ID"])
	if err != nil {
		return linuxTrustStore{}, fmt.Errorf("helper: unsupported Linux distribution version %q", values["VERSION_ID"])
	}
	switch id {
	case "debian":
		if major < 12 {
			return linuxTrustStore{}, fmt.Errorf("helper: Debian %d is below the supported floor", major)
		}
		return linuxTrustStore{
			family: linuxTrustStoreDebian, caPath: "/usr/local/share/ca-certificates/tunnelboard-local-ca.crt",
			refreshExecutable: "/usr/sbin/update-ca-certificates",
		}, nil
	case "ubuntu":
		if major < 24 {
			return linuxTrustStore{}, fmt.Errorf("helper: Ubuntu %d is below the supported floor", major)
		}
		return linuxTrustStore{
			family: linuxTrustStoreDebian, caPath: "/usr/local/share/ca-certificates/tunnelboard-local-ca.crt",
			refreshExecutable: "/usr/sbin/update-ca-certificates",
		}, nil
	case "rhel", "rocky", "almalinux", "centos":
		if major < 9 {
			return linuxTrustStore{}, fmt.Errorf("helper: RHEL-compatible %d is below the supported floor", major)
		}
		return linuxTrustStore{
			family: linuxTrustStoreRHEL, caPath: "/etc/pki/ca-trust/source/anchors/tunnelboard-local-ca.crt",
			refreshExecutable: "/usr/bin/update-ca-trust", refreshArgs: []string{"extract"},
		}, nil
	default:
		return linuxTrustStore{}, fmt.Errorf("helper: unsupported Linux distribution %q", id)
	}
}

func parseOSRelease(content string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"")
		if key != "" {
			values[key] = value
		}
	}
	return values
}

func linuxVersionMajor(value string) (int, error) {
	part, _, _ := strings.Cut(strings.TrimSpace(value), ".")
	if part == "" {
		return 0, fmt.Errorf("empty version")
	}
	major, err := strconv.Atoi(part)
	if err != nil || major < 0 {
		return 0, fmt.Errorf("invalid version")
	}
	return major, nil
}
