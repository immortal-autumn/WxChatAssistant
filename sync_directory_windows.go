//go:build windows

package main

// Windows does not support flushing a directory handle this way. Each file is
// still flushed before its atomic replacement.
func syncDirectory(string) error { return nil }
