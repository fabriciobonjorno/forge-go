package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	t.Parallel()
	vars, keys, err := parseEnvFile(`
# comment
FORGE_ENV=development
export QUOTED="value with spaces"
SINGLE='x'
URL=postgres://u:p@h/db?sslmode=disable#fragment
EMPTY=
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"FORGE_ENV": "development",
		"QUOTED":    "value with spaces",
		"SINGLE":    "x",
		"URL":       "postgres://u:p@h/db?sslmode=disable#fragment",
		"EMPTY":     "",
	}
	for key, value := range want {
		if vars[key] != value {
			t.Errorf("%s=%q want %q", key, vars[key], value)
		}
	}
	if !slices.Equal(keys, []string{"FORGE_ENV", "QUOTED", "SINGLE", "URL", "EMPTY"}) {
		t.Errorf("keys=%v", keys)
	}
	for _, bad := range []string{"no equals sign", "1KEY=x", "BAD-KEY=x", "=value"} {
		_, _, err := parseEnvFile("OK=1\n" + bad + "\n")
		if err == nil {
			t.Errorf("%q accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), bad) {
			t.Errorf("error must name the line without echoing it: %v", err)
		}
	}
}

func TestEnvironmentPrecedence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".env.development"), "A=development\nB=development\nC=development\n")
	writeFile(t, filepath.Join(root, ".env.local"), "B=local\nC=local\n")
	env, err := environment(root, []string{"C=process", "PATH=/bin"})
	if err != nil {
		t.Fatal(err)
	}
	lookup := lookupIn(env)
	for key, want := range map[string]string{"A": "development", "B": "local", "C": "process", "PATH": "/bin"} {
		if got, _ := lookup(key); got != want {
			t.Errorf("%s=%q want %q", key, got, want)
		}
	}
	if strings.Count(strings.Join(env, "\n"), "C=") != 1 {
		t.Errorf("process variable duplicated: %v", env)
	}

	empty, err := environment(t.TempDir(), []string{"X=1"})
	if err != nil || !slices.Equal(empty, []string{"X=1"}) {
		t.Fatalf("missing files must be ignored: %v %v", empty, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("broken line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := environment(root, nil); err == nil || !strings.Contains(err.Error(), ".env.local") {
		t.Fatalf("expected error naming the file, got %v", err)
	}
}

func TestDescribeDatabaseHidesPasswords(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"postgres://app:hunter2@db:5432/app?password=hunter3&sslpassword=hunter4&sslmode=require": "postgres://app@db:5432/app?sslmode=require",
		"mysql://app:hunter2@db:3306/app?tls=true":                                                "mysql://app@db:3306/app?tls=true",
		"sqlite:storage/development.db":                                                           "sqlite:storage/development.db",
		"sqlite:///app/storage/production.db":                                                     "sqlite:///app/storage/production.db",
	} {
		if got := describeDatabase(raw); got != want || strings.Contains(got, "hunter") {
			t.Errorf("describeDatabase(%q) = %q, want %q", raw, got, want)
		}
	}
}
