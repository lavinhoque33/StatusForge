package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary double as the fixture process, so the smoke
// test below exercises the real main path: environment configuration, the
// ticker, the report request, signal handling, and graceful shutdown.
func TestMain(m *testing.M) {
	if os.Getenv("SAMPLE_JOB_TEST_HELPER") == "1" {
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// syncBuffer is a writer the test can read while the fixture process writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestFixtureProcessReportsAndShutsDownGracefully(t *testing.T) {
	const token = "sfh_process-smoke-token"
	reports := make(chan []byte, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want the configured bearer token", got)
		}
		select {
		case reports <- body:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer receiver.Close()

	addr := freeLoopbackAddr(t)
	logs := &syncBuffer{}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(
		os.Environ(),
		"SAMPLE_JOB_TEST_HELPER=1",
		"STATUSFORGE_SAMPLE_JOB_ADDR="+addr,
		"STATUSFORGE_SAMPLE_JOB_REPORT_URL="+receiver.URL,
		"STATUSFORGE_SAMPLE_JOB_TOKEN="+token,
		"STATUSFORGE_SAMPLE_JOB_INTERVAL_SECONDS=1",
	)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var (
		exitOnce sync.Once
		exitErr  error
		exitDone = make(chan struct{})
	)
	go func() {
		exitOnce.Do(func() {
			exitErr = cmd.Wait()
			close(exitDone)
		})
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		exitOnce.Do(func() {})
		select {
		case <-exitDone:
		case <-time.After(10 * time.Second):
		}
	})

	waitForControl(t, "http://"+addr+"/status")

	select {
	case body := <-reports:
		var report struct {
			Status     string `json:"status"`
			RunID      string `json:"runId"`
			FinishedAt string `json:"finishedAt"`
			ExitCode   int    `json:"exitCode"`
		}
		if err := json.Unmarshal(body, &report); err != nil {
			t.Fatalf("report body %s: %v", body, err)
		}
		if report.Status != "success" || report.ExitCode != 0 || report.RunID == "" {
			t.Errorf("report = %+v, want a successful run with a run ID", report)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the fixture sent no report within 10s; logs:\n%s", logs.String())
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exitDone:
		if exitErr != nil {
			t.Fatalf("fixture exited with %v; logs:\n%s", exitErr, logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the fixture did not exit after SIGTERM; logs:\n%s", logs.String())
	}

	output := logs.String()
	if !strings.Contains(output, "sample job listening") ||
		!strings.Contains(output, "sample job stopped") {
		t.Errorf("logs do not show a clean start and stop:\n%s", output)
	}
	if strings.Contains(output, token) {
		t.Errorf("process logs contain the plaintext token:\n%s", output)
	}
}

// waitForControl polls the fixture's status route until it answers.
func waitForControl(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the fixture's control route at %s never became ready", url)
}

// freeLoopbackAddr reserves and releases a loopback port so the fixture process
// can bind it.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
