//go:build !windows

package main

import "fmt"

// helper 首发仅支持 Windows（设计文档 §1）；非 Windows 平台占位以保 go build ./... 可用。
func main() {
	fmt.Println("tunnelboard-helper is only supported on Windows")
}
