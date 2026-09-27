package targetpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/lavinhoque33/statusforge/backend/internal/netguard"
)

var ErrRefused = errors.New("refused_by_policy")

type Policy struct {
	allowed  map[string]bool
	resolver func(context.Context, string) ([]net.IPAddr, error)
	dial     func(context.Context, string, string) (net.Conn, error)
}

func Parse(value string) (*Policy, error) {
	p := &Policy{
		allowed:  map[string]bool{},
		resolver: net.DefaultResolver.LookupIPAddr,
		dial:     (&net.Dialer{}).DialContext,
	}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		host, port, err := net.SplitHostPort(entry)
		if err != nil || !netguard.IsLoopbackHost(host) {
			return nil, fmt.Errorf(
				"STATUSFORGE_ALLOWED_TARGETS: each entry must be a loopback host:port",
			)
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("STATUSFORGE_ALLOWED_TARGETS: port must be 1–65535")
		}
		p.allowed[net.JoinHostPort(host, strconv.Itoa(n))] = true
	}
	if len(p.allowed) == 0 {
		return nil, fmt.Errorf("STATUSFORGE_ALLOWED_TARGETS: list must not be empty")
	}
	return p, nil
}

func (p *Policy) Validate(raw string) (string, string) {
	if len(raw) > 2048 || raw == "" {
		return "url_invalid", "URL must be absolute and at most 2048 bytes"
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" {
		return "url_invalid", "URL must be absolute and parseable"
	}
	if u.Scheme != "http" {
		return "scheme_not_allowed", "only http is allowed"
	}
	if u.User != nil {
		return "url_has_userinfo", "userinfo is not allowed"
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "url_has_fragment", "fragment is not allowed"
	}
	host := u.Hostname()
	if !netguard.IsLoopbackHost(host) {
		return "host_not_loopback", "host must be loopback"
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return "url_invalid", "port must be 1–65535"
	}
	key := net.JoinHostPort(host, strconv.Itoa(n))
	if !p.allowed[key] {
		return "target_not_allowed", fmt.Sprintf(
			"host:port %s is not in STATUSFORGE_ALLOWED_TARGETS",
			key,
		)
	}
	return "", ""
}

func (p *Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrRefused
	}
	key := net.JoinHostPort(host, port)
	if !p.allowed[key] {
		return nil, ErrRefused
	}
	ips, err := p.resolver(ctx, host)
	if err != nil {
		return nil, ErrRefused
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || !addr.Unmap().IsLoopback() {
			return nil, ErrRefused
		}
	}
	if len(ips) == 0 {
		return nil, ErrRefused
	}
	return p.dial(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}
