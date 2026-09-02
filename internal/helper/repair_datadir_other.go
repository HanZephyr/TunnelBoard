//go:build !darwin

package helper

func repairDataDirIfNeeded(string) error { return nil }
