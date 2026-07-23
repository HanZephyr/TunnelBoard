//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tunnelboard-linux-helper only runs on Linux")
	os.Exit(1)
}
