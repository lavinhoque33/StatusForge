package notifyreceiver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func startFixture(t *testing.T) (*Server, *httptest.Server, *http.Client) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.TimeoutDelay = 250 * time.Millisecond
	fixture, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(func() { client.CloseIdleConnections() })
	return fixture, server, client
}

func send(
	t *testing.T,
	client *http.Client,
	method, url, key, body string,
) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return client.Do(req)
}

func received(t *testing.T, client *http.Client, url string) []Entry {
	t.Helper()
	resp, err := client.Get(url + "/received")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET received: %d", resp.StatusCode)
	}
	var result struct {
		Received []Entry `json:"received"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.Received
}

func TestModesOverLoopback(t *testing.T) {
	fixture, server, client := startFixture(t)
	for _, tc := range []struct {
		mode   Mode
		status int
		stored bool
	}{
		{ModeAccept, http.StatusNoContent, true},
		{ModeFail, http.StatusServiceUnavailable, false},
		{ModeReject, http.StatusBadRequest, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			if err := fixture.SetMode(tc.mode); err != nil {
				t.Fatal(err)
			}
			before := len(received(t, client, server.URL))
			resp, err := send(
				t,
				client,
				"POST",
				server.URL+"/notify",
				string(tc.mode),
				`{"event":1}`,
			)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			got := received(t, client, server.URL)
			want := before
			if tc.stored {
				want++
			}
			if len(got) != want {
				t.Fatalf("entries = %d, want %d", len(got), want)
			}
		})
	}
}

func TestDropStoresRequestAndClosesWithoutResponse(t *testing.T) {
	fixture, server, client := startFixture(t)
	if err := fixture.SetMode(ModeDrop); err != nil {
		t.Fatal(err)
	}
	for n := range 2 {
		resp, err := send(
			t,
			client,
			"POST",
			server.URL+"/notify",
			"incident:opened",
			`{"id":"incident"}`,
		)
		if resp != nil {
			resp.Body.Close()
			t.Fatalf("request %d got response %d", n, resp.StatusCode)
		}
		if !errors.Is(err, io.EOF) {
			t.Fatalf("request %d: expected EOF from closed connection, got %v", n, err)
		}
	}
	entries := received(t, client, server.URL)
	if len(entries) != 2 || entries[0].Duplicate || !entries[1].Duplicate ||
		entries[0].IdempotencyKey != "incident:opened" ||
		!bytes.Equal(entries[0].Body, []byte(`{"id":"incident"}`)) {
		t.Fatalf("drop entries = %+v", entries)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", entries[0].ReceivedAt); err != nil {
		t.Fatalf("receivedAt: %v", err)
	}
}

func TestTimeoutStoresRequestBeforeClientDeadline(t *testing.T) {
	fixture, server, client := startFixture(t)
	if err := fixture.SetMode(ModeTimeout); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		server.URL+"/notify",
		strings.NewReader(`{"id":1}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Idempotency-Key", "timeout-key")
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
		t.Fatalf("unexpected response: %d", resp.StatusCode)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	entries := received(t, client, server.URL)
	if len(entries) != 1 || entries[0].IdempotencyKey != "timeout-key" {
		t.Fatalf("timeout entries = %+v", entries)
	}
}

func TestBodyLimitAndClearResetsDuplicateHistory(t *testing.T) {
	_, server, client := startFixture(t)
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{"at limit", `"` + strings.Repeat("x", maxBodyBytes-2) + `"`, http.StatusNoContent},
		{"over limit", `"` + strings.Repeat("x", maxBodyBytes-1) + `"`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := send(t, client, "POST", server.URL+"/notify", "same", tc.body)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
		})
	}
	if len(received(t, client, server.URL)) != 1 {
		t.Fatal("oversized body was stored")
	}
	resp, err := send(t, client, "DELETE", server.URL+"/received", "", "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", resp.StatusCode)
	}
	resp, err = send(t, client, "POST", server.URL+"/notify", "same", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if entries := received(t, client, server.URL); len(entries) != 1 || entries[0].Duplicate {
		t.Fatalf("duplicate history not reset: %+v", entries)
	}
}

func TestModeControlRejectsInvalidInputWithoutChangingMode(t *testing.T) {
	_, server, client := startFixture(t)
	for _, body := range []string{`{"mode":"unknown"}`, `{"mode":"accept"}{}`, `{`, `{"mode":"drop","extra":1}`} {
		resp, err := send(t, client, "PUT", server.URL+"/control/mode", "", body)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Allowed []string `json:"allowed"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || len(result.Allowed) != 5 {
			t.Fatalf("invalid mode response %d: %+v", resp.StatusCode, result)
		}
	}
	resp, err := client.Get(server.URL + "/control/mode")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var mode struct {
		Mode Mode `json:"mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mode); err != nil || mode.Mode != ModeAccept {
		t.Fatalf("mode = %q, decode error = %v", mode.Mode, err)
	}
	resp, err = send(t, client, "PUT", server.URL+"/control/mode", "", `{"mode":"drop"}`)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid mode status = %d", resp.StatusCode)
	}
}

func TestConfigRejectsNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8091", ":8091", "192.168.1.10:8091", "example.com:8091", "[::]:8091", "127.0.0.1:0"} {
		_, err := LoadConfig(func(name string) (string, bool) { return addr, name == EnvAddr })
		if err == nil {
			t.Errorf("accepted unsafe address %q", addr)
		}
	}
	cfg, err := LoadConfig(func(string) (string, bool) { return "[::1]:8091", true })
	if err != nil || cfg.Addr != "[::1]:8091" {
		t.Fatalf("loopback IPv6 config = %+v, error = %v", cfg, err)
	}
}
