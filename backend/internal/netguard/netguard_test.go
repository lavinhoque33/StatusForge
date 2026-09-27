package netguard

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	for _, tt := range []struct {
		host string
		want bool
	}{
		{"localhost", true}, {"127.0.0.1", true}, {"127.0.0.2", true},
		{"127.255.255.255", true}, {"::1", true},
		{"", false}, {"LOCALHOST", false}, {"localhost.localdomain", false},
		{"0.0.0.0", false}, {"::", false}, {"192.168.1.1", false},
		{"8.8.8.8", false}, {"example.com", false}, {"::ffff:127.0.0.1", false},
	} {
		t.Run(tt.host, func(t *testing.T) {
			if got := IsLoopbackHost(tt.host); got != tt.want {
				t.Errorf("IsLoopbackHost(%q) = %v; want %v", tt.host, got, tt.want)
			}
		})
	}
}
