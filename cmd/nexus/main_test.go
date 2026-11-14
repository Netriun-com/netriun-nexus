// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netriun/nexus/internal/app"
	"github.com/netriun/nexus/internal/jobs"
	syncer "github.com/netriun/nexus/internal/sync"
	"github.com/netriun/nexus/internal/worker"
)

func TestConfig(t *testing.T) {
	for _, name := range []string{"NEXUS_ROLE", "NEXUS_WORKERS", "NEXUS_ACCOUNT_CONCURRENCY", "NEXUS_PROVIDER_CONCURRENCY", "NEXUS_SHUTDOWN_TIMEOUT"} {
		t.Setenv(name, "")
	}
	c, err := loadConfig()
	if err != nil || c.role != "all" || c.worker.Concurrency != 8 || c.accountLimit != 2 || c.providerLimit != 4 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for _, role := range []string{"serve", "worker", "all"} {
		t.Setenv("NEXUS_ROLE", role)
		if c, err := loadConfig(); err != nil || c.role != role {
			t.Fatalf("role: %+v %v", c, err)
		}
	}
	for _, tc := range []struct{ name, value string }{
		{"NEXUS_ROLE", "invalid"}, {"NEXUS_WORKERS", "0"}, {"NEXUS_ACCOUNT_CONCURRENCY", "-1"},
		{"NEXUS_PROVIDER_CONCURRENCY", "bad"}, {"NEXUS_SHUTDOWN_TIMEOUT", "0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if _, err := loadConfig(); err == nil {
				t.Fatal("accepted invalid setting")
			}
		})
	}
}

type testPayload struct {
	Marker       string
	FirstDelayMS int
	WaitFile     string
	FailFirst    bool
	PanicFirst   bool
}

// The child runs the real role startup/shutdown path with a test-only registry.
// There is no production environment variable or route to install test handlers.
func TestNexusProcess(t *testing.T) {
	if os.Getenv("NEXUS_TEST_PROCESS") != "1" {
		return
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.worker.Lease = 900 * time.Millisecond
	cfg.worker.PollInterval = 20 * time.Millisecond
	handlers := worker.Registry{"test": func(ctx context.Context, j jobs.Job) error {
		var p testPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return err
		}
		f, err := os.OpenFile(p.Marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(f, j.Attempts)
		f.Close()
		if err != nil {
			return err
		}
		defer func() {
			if ctx.Err() != nil {
				_ = os.WriteFile(p.Marker+".cancelled", nil, 0600)
			}
		}()
		if j.Attempts == 1 {
			if p.PanicFirst {
				panic("test panic with sensitive text")
			}
			if p.FailFirst {
				return errors.New("test failure")
			}
			timer := time.NewTimer(time.Duration(p.FirstDelayMS) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		for p.WaitFile != "" {
			if _, err := os.Stat(p.WaitFile); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		return nil
	}}
	if os.Getenv("NEXUS_TEST_EMPTY_REGISTRY") == "1" {
		handlers = worker.Registry{}
	}
	if os.Getenv("NEXUS_TEST_SCHEDULER") == "1" {
		cfg.sync.Tick, cfg.sync.Reconcile = 20*time.Millisecond, 40*time.Millisecond
		db, err := pgxpool.New(t.Context(), os.Getenv("DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		store := syncer.New(db, cfg.sync)
		handlers = worker.Registry{syncer.JobKind: store.Handler(func(ctx context.Context, _ syncer.Payload, _ syncer.Account) (bool, error) { return false, ctx.Err() })}
	}
	if os.Getenv("NEXUS_TEST_PRODUCTION_REGISTRY") == "1" {
		handlers = nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := runService(ctx, cfg, handlers); err != nil {
		t.Fatal(err)
	}
}

type process struct {
	cmd     *exec.Cmd
	done    chan error
	address string
	log     string
}

func startProcess(t *testing.T, dsn, role, shutdown string, extra ...string) *process {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &process{cmd: exec.Command(executable, "-test.run=^TestNexusProcess$", "-test.v"), done: make(chan error, 1), address: address, log: filepath.Join(t.TempDir(), "child.log")}
	log, err := os.Create(p.log)
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stdout, p.cmd.Stderr = log, log
	p.cmd.Env = append(os.Environ(),
		"NEXUS_TEST_PROCESS=1", "NEXUS_ROLE="+role, "HTTP_ADDR="+address, "DATABASE_URL="+dsn,
		"NEXUS_WORKERS=8", "NEXUS_ACCOUNT_CONCURRENCY=2", "NEXUS_PROVIDER_CONCURRENCY=3", "NEXUS_SHUTDOWN_TIMEOUT="+shutdown,
		"REDIS_URL="+os.Getenv("TEST_REDIS_URL"), "ENCRYPTION_KEY="+base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"ADMIN_USERNAME=testadmin", "ADMIN_EMAIL=admin@example.com", "ADMIN_PASSWORD=Testing-password1!", "DEPLOYMENT_MODE=self_hosted",
		"APP_ORIGIN=http://"+address, "COOKIE_SECURE=false", "TRUSTED_PROXY_CIDRS=",
		"SMTP_HOST=smtp.example.com", "SMTP_PORT=587", "SMTP_USERNAME=test", "SMTP_PASSWORD=testing-only", "SMTP_FROM_ADDRESS=test@example.com", "SMTP_FROM_NAME=Test")
	p.cmd.Env = append(p.cmd.Env, extra...)
	if err := p.cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait(); close(p.done); log.Close() }()
	t.Cleanup(func() {
		p.cmd.Process.Kill()
		<-p.done
		if t.Failed() {
			data, _ := os.ReadFile(p.log)
			t.Logf("child %s: %s", role, data)
		}
	})
	path := "/readyz"
	if role == "worker" {
		path = "/healthz"
	}
	eventually(t, func() bool { return status(p.address, path) == 200 })
	return p
}

func status(address, path string) int {
	client := http.Client{Timeout: 300 * time.Millisecond}
	r, err := client.Get("http://" + address + path)
	if err != nil {
		return 0
	}
	r.Body.Close()
	return r.StatusCode
}

func (p *process) stop(t *testing.T, sig os.Signal) {
	t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.done:
		if sig != os.Kill && err != nil {
			t.Fatalf("child exit: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("process did not stop")
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func database(t *testing.T) (*pgxpool.Pool, *jobs.Queue, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("TEST_REDIS_URL") == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_REDIS_URL for process integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Database != "ccmp_test" {
		t.Fatal("use isolated ccmp_test database")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("worker_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := pgxpool.New(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := app.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	q, err := jobs.New(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return db, q, u.String()
}

func addJob(t *testing.T, q *jobs.Queue, payload testPayload, key string) *jobs.Job {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	j, err := q.Enqueue(t.Context(), jobs.Params{Kind: "test", Payload: raw, ConcurrencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func jobState(t *testing.T, db *pgxpool.Pool, id int64) (string, int, string) {
	t.Helper()
	var state, message string
	var attempts int
	if err := db.QueryRow(t.Context(), "SELECT status,attempts,last_error FROM jobs WHERE id=$1", id).Scan(&state, &attempts, &message); err != nil {
		t.Fatal(err)
	}
	return state, attempts, message
}

func waitJob(t *testing.T, db *pgxpool.Pool, id int64, want string) {
	t.Helper()
	eventually(t, func() bool { state, _, _ := jobState(t, db, id); return state == want })
}

func waitMarker(t *testing.T, path string) {
	t.Helper()
	eventually(t, func() bool { data, _ := os.ReadFile(path); return len(data) > 0 })
}

func TestRoleProcesses(t *testing.T) {
	for _, role := range []string{"all", "separate"} {
		t.Run(role, func(t *testing.T) {
			db, q, dsn := database(t)
			marker := filepath.Join(t.TempDir(), "runs")
			var w *process
			if role == "separate" {
				serve := startProcess(t, dsn, "serve", "1s")
				defer serve.stop(t, syscall.SIGTERM)
				j := addJob(t, q, testPayload{Marker: marker}, "")
				time.Sleep(150 * time.Millisecond)
				if state, n, _ := jobState(t, db, j.ID); state != "queued" || n != 0 {
					t.Fatal("serve claimed work")
				}
				w = startProcess(t, dsn, "worker", "1s", "REDIS_URL=", "ENCRYPTION_KEY=", "SMTP_HOST=")
				if status(w.address, "/readyz") != 404 || status(w.address, "/api/v1/instances") != 404 {
					t.Fatal("worker exposed app routes")
				}
				waitJob(t, db, j.ID, "succeeded")
			} else {
				w = startProcess(t, dsn, "all", "1s")
				j := addJob(t, q, testPayload{Marker: marker, FirstDelayMS: 2200}, "")
				waitJob(t, db, j.ID, "succeeded")
				if _, n, _ := jobState(t, db, j.ID); n != 1 {
					t.Fatal("lease was not renewed for long handler")
				}
			}
			w.stop(t, syscall.SIGTERM)
		})
	}
}

func TestWorkerRecoveryAndShutdown(t *testing.T) {
	for _, scenario := range []string{"killed", "drained", "deadline", "lease-lost"} {
		t.Run(scenario, func(t *testing.T) {
			db, q, dsn := database(t)
			shutdown, delay := "1s", 5000
			if scenario == "drained" {
				delay = 400
			}
			if scenario == "deadline" {
				shutdown = "150ms"
			}
			w := startProcess(t, dsn, "worker", shutdown, "NEXUS_WORKERS=1", "NEXUS_ACCOUNT_CONCURRENCY=1")
			marker := filepath.Join(t.TempDir(), "runs")
			j := addJob(t, q, testPayload{Marker: marker, FirstDelayMS: delay}, "account:1")
			waitMarker(t, marker)
			next := addJob(t, q, testPayload{Marker: filepath.Join(t.TempDir(), "next")}, "account:1")
			sig := os.Signal(syscall.SIGTERM)
			if scenario == "killed" {
				sig = os.Kill
			}
			if scenario == "lease-lost" {
				if _, err := db.Exec(t.Context(), "UPDATE jobs SET locked_by='another-owner' WHERE id=$1", j.ID); err != nil {
					t.Fatal(err)
				}
				eventually(t, func() bool { _, err := os.Stat(marker + ".cancelled"); return err == nil })
			}
			w.stop(t, sig)
			if state, n, _ := jobState(t, db, next.ID); state != "queued" || n != 0 {
				t.Fatal("claimed after shutdown signal")
			}
			if scenario == "drained" {
				if state, n, _ := jobState(t, db, j.ID); state != "succeeded" || n != 1 {
					t.Fatalf("not drained: %s %d", state, n)
				}
			} else {
				if state, n, _ := jobState(t, db, j.ID); state != "running" || n != 1 {
					t.Fatalf("unfinished lease not preserved: %s %d", state, n)
				}
			}
			replacement := startProcess(t, dsn, "worker", "1s")
			waitJob(t, db, j.ID, "succeeded")
			waitJob(t, db, next.ID, "succeeded")
			if scenario != "drained" {
				if _, n, _ := jobState(t, db, j.ID); n != 2 {
					t.Fatalf("expected retry, got %d attempts", n)
				}
				data, _ := os.ReadFile(marker)
				if string(data) != "1\n2\n" {
					t.Fatalf("handler execution evidence: %q", data)
				}
			}
			replacement.stop(t, syscall.SIGTERM)
		})
	}
}

func TestWorkerDispatchAndLimits(t *testing.T) {
	db, q, dsn := database(t)
	w := startProcess(t, dsn, "worker", "1s")
	release := filepath.Join(t.TempDir(), "release")
	var ids []int64
	for _, key := range []string{"account:1", "account:2", "provider:aws"} {
		for range 4 {
			ids = append(ids, addJob(t, q, testPayload{Marker: filepath.Join(t.TempDir(), "runs"), WaitFile: release}, key).ID)
		}
	}
	unknown, err := q.Enqueue(t.Context(), jobs.Params{Kind: "future", Priority: 100})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		counts := map[string]int{}
		rows, err := db.Query(t.Context(), "SELECT concurrency_key,count(*) FROM jobs WHERE status='running' GROUP BY concurrency_key")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var n int
			if err := rows.Scan(&key, &n); err != nil {
				t.Fatal(err)
			}
			counts[key] = n
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if counts["account:1"] > 2 || counts["account:2"] > 2 || counts["provider:aws"] > 3 {
			t.Fatalf("limits exceeded: %v", counts)
		}
		return counts["account:1"] == 2 && counts["account:2"] == 2 && counts["provider:aws"] == 3
	})
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		waitJob(t, db, id, "succeeded")
	}
	// Empty concurrency keys still respect the process-wide handler pool.
	poolRelease := filepath.Join(t.TempDir(), "release")
	var poolIDs []int64
	for range 10 {
		poolIDs = append(poolIDs, addJob(t, q, testPayload{Marker: filepath.Join(t.TempDir(), "runs"), WaitFile: poolRelease}, "").ID)
	}
	eventually(t, func() bool {
		var n int
		if err := db.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE status='running'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 8 {
			t.Fatalf("process concurrency exceeded: %d", n)
		}
		return n == 8
	})
	if err := os.WriteFile(poolRelease, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range poolIDs {
		waitJob(t, db, id, "succeeded")
	}
	if state, n, _ := jobState(t, db, unknown.ID); state != "queued" || n != 0 {
		t.Fatal("unknown kind consumed")
	}
	for _, panics := range []bool{false, true} {
		j := addJob(t, q, testPayload{Marker: filepath.Join(t.TempDir(), "runs"), FailFirst: !panics, PanicFirst: panics}, "")
		waitJob(t, db, j.ID, "succeeded")
		_, n, message := jobState(t, db, j.ID)
		if n != 2 || message == "" || strings.Contains(message, "sensitive") {
			t.Fatalf("retry diagnostic: %d %q", n, message)
		}
	}
	w.stop(t, syscall.SIGTERM)
	empty := startProcess(t, dsn, "worker", "1s", "NEXUS_TEST_EMPTY_REGISTRY=1")
	j := addJob(t, q, testPayload{Marker: filepath.Join(t.TempDir(), "unused")}, "")
	time.Sleep(150 * time.Millisecond)
	if state, n, _ := jobState(t, db, j.ID); state != "queued" || n != 0 {
		t.Fatal("empty registry consumed work")
	}
	empty.stop(t, syscall.SIGTERM)
}

func TestScheduledRoleProcesses(t *testing.T) {
	for _, role := range []string{"serve", "worker", "all"} {
		t.Run(role, func(t *testing.T) {
			db, _, dsn := database(t)
			p := startProcess(t, dsn, role, "1s", "NEXUS_TEST_SCHEDULER=1")
			var workspace, account int64
			if err := db.QueryRow(t.Context(), "INSERT INTO workspaces(name,slug) VALUES('Scheduled test','scheduled-test') RETURNING id").Scan(&workspace); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(t.Context(), `INSERT INTO cloud_accounts(workspace_id,name,provider,credentials,regions) VALUES($1,'Scheduled','aws','test-placeholder',ARRAY['us-east-1']) RETURNING id`, workspace).Scan(&account); err != nil {
				t.Fatal(err)
			}
			if role == "serve" {
				time.Sleep(200 * time.Millisecond)
				var n int
				if err := db.QueryRow(t.Context(), "SELECT count(*) FROM sync_state").Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatal("serve ran scheduler")
				}
				p.stop(t, syscall.SIGTERM)
				return
			}
			eventually(t, func() bool {
				var n int
				if err := db.QueryRow(t.Context(), "SELECT count(*) FROM sync_state WHERE account_id=$1 AND last_success_at IS NOT NULL", account).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n == 1
			})
			p.stop(t, syscall.SIGTERM)
			if _, err := db.Exec(t.Context(), "UPDATE sync_state SET next_due_at=now()-interval '1 second' WHERE account_id=$1", account); err != nil {
				t.Fatal(err)
			}
			replacement := startProcess(t, dsn, role, "1s", "NEXUS_TEST_SCHEDULER=1")
			eventually(t, func() bool {
				var n int
				if err := db.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE status='succeeded' AND payload->>'account_id'=$1", fmt.Sprint(account)).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n == 2
			})
			replacement.stop(t, syscall.SIGTERM)
		})
	}
}

func TestWorkerWithScheduledCollectors(t *testing.T) {
	_, _, dsn := database(t) // empty account set: no live provider requests
	p := startProcess(t, dsn, "worker", "1s", "NEXUS_TEST_PRODUCTION_REGISTRY=1", "SMTP_HOST=")
	if status(p.address, "/healthz") != 200 {
		t.Fatal("production collector startup failed")
	}
	p.stop(t, syscall.SIGTERM)
}
