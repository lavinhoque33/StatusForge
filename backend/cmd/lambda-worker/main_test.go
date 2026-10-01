package main

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

const mainSentinel = "STATUSFORGE_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(mainSentinel) == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// runMain re-executes the test binary as the real command with only env.
func runMain(t *testing.T, env ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append([]string{mainSentinel + "=1"}, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// outside Lambda the binary refuses before reading configuration or
// credentials; with a runtime API but no configuration it names the missing
// variable and never contacts anything.
func TestRefusesOutsideLambda(t *testing.T) {
	out, code := runMain(t, "STATUSFORGE_TABLE=statusforge")
	if code != 1 || strings.TrimSpace(out) != "not running in AWS Lambda" {
		t.Fatalf("exit %d, output %q", code, out)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var accepted atomic.Int64
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			conn.Close()
		}
	}()
	out, code = runMain(t, "AWS_LAMBDA_RUNTIME_API="+listener.Addr().String())
	if code != 1 || !strings.HasPrefix(out, "STATUSFORGE_TABLE: required") {
		t.Fatalf("exit %d, output %q", code, out)
	}
	if accepted.Load() != 0 {
		t.Fatalf("contacted the runtime API %d times", accepted.Load())
	}
}
