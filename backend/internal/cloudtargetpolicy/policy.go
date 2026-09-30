// Package cloudtargetpolicy is the cloud destination policy (ADR 0008 D6):
// an explicit https://host[:port] allowlist, a public-address guard applied
// when a monitor is saved and again at dial time, and pinned-IP TLS dialing.
// Only the Lambda binaries use it; the local binary keeps targetpolicy.
package cloudtargetpolicy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

// ErrRefused is the shared refusal error, so the checker stores a refused
// check as checker_problem / refused_by_policy.
var ErrRefused = targetpolicy.ErrRefused

// Resolver returns every address of host.
type Resolver func(ctx context.Context, host string) ([]net.IPAddr, error)

type Policy struct {
	allowed     map[string]bool
	runtimeHost string
	resolve     Resolver
	dialer      net.Dialer
	roots       *x509.CertPool
	// loopbackForTest is the single loopback address a test fixture may use;
	// the zero value (always, outside tests) permits none.
	loopbackForTest netip.Addr
}

// Parse builds the policy from STATUSFORGE_CLOUD_TARGETS and the Lambda
// runtime API address. An empty allowlist is valid and refuses every check.
// Errors name the variable and entry index, never the value.
func Parse(allowlist, runtimeAPI string) (*Policy, error) {
	p := &Policy{
		allowed:     map[string]bool{},
		runtimeHost: runtimeHost(runtimeAPI),
		resolve:     net.DefaultResolver.LookupIPAddr,
	}
	if strings.TrimSpace(allowlist) == "" {
		return p, nil
	}
	for i, entry := range strings.Split(allowlist, ",") {
		key, ok := p.origin(strings.TrimSpace(entry))
		if !ok {
			return nil, fmt.Errorf(
				"STATUSFORGE_CLOUD_TARGETS: entry %d must be https://host[:port] with a public host "+
					"and no userinfo, path, query, or fragment",
				i+1,
			)
		}
		p.allowed[key] = true
	}
	return p, nil
}

func runtimeHost(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.ToLower(value)
}

// origin returns the host:port key of one allowlist entry.
func (p *Policy) origin(entry string) (string, bool) {
	u, err := url.Parse(entry)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(entry, "#") {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	port, ok := portOf(u)
	if !ok || !validHost(host) || host == p.runtimeHost {
		return "", false
	}
	if ip, err := netip.ParseAddr(host); err == nil && !Public(ip) {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

func portOf(u *url.URL) (string, bool) {
	port := u.Port()
	if port == "" {
		return "443", true
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return strconv.Itoa(n), true
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return !strings.Contains(host, "%")
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// SetResolver replaces the DNS resolver. Every resolved address still has to
// pass the public-address check, so a resolver cannot loosen the policy.
func (p *Policy) SetResolver(r Resolver) { p.resolve = r }

// Empty reports whether the allowlist refuses every check.
func (p *Policy) Empty() bool { return len(p.allowed) == 0 }

// Validate is the static save-time check of a monitor URL. It returns a field
// error code and message, or two empty strings.
func (p *Policy) Validate(raw string) (string, string) {
	if len(raw) > 2048 || raw == "" {
		return "url_invalid", "URL must be absolute and at most 2048 bytes"
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" {
		return "url_invalid", "URL must be absolute and parseable"
	}
	if u.Scheme != "https" {
		return "scheme_not_allowed", "only https is allowed"
	}
	if u.User != nil {
		return "url_has_userinfo", "userinfo is not allowed"
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "url_has_fragment", "fragment is not allowed"
	}
	host := strings.ToLower(u.Hostname())
	port, ok := portOf(u)
	if !ok || !validHost(host) {
		return "url_invalid", "host and port must be valid"
	}
	if host == p.runtimeHost {
		return "host_not_allowed", "the Lambda runtime API host is never a target"
	}
	if ip, err := netip.ParseAddr(host); err == nil && !p.permitted(ip) {
		return "host_not_public", "host must be a public address"
	}
	key := net.JoinHostPort(host, port)
	if !p.allowed[key] {
		return "target_not_allowed", fmt.Sprintf(
			"host:port %s is not in STATUSFORGE_CLOUD_TARGETS",
			key,
		)
	}
	return "", ""
}

// CheckResolved is the save-time check with resolution: Validate, then every
// resolved address of the host must be public.
func (p *Policy) CheckResolved(ctx context.Context, raw string) error {
	if code, _ := p.Validate(raw); code != "" {
		return ErrRefused
	}
	u, _ := url.Parse(raw)
	_, err := p.addresses(ctx, strings.ToLower(u.Hostname()))
	return err
}

// addresses resolves host and refuses unless every address is permitted.
func (p *Policy) addresses(ctx context.Context, host string) ([]netip.Addr, error) {
	if host == p.runtimeHost {
		return nil, ErrRefused
	}
	var result []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		result = []netip.Addr{ip}
	} else {
		resolved, err := p.resolve(ctx, host)
		if err != nil {
			return nil, ErrRefused
		}
		for _, r := range resolved {
			ip, ok := netip.AddrFromSlice(r.IP)
			if !ok || r.Zone != "" {
				return nil, ErrRefused
			}
			// net.IP stores IPv4 in 16-byte form; judge and dial it as IPv4.
			result = append(result, ip.Unmap())
		}
	}
	if len(result) == 0 {
		return nil, ErrRefused
	}
	for _, ip := range result {
		if !p.permitted(ip) {
			return nil, ErrRefused
		}
	}
	return result, nil
}

func (p *Policy) permitted(ip netip.Addr) bool {
	if p.loopbackForTest.IsValid() && ip == p.loopbackForTest {
		return true
	}
	return Public(ip)
}

// DialContext refuses every plain connection: cloud checks are HTTPS only.
func (p *Policy) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, ErrRefused
}

// DialTLSContext re-checks the allowlist, resolves the host, refuses unless
// every address is public, dials the first validated address, and completes
// TLS with ServerName set to the declared host and normal verification.
func (p *Policy) DialTLSContext(ctx context.Context, _, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrRefused
	}
	host = strings.ToLower(host)
	if !p.allowed[net.JoinHostPort(host, port)] {
		return nil, ErrRefused
	}
	ips, err := p.addresses(ctx, host)
	if err != nil {
		return nil, err
	}
	conn, err := p.dialer.DialContext(ctx, "tcp", net.JoinHostPort(ips[0].String(), port))
	if err != nil {
		return nil, err
	}
	client := tls.Client(conn, &tls.Config{
		ServerName: host,
		RootCAs:    p.roots,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	})
	if err := client.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

// allowLoopbackForTest lets exactly one loopback address through the
// public-address check and trusts roots for TLS, so a local
// httptest.NewTLSServer can act as a target. It is unexported and only tests
// call it: no exported API or environment variable can loosen the policy.
func (p *Policy) allowLoopbackForTest(ip netip.Addr, roots *x509.CertPool) {
	if !ip.IsLoopback() {
		panic("cloudtargetpolicy: the test hook accepts only a loopback address")
	}
	p.loopbackForTest = ip
	p.roots = roots
}

var (
	nonPublic4 = prefixes(
		"0.0.0.0/8",       // "this network"
		"10.0.0.0/8",      // RFC 1918
		"100.64.0.0/10",   // shared address space (CGNAT)
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local: 169.254.169.254 IMDS, 169.254.100.1 Lambda metadata
		"172.16.0.0/12",   // RFC 1918
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"192.88.99.0/24",  // deprecated 6to4 relay anycast
		"192.168.0.0/16",  // RFC 1918
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, including broadcast
	)
	nonPublic6 = prefixes(
		"::/96",          // unspecified, loopback, deprecated IPv4-compatible
		"64:ff9b:1::/48", // local-use NAT64
		"100::/64",       // discard-only
		"2001::/32",      // Teredo (embeds IPv4)
		"2001:db8::/32",  // documentation
		"2002::/16",      // 6to4 (embeds IPv4)
		"3fff::/20",      // documentation
		"fc00::/7",       // unique local
		"fe80::/10",      // link-local
		"fec0::/10",      // deprecated site-local
		"ff00::/8",       // multicast
	)
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
)

func prefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, len(values))
	for i, v := range values {
		result[i] = netip.MustParsePrefix(v)
	}
	return result
}

// Public reports whether ip is a public unicast address. IPv4-mapped and
// well-known NAT64 addresses are judged by the IPv4 address they carry.
func Public(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	if ip.Is6() && nat64.Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	ranges := nonPublic6
	if ip.Is4() {
		ranges = nonPublic4
	}
	for _, r := range ranges {
		if r.Contains(ip) {
			return false
		}
	}
	return true
}
