package samplejob

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeployControl(t *testing.T) {
	token := "sfd_secret-must-not-be-returned"
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token ||
			r.URL.Path != "/ingest/applications/app/deployments" {
			t.Errorf("incorrect deploy request")
		}
		var m map[string]any
		if json.NewDecoder(r.Body).Decode(&m) != nil || m["version"] != "release" ||
			m["deploymentId"] != "build-1" {
			t.Errorf("incorrect marker: %+v", m)
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer receiver.Close()
	cfg := DefaultConfig()
	cfg.DeployURL = receiver.URL + "/ingest/applications/app/deployments"
	cfg.DeployToken = token
	s, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	request := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().
			ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/control/deploy", bytes.NewBufferString(body)))
		return w
	}
	response := request(`{"version":"release","deploymentId":"build-1"}`)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"apiStatus":202`) ||
		strings.Contains(response.Body.String(), token) {
		t.Fatalf("deploy response: %d %s", response.Code, response.Body.String())
	}
	if response = request(`{"version":"release","unexpected":true}`); response.Code != 400 {
		t.Fatalf("unknown field: %d", response.Code)
	}
	cfg.DeployToken = ""
	unconfigured, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	response = httptest.NewRecorder()
	unconfigured.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/control/deploy", strings.NewReader(`{"version":"release"}`)))
	if response.Code != 409 {
		t.Fatalf("unconfigured: %d", response.Code)
	}
}

func TestDeployRejectsRemoteTarget(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DeployURL = "http://example.com/ingest/applications/app/deployments"
	if _, e := New(cfg); e == nil {
		t.Fatal("non-loopback deploy URL accepted")
	}
}
