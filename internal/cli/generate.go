package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var migrationName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)
var viewName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func runGenerate(args []string, stdout io.Writer) error {
	return runGenerateAt(args, stdout, ".")
}

func runGenerateAt(args []string, stdout io.Writer, root string) error {
	return runGenerateAtTime(args, stdout, root, time.Now())
}

func runGenerateAtTime(args []string, stdout io.Writer, root string, now time.Time) error {
	dryRun := len(args) == 3 && args[2] == "--dry-run"
	if (len(args) != 2 && !dryRun) || (len(args) == 3 && !dryRun) {
		return errors.New("usage: forge generate <migration|view> NAME [--dry-run]")
	}
	var files []generatedFile
	var err error
	switch args[0] {
	case "migration":
		files, err = planMigration(root, args[1], now)
	case "view":
		files, err = planView(root, args[1])
	default:
		return errors.New("usage: forge generate <migration|view> NAME [--dry-run]")
	}
	if err != nil {
		return err
	}
	if !dryRun {
		directories := []string{"db/migrations"}
		if args[0] == "view" {
			directories = []string{"app/views"}
		}
		if err := applyGeneratedFiles(root, directories, files); err != nil {
			return err
		}
	}
	for _, file := range files {
		verb := "create"
		if dryRun {
			verb = "would create"
		}
		if _, err := fmt.Fprintf(stdout, "  %s  %s\n", verb, file.path); err != nil {
			return err
		}
	}
	return nil
}

type generatedFile struct {
	path    string
	content string
}

const generatedViewRenderer = `package views

import (
	"embed"

	forge "github.com/fabriciobonjorno/forge-go"
)

//go:embed *.gohtml
var templates embed.FS

// NewRenderer returns the application's embedded HTML templates.
func NewRenderer() (*forge.ViewRenderer, error) {
	return forge.NewViewRenderer(templates)
}
`

const generatedView = `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>{{.Title}}</title>
</head>
<body>
	<main>
		<h1>{{.Title}}</h1>
		<p>{{.Content}}</p>
	</main>
</body>
</html>
`

// generateView adds an auto-escaped Go HTML template and a production-safe
// embed wrapper to an existing Forge application. Existing files are never
// overwritten.
func generateView(root, name string) ([]string, error) {
	files, err := planView(root, name)
	if err != nil {
		return nil, err
	}
	if err := applyGeneratedFiles(root, []string{"app/views"}, files); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.path)
	}
	return paths, nil
}

func planView(root, name string) ([]generatedFile, error) {
	if !viewName.MatchString(name) {
		return nil, errors.New("view name must be lowercase snake_case (letters, digits and underscores)")
	}
	projectRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("go.mod not found: run this inside a Forge application")
	}
	defer func() { _ = projectRoot.Close() }()
	if _, err := projectRoot.Stat("go.mod"); err != nil {
		return nil, errors.New("go.mod not found: run this inside a Forge application")
	}
	if err := rejectExistingSymlinkPaths(projectRoot, []string{filepath.Join("app", "views", name+".gohtml"), filepath.Join("app", "views", "renderer.go")}); err != nil {
		return nil, err
	}
	viewPath := filepath.Join("app", "views", name+".gohtml")
	rendererPath := filepath.Join("app", "views", "renderer.go")
	if _, err := projectRoot.Lstat(viewPath); err == nil {
		return nil, fmt.Errorf("refusing to overwrite %s", viewPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	rendererExists := false
	if _, err := projectRoot.Lstat(rendererPath); err == nil {
		renderer, err := projectRoot.ReadFile(rendererPath)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(renderer), "//go:embed *.gohtml") || !strings.Contains(string(renderer), "func NewRenderer()") {
			return nil, fmt.Errorf("refusing to modify existing %s; expected the Forge-generated template embed wrapper", rendererPath)
		}
		rendererExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	files := []generatedFile{{path: viewPath, content: generatedView}}
	if rendererExists {
		return files, nil
	}
	return append(files, generatedFile{path: rendererPath, content: generatedViewRenderer}), nil
}

// generateMigration writes an empty up/down pair under db/migrations. The
// version is the UTC timestamp, bumped past any existing version so two
// migrations generated in the same second stay ordered.
func generateMigration(root, name string, now time.Time) (string, string, error) {
	files, err := planMigration(root, name, now)
	if err != nil {
		return "", "", err
	}
	if err := applyGeneratedFiles(root, []string{"db/migrations"}, files); err != nil {
		return "", "", err
	}
	return filepath.Join(root, files[0].path), filepath.Join(root, files[1].path), nil
}

func planMigration(root, name string, now time.Time) ([]generatedFile, error) {
	if !migrationName.MatchString(name) {
		return nil, errors.New("migration name must be snake_case: lowercase letters, digits and underscores, starting with a letter")
	}
	projectRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("db/migrations not found: run this inside a Forge application with a database")
	}
	defer func() { _ = projectRoot.Close() }()
	dir := filepath.Join("db", "migrations")
	if err := rejectExistingSymlinkPaths(projectRoot, []string{dir}); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(projectRoot.FS(), dir)
	if err != nil {
		return nil, errors.New("db/migrations not found: run this inside a Forge application with a database")
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
	return []generatedFile{{path: up, content: upBody}, {path: down, content: downBody}}, nil
}

func applyGeneratedFiles(rootPath string, directories []string, files []generatedFile) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, directory := range directories {
		if err := ensureDirectoryTree(root, directory); err != nil {
			return err
		}
	}
	created := make([]string, 0, len(files))
	rollback := func(cause error) error {
		for i := len(created) - 1; i >= 0; i-- {
			cause = errors.Join(cause, root.Remove(created[i]))
		}
		return cause
	}
	for _, planned := range files {
		file, err := root.OpenFile(planned.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return rollback(err)
		}
		created = append(created, planned.path)
		_, writeErr := file.WriteString(planned.content)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func ensureDirectoryTree(root *os.Root, directory string) error {
	current := ""
	for _, part := range strings.Split(filepath.Clean(directory), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(current, 0o750); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing to write through non-directory path %s", current)
		}
	}
	return nil
}

func rejectExistingSymlinkPaths(root *os.Root, paths []string) error {
	seen := make(map[string]struct{})
	for _, path := range paths {
		for current := filepath.Clean(path); current != "." && current != string(filepath.Separator); current = filepath.Dir(current) {
			if _, ok := seen[current]; ok {
				continue
			}
			seen[current] = struct{}{}
			info, err := root.Lstat(current)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to inspect through symlink %s", current)
			}
		}
	}
	return nil
}
