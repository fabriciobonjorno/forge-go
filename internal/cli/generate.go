package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var migrationName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)

func runGenerate(args []string, stdout io.Writer) error {
	if len(args) != 2 || args[0] != "migration" {
		return errors.New("usage: forge generate migration NAME")
	}
	up, down, err := generateMigration(".", args[1], time.Now())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "  create  %s\n  create  %s\n", up, down)
	return err
}

// generateMigration writes an empty up/down pair under db/migrations. The
// version is the UTC timestamp, bumped past any existing version so two
// migrations generated in the same second stay ordered.
func generateMigration(root, name string, now time.Time) (string, string, error) {
	if !migrationName.MatchString(name) {
		return "", "", errors.New("migration name must be snake_case: lowercase letters, digits and underscores, starting with a letter")
	}
	dir := filepath.Join(root, "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", errors.New("db/migrations not found: run this inside a Forge application with a database")
	}
	versions := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if len(entry.Name()) > 14 {
			versions[entry.Name()[:14]] = true
		}
	}
	stamp := now.UTC()
	for versions[stamp.Format("20060102150405")] {
		stamp = stamp.Add(time.Second)
	}
	base := filepath.Join(dir, stamp.Format("20060102150405")+"_"+name)
	up, down := base+".up.sql", base+".down.sql"
	upBody := fmt.Sprintf("-- %s\n--\n-- Runs inside a transaction. For statements that cannot, such as\n-- CREATE INDEX CONCURRENTLY, make this the first line:\n-- -- forge:no-transaction\n", name)
	downBody := fmt.Sprintf("-- Reverts %s. Delete this file if the migration cannot be reverted.\n", name)
	if err := writeNew(up, upBody); err != nil {
		return "", "", err
	}
	if err := writeNew(down, downBody); err != nil {
		return "", "", errors.Join(err, os.Remove(up))
	}
	return up, down, nil
}

// writeNew creates path and fails if it already exists.
func writeNew(path, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- path built from a validated name under db/migrations
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(content)
	return errors.Join(writeErr, file.Close())
}
