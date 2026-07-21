package main

// 随安装包内置的固定版本 Caddy（CONTEXT.md:75：固定版本、完整性校验、不在首次使用时下载）。
// 唯一 release Module 从 scripts/caddy-lock.json 读取目标平台摘要并通过 -ldflags 注入；
// 默认值只供 Windows 本地开发构建使用。
var (
	caddyBundleVersion = "2.11.4"
	caddyBundleSHA256  = "5cb9ab71e5756ce72840b8234177a2f40c8b4ab47a806b8e841e2b784e9df62b"
)
