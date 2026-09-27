package cli

import (
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
