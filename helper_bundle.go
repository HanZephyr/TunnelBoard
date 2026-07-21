package main

// helperBundleSHA256 由唯一 release Module 在主程序构建时通过 -ldflags 注入。
// 开发环境只有显式 TUNNELBOARD_HELPER_PATH override 才允许跳过固定发行摘要。
var helperBundleSHA256 string
