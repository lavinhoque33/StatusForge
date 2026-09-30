package cloudtargetpolicy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

// every non-public range of ADR 0008 D6 is refused, including the
// IPv4-mapped and NAT64 forms and the metadata addresses.
func TestDrill15NonPublicAddressesRefused(t *testing.T) {
	refused := []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "10.255.255.255", "100.64.0.1", "100.127.255.254",
		"127.0.0.1", "127.8.9.10", "169.254.169.254", "169.254.100.1", "169.254.0.1",
		"172.16.0.1", "172.31.255.255", "192.0.0.8", "192.0.2.1", "192.168.1.1",
		"198.18.0.1", "198.19.255.255", "198.51.100.7", "203.0.113.9", "224.0.0.1",
		"239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "2001:db8::1",
		"3fff::1", "fec0::1", "100::1",
		// IPv4-mapped forms.
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1", "::ffff:169.254.100.1",
		// NAT64 forms (well-known prefix carrying a private address, and local-use).
		"64:ff9b::a9fe:a9fe", "64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b:1::8.8.8.8",
		// IPv4-compatible, 6to4, and Teredo embed IPv4 too.
		"::127.0.0.1", "2002:a9fe:a9fe::1", "2001:0:4136:e378::1",
	}
	for _, value := range refused {
		if Public(netip.MustParseAddr(value)) {
			t.Errorf("%s accepted as public", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700::1111", "64:ff9b::808:808", "::ffff:8.8.8.8"} {
		if !Public(netip.MustParseAddr(value)) {
			t.Errorf("%s refused as non-public", value)
		}
	}
	if Public(netip.MustParseAddr("fe80::1%eth0")) {
		t.Error("zoned address accepted")
	}
}

func publicResolver(ip string) Resolver {
	return func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
}

func TestDrill15SaveTimeRefusals(t *testing.T) {
	p, err := Parse("https://status.example.com, https://api.example.org:8443", "127.0.0.1:9001")
	if err != nil {
		t.Fatal(err)
	}
	p.SetResolver(publicResolver("93.184.216.34"))
	cases := map[string]string{
		"https://status.example.com/health":         "",
		"https://STATUS.example.com:443/health?x=1": "",
		"https://api.example.org:8443/":             "",
		"http://status.example.com/health":          "scheme_not_allowed",
		"https://user:pw@status.example.com/":       "url_has_userinfo",
		"https://status.example.com/#frag":          "url_has_fragment",
		"https://other.example.com/":                "target_not_allowed",
		"https://status.example.com:8443/":          "target_not_allowed",
		"https://api.example.org/":                  "target_not_allowed",
		"https://127.0.0.1:9001/2018-06-01/runtime": "host_not_allowed",
		"https://169.254.169.254/latest/meta-data":  "host_not_public",
		"https://169.254.100.1/":                    "host_not_public",
		"https://[::ffff:169.254.169.254]/":         "host_not_public",
		"https://[64:ff9b::a9fe:a9fe]/":             "host_not_public",
		"https://10.0.0.1/":                         "host_not_public",
		"status.example.com/health":                 "url_invalid",
		"":                                          "url_invalid",
	}
	for raw, want := range cases {
		if code, _ := p.Validate(raw); code != want {
			t.Errorf("%q: code %q, want %q", raw, code, want)
		}
	}
	if err := p.CheckResolved(t.Context(), "https://status.example.com/health"); err != nil {
		t.Fatalf("public resolution refused: %v", err)
	}
	for _, private := range []string{"10.1.2.3", "169.254.169.254", "::ffff:127.0.0.1", "64:ff9b::a9fe:a9fe"} {
		p.SetResolver(publicResolver(private))
		if err := p.CheckResolved(t.Context(), "https://status.example.com/"); !errors.Is(
			err,
			ErrRefused,
		) {
			t.Errorf("resolution to %s accepted: %v", private, err)
		}
	}
	p.SetResolver(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.1")}}, nil
	})
	if err := p.CheckResolved(t.Context(), "https://status.example.com/"); !errors.Is(
		err,
		ErrRefused,
	) {
		t.Errorf("mixed public/private resolution accepted: %v", err)
	}
}

func TestDrill15AllowlistParsing(t *testing.T) {
	for _, bad := range []string{
		"http://status.example.com", "https://user@status.example.com",
		"https://status.example.com/path", "https://status.example.com/?q=1",
		"https://status.example.com/#f", "https://10.0.0.1", "https://169.254.169.254",
		"https://[::ffff:127.0.0.1]", "https://runtime.internal:9001", "status.example.com",
		"https://status.example.com:0", "https://status.example.com,",
	} {
		_, err := Parse(bad, "runtime.internal:9001")
		if err == nil {
			t.Errorf("%q accepted", bad)
			continue
		}
		if !strings.HasPrefix(err.Error(), "STATUSFORGE_CLOUD_TARGETS") ||
			strings.Contains(err.Error(), "status.example.com") {
			t.Errorf("error must name the variable without echoing the value: %v", err)
		}
	}
	empty, err := Parse("", "127.0.0.1:9001")
	if err != nil || !empty.Empty() {
		t.Fatalf("empty allowlist: %v", err)
	}
	if code, _ := empty.Validate("https://status.example.com/"); code != "target_not_allowed" {
		t.Fatalf("empty allowlist accepted a URL: %q", code)
	}
}

// tlsFixture serves HTTPS on loopback; the policy reaches it only through the
// unexported test hook, under the certificate's host name example.com.
func tlsFixture(t *testing.T, handler http.Handler) (*Policy, string, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	port := u.Port()
	p, err := Parse("https://example.com:"+port, "127.0.0.1:9001")
	if err != nil {
		t.Fatal(err)
	}
	p.SetResolver(publicResolver("127.0.0.1"))
	p.allowLoopbackForTest(
		netip.MustParseAddr("127.0.0.1"),
		server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs,
	)
	return p, "https://example.com:" + port, &hits
}

func check(p *Policy, raw string) monitor.Observation {
	m := monitor.New("t", monitor.Check{
		URL: raw, Method: "GET", ExpectedStatus: 200, DeadlineMs: 2000,
		MaxBodyBytes: monitor.MaxBodyBytes,
	}, time.Now())
	return checker.New(p, time.Now).Run(context.Background(), m)
}

func TestDrill15PinnedTLSDialThroughTestHook(t *testing.T) {
	p, origin, hits := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.ServerName != "example.com" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	o := check(p, origin+"/health")
	if o.Outcome != "healthy" || o.ObservedStatus == nil || *o.ObservedStatus != 200 {
		t.Fatalf("pinned TLS check: %+v", o)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}
	// Plain HTTP is never dialled, even to an allowlisted host.
	if o := check(p, "http://example.com:"+strings.TrimPrefix(origin, "https://example.com:")+"/"); o.Reason != "refused_by_policy" {
		t.Fatalf("http:// check: %+v", o)
	}
	// Rebinding: public at save, private at dial.
	p.SetResolver(publicResolver("93.184.216.34"))
	if err := p.CheckResolved(t.Context(), origin+"/health"); err != nil {
		t.Fatal(err)
	}
	p.SetResolver(publicResolver("10.0.0.7"))
	if o := check(p, origin+"/health"); o.Outcome != "checker_problem" ||
		o.Reason != "refused_by_policy" {
		t.Fatalf("rebinding resolver: %+v", o)
	}
	// The hook admits exactly one loopback address.
	p.SetResolver(publicResolver("127.0.0.2"))
	if o := check(p, origin+"/health"); o.Reason != "refused_by_policy" {
		t.Fatalf("second loopback address: %+v", o)
	}
	if hits.Load() != 1 {
		t.Fatalf("refused checks reached the target: hits = %d", hits.Load())
	}
}

func TestDrill15RedirectsNotFollowed(t *testing.T) {
	var private atomic.Int64
	inner := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		private.Add(1)
	}))
	defer inner.Close()
	p, origin, hits := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inner.URL+"/latest/meta-data", http.StatusFound)
	}))
	o := check(p, origin+"/")
	if o.Outcome != "failing" || o.Reason != "wrong_status" || o.ObservedStatus == nil ||
		*o.ObservedStatus != http.StatusFound {
		t.Fatalf("redirect: %+v", o)
	}
	if hits.Load() != 1 || private.Load() != 0 {
		t.Fatalf("redirect followed: target %d, redirect location %d", hits.Load(), private.Load())
	}
}

func TestDrill15RuntimeAPIHostAndUntrustedCertificate(t *testing.T) {
	p, err := Parse("https://status.example.com", "status.example.com:9001")
	if err == nil {
		t.Fatalf("runtime API host accepted in allowlist: %+v", p)
	}
	p, origin, hits := tlsFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	p.roots = nil // system roots do not trust the fixture
	if o := check(p, origin+"/"); o.Outcome != "failing" || o.Reason != "connection_error" {
		t.Fatalf("untrusted certificate: %+v", o)
	}
	if hits.Load() != 0 {
		t.Fatal("request sent over an unverified connection")
	}
	runtime, err := Parse("", "169.254.100.1:9001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.addresses(t.Context(), "169.254.100.1"); !errors.Is(err, ErrRefused) {
		t.Fatalf("runtime API address resolved: %v", err)
	}
}
