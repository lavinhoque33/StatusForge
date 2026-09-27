package targetpolicy

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestURLValidationOrder(t *testing.T) {
	p, err := Parse("127.0.0.1:80, [::1]:8090,localhost:80,127.0.0.1:080")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ url, code string }{{"not a URL", "url_invalid"}, {"https://user:pw@8.8.8.8/path#frag", "scheme_not_allowed"}, {"http://user:pw@8.8.8.8/path#frag", "url_has_userinfo"}, {"http://8.8.8.8/path#frag", "url_has_fragment"}, {"http://8.8.8.8/", "host_not_loopback"}, {"http://127.0.0.1:9090/", "target_not_allowed"}, {"http://127.0.0.1/a?q=1", ""}, {"http://[::1]:8090/", ""}, {"http://localhost/a", ""}} {
		code, _ := p.Validate(tc.url)
		if code != tc.code {
			t.Errorf("%q: got %q, want %q", tc.url, code, tc.code)
		}
	}
}
func TestDialRefusesNonAllowedAndNonLoopback(t *testing.T) {
	p, _ := Parse("localhost:80")
	if _, err := p.DialContext(context.Background(), "tcp", "127.0.0.1:80"); !errors.Is(err, ErrRefused) {
		t.Fatalf("unlisted: %v", err)
	}
	p.resolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	p.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("attempted dial despite unsafe resolution")
		return nil, nil
	}
	if _, err := p.DialContext(context.Background(), "tcp", "localhost:80"); !errors.Is(err, ErrRefused) {
		t.Fatalf("unsafe resolution: %v", err)
	}
}
