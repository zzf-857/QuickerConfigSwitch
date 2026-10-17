//go:build !windows

package autostart

func nativeRegistry() registryOps { return nil }
