//go:build !windows

package main

import "fmt"

func main() {
	fmt.Println("selfcheck is only supported on Windows")
}
