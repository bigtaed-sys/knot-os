//go:build !linux

package api

// bringUSBEthUp — no-op on non-Linux dev hosts. /sys/class/net
// doesn't exist; the capability probe returns an empty Eth list
// anyway.
func bringUSBEthUp() {}
