//go:build !windows

package main

import (
	"fmt"
	"os"
)

// helper 首发仅支持 Windows（设计文档 §1）；非 Windows 平台占位以保 go build ./... 可用。
func main() {
	if handled, err := runSelfCheck(os.Args[1:], os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Println("tunnelboard-helper is only supported on Windows")
}
