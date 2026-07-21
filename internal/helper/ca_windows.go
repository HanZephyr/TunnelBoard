//go:build windows

package helper

// ProductionEnvironment 只向提权 Helper 注入受托管 hosts 路径和协议版本。
// CA 信任由当前用户 LocalCATrust Module 直接操作 CurrentUser\Root。
func ProductionEnvironment(hostsPath, version string) Environment {
	return Environment{HostsPath: hostsPath, Version: version}
}
