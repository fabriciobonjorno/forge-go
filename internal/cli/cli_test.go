package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fabriciobonjorno/forge-go/config"
	"github.com/fabriciobonjorno/forge-go/mysql/mysqltest"
	"github.com/fabriciobonjorno/forge-go/postgres/postgrestest"
	"github.com/fabriciobonjorno/forge-go/sqlite/sqlitetest"
	"github.com/fabriciobonjorno/forge-go/uuid"
)

func TestRunVersion(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "forge ") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunUUID(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"uuid", "--count", "2"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	lines := strings.Fields(stdout.String())
	if len(lines) != 2 {
		t.Fatalf("UUID count=%d", len(lines))
	}
	for _, line := range lines {
		if _, err := uuid.Parse(line); err != nil {
			t.Fatalf("parse generated UUID: %v", err)
		}
	}
}

func TestRunRejectsUnknownCommandAndLargeCount(t *testing.T) {
	t.Parallel()
	tests := [][]string{{"unknown"}, {"uuid", "--count", "1001"}}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("args=%v unexpectedly succeeded", args)
		}
	}
}

func TestRunNewGeneratesApplicationWithoutDependencies(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "shop")
	var stdout, stderr bytes.Buffer
	// The positional name may come before or after flags.
	if code := Run([]string{"new", "--skip-deps", "--dir", dir, "shop"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, path := range []string{"Dockerfile", ".dockerignore", "compose.yaml", "cmd/shop/main.go"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path))); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	if !strings.Contains(stdout.String(), "docker compose up --build") || !strings.Contains(stdout.String(), "http://127.0.0.1:8080/") {
		t.Fatalf("missing next steps: %s", stdout.String())
	}
}

func TestRunNewRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tests := [][]string{
		{"new"},
		{"new", "Bad_Name", "--skip-deps"},
		{"new", "one", "two"},
		{"new", "app", "--dir", filepath.Join(dir, "app"), "--framework-path", dir},
		{"new", "app2", "--skip-deps", "--database", "oracle"},
		{"new", "app3", "--skip-deps", "--database", "mysql", "--skip-database"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("args=%v unexpectedly succeeded", args)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "app")); !os.IsNotExist(err) {
		t.Fatalf("invalid framework path still generated files: %v", err)
	}
}

func TestResolveCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := resolveCommand(root, nil); err == nil {
		t.Fatal("expected missing go.mod to fail")
	}
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n")
	writeFile(t, filepath.Join(root, "cmd", "api", "main.go"), "package main\n")
	if name, err := resolveCommand(root, nil); err != nil || name != "api" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	writeFile(t, filepath.Join(root, "cmd", "worker", "main.go"), "package main\n")
	if _, err := resolveCommand(root, nil); err == nil {
		t.Fatal("expected ambiguity error with two commands")
	}
	if name, err := resolveCommand(root, []string{"worker"}); err != nil || name != "worker" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	for _, args := range [][]string{{"../escape"}, {"missing"}, {"api", "worker"}} {
		if _, err := resolveCommand(root, args); err == nil {
			t.Fatalf("args=%v unexpectedly resolved", args)
		}
	}
}

// TestGeneratedApplication is the end-to-end check for `forge new`: generated
// projects must compile, vet and pass their own tests against this checkout of
// the framework, and the database workflow must work end to end.
func TestGeneratedApplication(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go toolchain on generated projects")
	}
	t.Parallel()
	frameworkPath, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	generate := func(t *testing.T, name string, extra ...string) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), name)
		args := append([]string{"new", name, "--module", "example.com/acme/" + name, "--dir", dir, "--framework-path", frameworkPath}, extra...)
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "vendor", "modules.txt")); err != nil {
			t.Fatalf("framework was not vendored: %v", err)
		}
		for _, args := range [][]string{{"vet", "./..."}, {"test", "./..."}, {"build", "-o", os.DevNull, "./cmd/" + name}} {
			command := exec.Command("go", args...)
			command.Dir = dir
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("go %v: %v\n%s", args, err, output)
			}
		}
		return dir
	}

	t.Run("without database", func(t *testing.T) {
		t.Parallel()
		dir := generate(t, "edge", "--skip-database")
		if _, err := os.Stat(filepath.Join(dir, "db")); !os.IsNotExist(err) {
			t.Fatalf("db/ generated with --skip-database: %v", err)
		}
	})

	// Each database: generated tests pass, and the migration workflow runs
	// through the application binary against a real server.
	for _, database := range []struct {
		name   string
		config func(testing.TB) config.Database
	}{
		{"postgresql", postgrestest.Config},
		{"mysql", mysqltest.Config},
		{"sqlite", sqlitetest.Config},
	} {
		t.Run("database workflow "+database.name, func(t *testing.T) {
			t.Parallel()
			dir := generate(t, "ledger", "--database", database.name)
			up, down, err := generateMigration(dir, "create_entries", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, up, "CREATE TABLE entries (id CHAR(36) PRIMARY KEY, amount_cents BIGINT NOT NULL);\n")
			writeFile(t, down, "DROP TABLE entries;\n")

			// The database settings come from .env.local, as a developer
			// would configure them.
			cfg := database.config(t)
			writeFile(t, filepath.Join(dir, ".env.local"), "FORGE_DATABASE_URL="+cfg.URL.Reveal()+"\n")
			env, err := environment(dir, os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			run := func(args ...string) string {
				t.Helper()
				var output bytes.Buffer
				if err := runApp(context.Background(), dir, "ledger", args, env, &output, &output); err != nil {
					t.Fatalf("ledger %v: %v\n%s", args, err, output.String())
				}
				return output.String()
			}
			if output := run("migrate"); !strings.Contains(output, "_create_entries") {
				t.Fatalf("migrate output: %s", output)
			}
			if output := run("migrate", "status"); !strings.Contains(output, "applied") {
				t.Fatalf("status output: %s", output)
			}
			if output := run("rollback"); !strings.Contains(output, "reverted") {
				t.Fatalf("rollback output: %s", output)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDevForwardsInterruptToApplication is a regression test: `forge dev`
// used `go run`, which does not forward signals, so stopping forge left the
// application running.
func TestDevForwardsInterruptToApplication(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a program with the go toolchain")
	}
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/sig\n\ngo 1.27.1\n")
	writeFile(t, filepath.Join(root, "cmd", "sig", "main.go"), `package main

import (
	"fmt"
	"os"
	"os/signal"
)

func main() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	fmt.Println("ready")
	<-signals
	fmt.Println("graceful shutdown")
}
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- runApp(ctx, root, "sig", nil, os.Environ(), output, output) }()

	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(output.String(), "ready") {
		if time.Now().After(deadline) {
			t.Fatalf("application never started: %s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dev: %v output=%s", err, output.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("application kept running after cancellation")
	}
	if !strings.Contains(output.String(), "graceful shutdown") {
		t.Fatalf("application was not interrupted gracefully: %s", output.String())
	}
}

type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
