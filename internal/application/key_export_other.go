//go:build !windows

package application

import "os"

func restrictPrivateKeyFile(string) error { return nil }

func replacePrivateKeyFile(source, destination string) error {
	return os.Rename(source, destination)
}

func linkPrivateKeyFile(source, destination string) error {
	return os.Link(source, destination)
}
