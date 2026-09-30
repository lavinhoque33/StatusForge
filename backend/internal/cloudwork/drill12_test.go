package cloudwork_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
)

// scratchDynamo starts a throwaway in-memory DynamoDB Local container on a
// free loopback port in 8300–8309 and removes it (by its exact name) when the
// test ends. It never touches the Compose instance.
func scratchDynamo(t *testing.T) (string, *dynamodb.Client) {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("this test needs Docker for a scratch DynamoDB Local; docker not found")
	}
	if out, err := exec.Command(docker, "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("this test needs Docker for a scratch DynamoDB Local; docker unavailable: %s",
			strings.TrimSpace(string(out)))
	}
	port := 0
	for p := 8300; p <= 8309; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			l.Close()
			port = p
			break
		}
	}
	if port == 0 {
		t.Skip("no free port in 127.0.0.1:8300–8309")
	}
	name := "sf-m7a-ddb-" + strings.ToLower(rand.Text()[:8])
	out, err := exec.Command(
		docker,
		"run",
		"--rm",
		"-d",
		"--name",
		name,
		"-p",
		fmt.Sprintf("127.0.0.1:%d:8000", port),
		"amazon/dynamodb-local:3.3.1",
		"-jar",
		"DynamoDBLocal.jar",
		"-inMemory",
		"-disableTelemetry",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("start scratch DynamoDB Local: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command(docker, "unpause", name).Run()
		_ = exec.Command(docker, "stop", name).Run()
	})
	db := localdynamo.New(fmt.Sprintf("http://127.0.0.1:%d", port), "127.0.0.1", "local", "local", "local").
		DynamoDB()
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		_, err := db.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)})
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scratch DynamoDB Local not ready: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return name, db
}

func dockerRun(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker %v: %v: %s", args, err, out)
	}
}

// the endpoint becomes unavailable mid-batch. The affected record
// and every later one are reported; later records are not claimed; nothing
// is stored as a target failure.
func TestDrill12DynamoDBUnavailableMidBatch(t *testing.T) {
	name, db := scratchDynamo(t)
	d := newDrillOn(t, db)
	d.declare(declare("db-a", 60, 1000), declare("db-b", 60, 1000), declare("db-c", 60, 1000))
	d.start("db-a", 60)
	d.pass(d.store())
	sent := d.queue.take()
	if len(sent) != 3 {
		t.Fatalf("sent: %v", sent)
	}
	s := d.store()
	// The endpoint stops answering while the first record's check runs.
	d.checker.during = func() { dockerRun(t, "pause", name) }
	r, counts := d.deliver(s, sent...)
	if got := failed(r); !slices.Equal(got, []string{"m0", "m1", "m2"}) {
		t.Fatalf("failures: %v", got)
	}
	if counts["dependency_failure"] != 1 || counts["deferred"] != 2 || counts["recorded"] != 0 ||
		d.checker.callCount() != 1 {
		t.Fatalf("counts %v, checks %d", counts, d.checker.callCount())
	}
	dockerRun(t, "unpause", name)
	first := mustMessage(t, sent[0])
	for i, body := range sent {
		msg := mustMessage(t, body)
		if obs := d.observations(msg.MonitorID); len(obs) != 0 {
			t.Fatalf("record %d stored an observation: %+v", i, obs)
		}
		if gaps := d.gaps(msg.MonitorID); len(gaps) != 0 {
			t.Fatalf("record %d stored a gap: %+v", i, gaps)
		}
		w := d.works(msg.MonitorID)[0]
		switch {
		case msg.MonitorID == first.MonitorID && (w.State != "claimed" || w.Attempts != 1):
			t.Fatalf("affected record: %+v", w)
		case msg.MonitorID != first.MonitorID && (w.State != "pending" || w.Attempts != 0):
			t.Fatalf("later record %d claimed: %+v", i, w)
		}
	}
	// Stopped outright: the first read fails, the rest are not attempted.
	dockerRun(t, "stop", name)
	r, counts = d.deliver(s, sent[1:]...)
	if got := failed(r); !slices.Equal(got, []string{"m0", "m1"}) ||
		counts["dependency_failure"] != 1 || counts["deferred"] != 1 {
		t.Fatalf("stopped endpoint: %v %v", got, counts)
	}
	if d.checker.callCount() != 1 {
		t.Fatal("a check ran without the table")
	}
}
