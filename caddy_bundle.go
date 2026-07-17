package main

// 随安装包内置的固定版本 Caddy（CONTEXT.md:75：固定版本、完整性校验、不在首次使用时下载）。
// 升级版本时同步更新 scripts/fetch-caddy.py 与这两个常量。
const (
	caddyBundleVersion = "2.11.4"
	caddyBundleSHA256  = "5cb9ab71e5756ce72840b8234177a2f40c8b4ab47a806b8e841e2b784e9df62b"
)
