package netguard

import "net/netip"

// IsLoopbackHost accepts only literal loopback IP addresses or localhost.
// Hostnames other than localhost are not resolved, preventing DNS from changing
// the safety of a configured listener or endpoint after validation.
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback() && !ip.Is4In6()
}
