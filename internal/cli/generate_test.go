package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateMigration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, _, err := generateMigration(root, "create_widgets", time.Now()); err == nil {
		t.Fatal("expected error outside an application with db/migrations")
	}
	if err := os.MkdirAll(filepath.Join(root, "db", "migrations"), 0o750); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	up, down, err := generateMigration(root, "create_widgets", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(up) != "20260926150000_create_widgets.up.sql" || filepath.Base(down) != "20260926150000_create_widgets.down.sql" {
		t.Fatalf("up=%s down=%s (version must be the UTC timestamp)", up, down)
	}
	// Same second: the version is bumped instead of colliding.
	second, _, err := generateMigration(root, "add_index", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(second), "20260926150001_") {
		t.Fatalf("second=%s", second)
	}
	for _, name := range []string{"CreateWidgets", "1widgets", "drop-table", "../escape", ""} {
		if _, _, err := generateMigration(root, name, now); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
}

func TestGenerateViewEmbedsTemplatesAndRefusesOverwrite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/shop\n")

	paths, err := generateView(root, "welcome")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != filepath.Join("app", "views", "welcome.gohtml") || paths[1] != filepath.Join("app", "views", "renderer.go") {
		t.Fatalf("generated paths = %v", paths)
	}
	templatePath := filepath.Join(root, filepath.FromSlash(paths[0]))
	templateBody, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(templateBody), "{{.Title}}") || !strings.Contains(string(templateBody), "{{.Content}}") {
		t.Fatalf("generated template missing data fields: %s", templateBody)
	}
	rendererPath := filepath.Join(root, filepath.FromSlash(paths[1]))
	rendererBefore, err := os.ReadFile(rendererPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendererBefore), "//go:embed *.gohtml") {
		t.Fatalf("renderer does not embed templates: %s", rendererBefore)
	}

	paths, err = generateView(root, "dashboard")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join("app", "views", "dashboard.gohtml") {
		t.Fatalf("second view should only add its template, got %v", paths)
	}
	rendererAfter, err := os.ReadFile(rendererPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(rendererAfter) != string(rendererBefore) {
		t.Fatal("second view generation modified the existing renderer")
	}
	if _, err := generateView(root, "welcome"); err == nil {
		t.Fatal("existing view was overwritten")
	}
}

func TestGenerateViewValidatesNamesAndApplicationRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"", "Welcome", "../escape", "x-y", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := generateView(root, name); err == nil {
				t.Fatalf("accepted invalid view name %q", name)
			}
		})
	}
	if _, err := generateView(root, "home"); err == nil {
		t.Fatal("generated a view outside an application")
	}
}

func TestGenerateViewRefusesUnknownRendererWrapper(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/shop\n")
	rendererPath := filepath.Join(root, "app", "views", "renderer.go")
	writeFile(t, rendererPath, "package views\n// application-owned renderer\n")
	if _, err := generateView(root, "welcome"); err == nil {
		t.Fatal("expected renderer collision to be rejected")
	}
	if _, err := os.Stat(filepath.Join(root, "app", "views", "welcome.gohtml")); !os.IsNotExist(err) {
		t.Fatalf("view was created despite renderer collision: %v", err)
	}
}

func TestRunGenerateViewReportsCreatedFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/shop\n")
	var output bytes.Buffer
	if err := runGenerateAt([]string{"view", "landing_page"}, &output, root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"app/views/landing_page.gohtml", "app/views/renderer.go"} {
		if !strings.Contains(output.String(), path) {
			t.Errorf("output does not report %s: %s", path, output.String())
		}
	}
}

func TestRunGenerateDryRunViewDoesNotWriteFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/shop\n")
	var output bytes.Buffer
	if err := runGenerateAt([]string{"view", "welcome", "--dry-run"}, &output, root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"app/views/welcome.gohtml", "app/views/renderer.go"} {
		if !strings.Contains(output.String(), "would create") || !strings.Contains(output.String(), path) {
			t.Errorf("dry-run output missing planned path %s: %s", path, output.String())
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("dry-run created %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "app")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created directories: %v", err)
	}
}

func TestRunGenerateDryRunMigrationDoesNotWriteFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "db", "migrations")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	if err := runGenerateAtTime([]string{"migration", "create_widgets", "--dry-run"}, &output, root, now); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"db/migrations/20261004120000_create_widgets.up.sql",
		"db/migrations/20261004120000_create_widgets.down.sql",
	} {
		if !strings.Contains(output.String(), "would create") || !strings.Contains(output.String(), path) {
			t.Errorf("dry-run output missing planned path %s: %s", path, output.String())
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("dry-run created %s: %v", path, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("dry-run changed migration directory: entries=%v err=%v", entries, err)
	}
}

func TestRunGenerateDryRunRejectsSymlinkedViewDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/shop\n")
	if err := os.Mkdir(filepath.Join(root, "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "app", "views")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var output bytes.Buffer
	if err := runGenerateAt([]string{"view", "welcome", "--dry-run"}, &output, root); err == nil {
		t.Fatal("dry-run accepted a symlinked views directory")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink target was modified: entries=%v err=%v", entries, err)
	}
}

func TestRunGenerateRejectsUnknownDryRunPlacement(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--dry-run", "view", "welcome"},
		{"view", "welcome", "--force"},
		{"view", "welcome", "--dry-run", "extra"},
	} {
		var output bytes.Buffer
		if err := runGenerateAt(args, &output, t.TempDir()); err == nil {
			t.Errorf("accepted args %v", args)
		}
	}
}

func TestApplyGeneratedFilesRollsBackPartialCreate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "db", "migrations", "conflict.down.sql"), "existing\n")
	err := applyGeneratedFiles(root, []string{"db/migrations"}, []generatedFile{
		{path: filepath.Join("db", "migrations", "conflict.up.sql"), content: "up\n"},
		{path: filepath.Join("db", "migrations", "conflict.down.sql"), content: "down\n"},
	})
	if err == nil {
		t.Fatal("expected exclusive create to reject existing down migration")
	}
	if _, err := os.Lstat(filepath.Join(root, "db", "migrations", "conflict.up.sql")); !os.IsNotExist(err) {
		t.Fatalf("partial up migration was not removed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "db", "migrations", "conflict.down.sql"))
	if err != nil || string(content) != "existing\n" {
		t.Fatalf("existing file changed: content=%q err=%v", content, err)
	}
}
