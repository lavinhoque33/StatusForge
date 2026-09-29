// Command capacity runs isolated, loopback-only, reproducible DynamoDB Local loads.
// It never stops or changes the database container. The outage closes both new
// and established connections at an in-process loopback TCP proxy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
)

const (
	apiAddr    = "127.0.0.1:8200"
	targetAddr = "127.0.0.1:8201"
	proxyAddr  = "127.0.0.1:8202"
)

var (
	apiLogLevel = new(string)
	apiWorkers  = new(int)
)

type (
	point struct {
		At               string  `json:"at"`
		APIReady         int     `json:"apiReady"`
		APICPUPercent    float64 `json:"apiCpuPercent"`
		APIRSSBytes      int64   `json:"apiRssBytes"`
		DynamoCPUPercent float64 `json:"dynamoCpuPercent"`
		DynamoRSSBytes   int64   `json:"dynamoRssBytes"`
		// Scheduler is the API's own coverage state from GET /api/overview.
		Scheduler *schedulerCoverage `json:"scheduler,omitempty"`
	}
	schedulerCoverage struct {
		State        string `json:"state"`
		DueChecks    int    `json:"dueChecks"`
		MissedChecks int    `json:"missedChecks"`
	}
	latency struct {
		P50     float64 `json:"p50Ms"`
		P95     float64 `json:"p95Ms"`
		Max     float64 `json:"maxMs"`
		Samples int     `json:"samples"`
	}
	stage struct {
		Name               string  `json:"name"`
		Monitors           int     `json:"monitors"`
		DurationSeconds    float64 `json:"durationSeconds"`
		StartedAt          string  `json:"startedAt"`
		FinishedAt         string  `json:"finishedAt"`
		Observations       int     `json:"observations"`
		ChecksPerMinute    float64 `json:"checksPerMinute"`
		ChecksByMinute     []int   `json:"checksByMinute"`
		Gaps               int     `json:"gaps"`
		MissedSlots        int     `json:"missedSlots"`
		GapsNearDisruption int     `json:"gapsNearDisruption"`
		DuplicateSlots     int     `json:"duplicateSlots"`
		Delay              latency `json:"scheduleDelay"`
		Overview           latency `json:"overview"`
		Summary            latency `json:"summary"`
		Samples            []point `json:"samples"`
		Transitions        []point `json:"readinessTransitions"`
		DisruptionAt       string  `json:"disruptionAt,omitempty"`
		RestoredAt         string  `json:"restoredAt,omitempty"`
		RecoveredAt        string  `json:"recoveredAt,omitempty"`
		RecoverySeconds    float64 `json:"recoverySeconds,omitempty"`
		// SchedulerStates counts samples by the API's scheduler coverage state.
		SchedulerStates map[string]int `json:"schedulerStates,omitempty"`
		Errors          []string       `json:"errors,omitempty"`
	}
	result struct {
		StartedAt  string            `json:"startedAt"`
		FinishedAt string            `json:"finishedAt"`
		Machine    map[string]string `json:"machine"`
		Versions   map[string]string `json:"versions"`
		Parameters map[string]any    `json:"parameters"`
		Stages     []stage           `json:"stages"`
	}
)

type proxy struct {
	listener net.Listener
	mu       sync.Mutex
	cut      bool
	conns    map[net.Conn]struct{}
}

func newProxy() (*proxy, error) {
	l, e := net.Listen("tcp", proxyAddr)
	if e != nil {
		return nil, e
	}
	p := &proxy{listener: l, conns: map[net.Conn]struct{}{}}
	go p.serve()
	return p, nil
}

func (p *proxy) serve() {
	for {
		c, e := p.listener.Accept()
		if e != nil {
			return
		}
		p.mu.Lock()
		cut := p.cut
		if !cut {
			p.conns[c] = struct{}{}
		}
		p.mu.Unlock()
		if cut {
			c.Close()
			continue
		}
		go p.forward(c)
	}
}

func (p *proxy) forward(c net.Conn) {
	remote, e := net.DialTimeout("tcp", "127.0.0.1:8000", time.Second)
	if e != nil {
		c.Close()
		p.mu.Lock()
		delete(p.conns, c)
		p.mu.Unlock()
		return
	}
	p.mu.Lock()
	if p.cut {
		p.mu.Unlock()
		remote.Close()
		c.Close()
		return
	}
	p.conns[remote] = struct{}{}
	p.mu.Unlock()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, remote); done <- struct{}{} }()
	<-done
	c.Close()
	remote.Close()
	p.mu.Lock()
	delete(p.conns, c)
	delete(p.conns, remote)
	p.mu.Unlock()
}

func (p *proxy) outage(cut bool) {
	p.mu.Lock()
	p.cut = cut
	if cut {
		for c := range p.conns {
			c.Close()
			delete(p.conns, c)
		}
	}
	p.mu.Unlock()
}
func (p *proxy) close() { p.outage(true); p.listener.Close() }

func runProcess(ctx context.Context, path string, env []string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, path)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if e := cmd.Start(); e != nil {
		return nil, e
	}
	return cmd, nil
}

func stop(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(12 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func get(client *http.Client, path string) (int, time.Duration, error) {
	return getJSON(client, path, nil)
}

// getJSON times one GET and, when into is non-nil, decodes a 200 body into it.
func getJSON(client *http.Client, path string, into any) (int, time.Duration, error) {
	start := time.Now()
	r, e := client.Get("http://" + apiAddr + path)
	if e != nil {
		return 0, time.Since(start), e
	}
	defer r.Body.Close()
	body, e := io.ReadAll(r.Body)
	elapsed := time.Since(start)
	if e == nil && into != nil && r.StatusCode == 200 {
		e = json.Unmarshal(body, into)
	}
	return r.StatusCode, elapsed, e
}

func waitReady(ctx context.Context, client *http.Client) error {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		code, _, _ := get(client, "/api/health/ready")
		if code == 200 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func measure(values []float64) latency {
	sort.Float64s(values)
	n := len(values)
	if n == 0 {
		return latency{}
	}
	return latency{
		P50:     values[(n-1)/2],
		P95:     values[int(math.Ceil(float64(n)*.95))-1],
		Max:     values[n-1],
		Samples: n,
	}
}

func proc(pid int) (int64, int64) {
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return 0, 0
	}
	parts := strings.Fields(string(raw[strings.LastIndex(string(raw), ")")+2:]))
	if len(parts) < 22 {
		return 0, 0
	}
	usr, _ := strconv.ParseInt(parts[11], 10, 64)
	sys, _ := strconv.ParseInt(parts[12], 10, 64)
	status, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil {
		return usr + sys, 0
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return usr + sys, kb * 1024
		}
	}
	return usr + sys, 0
}

func command(ctx context.Context, args ...string) string {
	out, e := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	if e != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(out))
}

func daemonPID(ctx context.Context) int {
	id := command(ctx, "docker", "compose", "ps", "-q", "dynamodb")
	if id == "" || id == "unavailable" {
		return 0
	}
	pid, _ := strconv.Atoi(command(ctx, "docker", "inspect", "-f", "{{.State.Pid}}", id))
	return pid
}

func stats(ctx context.Context, id string) float64 {
	if id == "" || id == "unavailable" {
		return 0
	}
	raw := command(ctx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}", id)
	v, _ := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
	return v
}

func create(ctx context.Context, client *http.Client, count int) ([]string, error) {
	ids := make([]string, 0, count)
	for i := range count {
		body := fmt.Sprintf(
			`{"name":"Capacity %03d","intervalSeconds":10,"check":{"url":"http://%s/healthy","method":"GET","expectedStatus":200,"deadlineMs":1000}}`,
			i,
			targetAddr,
		)
		req, e := http.NewRequestWithContext(
			ctx,
			"POST",
			"http://"+apiAddr+"/api/monitors",
			strings.NewReader(body),
		)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		var m struct {
			ID string `json:"id"`
		}
		e = json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		if e != nil || resp.StatusCode != 201 {
			return nil, fmt.Errorf("create monitor %d status %d: %w", i, resp.StatusCode, e)
		}
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func scan(
	ctx context.Context,
	db *dynamodb.Client,
	table string,
	start, end time.Time,
	s *stage,
) error {
	var cursor map[string]types.AttributeValue
	seen := map[string]bool{}
	delay := []float64{}
	s.ChecksByMinute = make([]int, int(math.Ceil(s.DurationSeconds/60)))
	disruption, _ := time.Parse(time.RFC3339Nano, s.DisruptionAt)
	restored, _ := time.Parse(time.RFC3339Nano, s.RestoredAt)
	for {
		out, e := db.Scan(
			ctx,
			&dynamodb.ScanInput{
				TableName:            aws.String(table),
				ProjectionExpression: aws.String("PK,SK,dueAt,startedAt,recordedAt,missedCount"),
				ExclusiveStartKey:    cursor,
			},
		)
		if e != nil {
			return e
		}
		for _, item := range out.Items {
			pk, _ := item["PK"].(*types.AttributeValueMemberS)
			sk, _ := item["SK"].(*types.AttributeValueMemberS)
			if pk == nil || sk == nil {
				continue
			}
			if strings.HasPrefix(sk.Value, "OBS#") {
				started, _ := item["startedAt"].(*types.AttributeValueMemberS)
				due, _ := item["dueAt"].(*types.AttributeValueMemberS)
				if started == nil || due == nil {
					continue
				}
				at, e := time.Parse(time.RFC3339Nano, started.Value)
				if e != nil || at.Before(start) || at.After(end) {
					continue
				}
				s.Observations++
				minute := int(at.Sub(start).Minutes())
				if minute >= 0 && minute < len(s.ChecksByMinute) {
					s.ChecksByMinute[minute]++
				}
				slot := pk.Value + "/" + due.Value
				if seen[slot] {
					s.DuplicateSlots++
				}
				seen[slot] = true
				scheduled, e := time.Parse(time.RFC3339Nano, due.Value)
				if e == nil {
					delay = append(delay, at.Sub(scheduled).Seconds()*1000)
				}
			} else if strings.HasPrefix(sk.Value, "GAP#") {
				s.Gaps++
				if !disruption.IsZero() && !restored.IsZero() {
					if recorded, ok := item["recordedAt"].(*types.AttributeValueMemberS); ok {
						at, e := time.Parse(time.RFC3339Nano, recorded.Value)
						if e == nil && !at.Before(disruption) && !at.After(restored.Add(2*time.Minute)) {
							s.GapsNearDisruption++
						}
					}
				}
				if n, ok := item["missedCount"].(*types.AttributeValueMemberN); ok {
					v, _ := strconv.Atoi(n.Value)
					s.MissedSlots += v
				}
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	s.Delay = measure(delay)
	s.ChecksPerMinute = float64(s.Observations) / s.DurationSeconds * 60
	return nil
}

func machine() map[string]string {
	cpu := "unknown"
	if b, e := os.ReadFile("/proc/cpuinfo"); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "model name") {
				cpu = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				break
			}
		}
	}
	mem := "unknown"
	if b, e := os.ReadFile("/proc/meminfo"); e == nil {
		mem = strings.Fields(string(b))[1] + " kB"
	}
	return map[string]string{
		"cpuModel":     cpu,
		"logicalCores": strconv.Itoa(runtime.NumCPU()),
		"memory":       mem,
		"os":           command(context.Background(), "uname", "-srmo"),
	}
}

func stageRun(
	ctx context.Context,
	binary string,
	p *proxy,
	db *dynamodb.Client,
	client *http.Client,
	dockerID string,
	dockerPID int,
	n int,
	kind string,
	duration time.Duration,
	ordinal int,
) (s stage, err error) {
	s.Name = kind
	s.Monitors = n
	s.DurationSeconds = duration.Seconds()
	table := fmt.Sprintf("statusforge_m6cap_%d_%d", os.Getpid(), ordinal)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, e := db.DeleteTable(cleanup, &dynamodb.DeleteTableInput{TableName: aws.String(table)})
		if e != nil {
			err = errors.Join(err, fmt.Errorf("delete %s: %w", table, e))
		} else {
			e = dynamodb.NewTableNotExistsWaiter(db).Wait(cleanup, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, time.Minute)
			if e != nil {
				err = errors.Join(err, e)
			}
		}
	}()
	env := []string{
		"STATUSFORGE_HTTP_ADDR=" + apiAddr,
		"STATUSFORGE_DYNAMODB_ENDPOINT=http://" + proxyAddr,
		"STATUSFORGE_DYNAMODB_TABLE=" + table,
		"STATUSFORGE_MIN_INTERVAL_SECONDS=10",
		"STATUSFORGE_ALLOWED_TARGETS=" + targetAddr,
		"STATUSFORGE_NOTIFY_URL=http://127.0.0.1:8203/notify",
		fmt.Sprintf("STATUSFORGE_WORKERS=%d", *apiWorkers),
		"STATUSFORGE_LOG_LEVEL=" + *apiLogLevel,
	}
	api, e := runProcess(ctx, binary, env)
	if e != nil {
		return s, e
	}
	defer func() { stop(api) }()
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if e = waitReady(readyCtx, client); e != nil {
		return s, e
	}
	ids, e := create(ctx, client, n)
	if e != nil {
		return s, e
	}
	s.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	start := time.Now()
	finish := start.Add(duration)
	overview, summary := []float64{}, []float64{}
	lastReady := -1
	cutAt := start.Add(duration / 2)
	restoreAt := cutAt.Add(30 * time.Second)
	didCut, didRestore, didRestart, recovered := false, false, false, false
	prevAPI, prevDB := int64(0), int64(0)
	prevAt := time.Now()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for time.Now().Before(finish) {
		select {
		case <-ctx.Done():
			return s, ctx.Err()
		case <-ticker.C:
		}
		now := time.Now()
		if kind == "outage" && !didCut && !now.Before(cutAt) {
			p.outage(true)
			didCut = true
			s.DisruptionAt = now.UTC().Format(time.RFC3339Nano)
		}
		if kind == "outage" && didCut && !didRestore && !now.Before(restoreAt) {
			p.outage(false)
			didRestore = true
			s.RestoredAt = now.UTC().Format(time.RFC3339Nano)
		}
		if kind == "restart" && !didRestart && !now.Before(cutAt) {
			s.DisruptionAt = now.UTC().Format(time.RFC3339Nano)
			stop(api)
			api, e = runProcess(ctx, binary, env)
			if e != nil {
				return s, e
			}
			didRestart = true
			s.RestoredAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		code, _, _ := get(client, "/api/health/ready")
		point := point{At: now.UTC().Format(time.RFC3339Nano), APIReady: code}
		if code != lastReady {
			s.Transitions = append(s.Transitions, point)
			lastReady = code
		}
		if s.RestoredAt != "" && !recovered && code == 200 {
			restored, _ := time.Parse(time.RFC3339Nano, s.RestoredAt)
			recovery := time.Now().UTC()
			s.RecoveredAt = recovery.Format(time.RFC3339Nano)
			s.RecoverySeconds = recovery.Sub(restored).Seconds()
			recovered = true
		}
		// The scheduler state is in-process (no table reads), so it is sampled
		// whether or not the dependency is ready and does not load the database.
		var coverage schedulerCoverage
		if c, _, e := getJSON(client, "/api/system/scheduler", &coverage); e == nil && c == 200 {
			point.Scheduler = &coverage
			if s.SchedulerStates == nil {
				s.SchedulerStates = map[string]int{}
			}
			s.SchedulerStates[coverage.State]++
		}
		if code == 200 {
			if c, d, e := get(client, "/api/overview"); e == nil && c == 200 {
				overview = append(overview, d.Seconds()*1000)
			}
			if c, d, e := get(client, "/api/monitors/"+ids[0]+"/summary?window=24h"); e == nil &&
				c == 200 {
				summary = append(summary, d.Seconds()*1000)
			}
		}
		a, r := proc(api.Process.Pid)
		b, dr := proc(dockerPID)
		secs := now.Sub(prevAt).Seconds()
		if secs > 0 {
			point.APICPUPercent = float64(a-prevAPI) / 100 / secs * 100
			point.DynamoCPUPercent = stats(ctx, dockerID)
			_ = b
		}
		point.APIRSSBytes = r
		point.DynamoRSSBytes = dr
		prevAPI, prevDB, prevAt = a, b, now
		_ = prevDB
		s.Samples = append(s.Samples, point)
	}
	s.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.Overview = measure(overview)
	s.Summary = measure(summary)
	stop(api)
	api = nil
	p.outage(false)
	scanCtx, scanCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer scanCancel()
	e = scan(scanCtx, db, table, start, time.Now(), &s)
	return s, e
}

func main() {
	duration := flag.Duration(
		"duration",
		10*time.Minute,
		"duration per load stage (release evidence requires 10m)",
	)
	levels := flag.String(
		"stages",
		"25,100,250,restart,outage",
		"comma-separated 25,100,250,restart,outage",
	)
	out := flag.String("out", "", "write JSON to file (also prints to stdout)")
	apiLogLevel = flag.String(
		"api-log-level",
		"error",
		"API STATUSFORGE_LOG_LEVEL; info adds per-cycle scheduler tick lines on stderr",
	)
	apiWorkers = flag.Int(
		"workers",
		4,
		"API STATUSFORGE_WORKERS (1–16); release evidence uses the default 4",
	)
	flag.Parse()
	if *apiWorkers < 1 || *apiWorkers > 16 {
		fmt.Fprintln(os.Stderr, "workers must be 1–16")
		os.Exit(2)
	}
	if e := run(*duration, *levels, *out); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func run(duration time.Duration, levels, out string) error {
	if duration < time.Second {
		return errors.New("duration must be positive")
	}
	ctx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	root, e := os.MkdirTemp("", "statusforge-capacity-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	binary := filepath.Join(root, "statusforge")
	target := filepath.Join(root, "sample-target")
	for _, v := range []struct{ dest, src string }{{binary, "./cmd/statusforge"}, {target, "./cmd/sample-target"}} {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", v.dest, v.src)
		cmd.Stderr = os.Stderr
		if e = cmd.Run(); e != nil {
			return e
		}
	}
	p, e := newProxy()
	if e != nil {
		return e
	}
	defer p.close()
	fixture, e := runProcess(ctx, target, []string{"STATUSFORGE_SAMPLE_TARGET_ADDR=" + targetAddr})
	if e != nil {
		return e
	}
	defer stop(fixture)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	local := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local")
	db := local.DynamoDB()
	if _, e = db.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)}); e != nil {
		return e
	}
	dockerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	id := command(dockerCtx, "docker", "compose", "ps", "-q", "dynamodb")
	pid := daemonPID(dockerCtx)
	cancel()
	r := result{
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Machine:   machine(),
		Versions: map[string]string{
			"go":            runtime.Version(),
			"dynamodbLocal": "3.3.1 (compose.yaml)",
			"application":   command(ctx, "git", "describe", "--tags", "--always", "--dirty"),
		},
		Parameters: map[string]any{
			"secondsPerStage": duration.Seconds(),
			"workers":         *apiWorkers,
			"intervalSeconds": 10,
			"api":             apiAddr,
			"fixture":         targetAddr,
			"proxy":           proxyAddr,
			"database":        "127.0.0.1:8000",
			"dynamodbStats":   "docker stats --no-stream CPU; /proc/<docker inspect PID>/status VmRSS",
			"cpuSampling":     "/proc/<PID>/stat ticks, assumed 100 Hz",
		},
	}
	for i, name := range strings.Split(levels, ",") {
		count := 100
		switch name {
		case "25":
			count = 25
		case "100":
			count = 100
		case "250":
			count = 250
		case "restart", "outage":
		default:
			return fmt.Errorf("unknown stage %q", name)
		}
		s, e := stageRun(ctx, binary, p, db, client, id, pid, count, name, duration, i)
		r.Stages = append(r.Stages, s)
		if e != nil {
			return fmt.Errorf("stage %s: %w", name, e)
		}
	}
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	if out != "" {
		if e = os.WriteFile(out, append(raw, '\n'), 0o600); e != nil {
			return e
		}
	}
	fmt.Println(string(raw))
	return nil
}
