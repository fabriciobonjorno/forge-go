package migrate_test

import (
	"testing"
	"testing/fstest"

	"github.com/fabriciobonjorno/forge-go/migrate"
)

func TestLoadMigrations(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"20260102000000_add_index.up.sql":      {Data: []byte("-- forge:no-transaction\nCREATE INDEX CONCURRENTLY i ON t (x);")},
		"20260101000000_create_t.up.sql":       {Data: []byte("CREATE TABLE t (x int);")},
		"20260101000000_create_t.down.sql":     {Data: []byte("DROP TABLE t;")},
		"README.md":                            {Data: []byte("ignored")},
		".keep":                                {Data: nil},
		"nested/20260103000000_ignored.up.sql": {Data: []byte("SELECT 1;")},
	}
	migrations, err := migrate.LoadMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Name != "create_t" || migrations[1].Name != "add_index" {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
	if migrations[0].Up.NoTx || !migrations[1].Up.NoTx || migrations[1].Down.SQL != "" {
		t.Fatalf("directives/down parsed wrong: %+v", migrations)
	}
	if len(migrations[0].Checksum) != 64 || migrations[0].Checksum == migrations[1].Checksum {
		t.Fatalf("checksums: %q %q", migrations[0].Checksum, migrations[1].Checksum)
	}
}

func TestLoadMigrationsRejectsMistakes(t *testing.T) {
	t.Parallel()
	cases := map[string]fstest.MapFS{
		"bad name":        {"2026_create.up.sql": {Data: []byte("SELECT 1;")}},
		"uppercase name":  {"20260101000000_Create.up.sql": {Data: []byte("SELECT 1;")}},
		"empty file":      {"20260101000000_empty.up.sql": {Data: []byte("  \n")}},
		"comments only":   {"20260101000000_todo.up.sql": {Data: []byte("-- describe the change\n\n-- forge:no-transaction\n")}},
		"down only":       {"20260101000000_x.down.sql": {Data: []byte("SELECT 1;")}},
		"version reused":  {"20260101000000_a.up.sql": {Data: []byte("SELECT 1;")}, "20260101000000_b.up.sql": {Data: []byte("SELECT 1;")}},
		"wrong direction": {"20260101000000_a.sideways.sql": {Data: []byte("SELECT 1;")}},
	}
	for name, fsys := range cases {
		if _, err := migrate.LoadMigrations(fsys); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
