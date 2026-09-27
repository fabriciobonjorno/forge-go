// Package scaffold generates new Forge applications. Every generated
// application is containerized by default: it ships a production Dockerfile,
// a .dockerignore, a compose stack and a CI workflow that builds the image.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// FrameworkModule is the module path generated applications depend on.
const FrameworkModule = "github.com/fabriciobonjorno/forge-go"

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	modulePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*(/[A-Za-z0-9._~-]+)*$`)
)

// Options describes the application to generate.
type Options struct {
	// Name is the application and binary name: lowercase letters, digits and hyphens.
	Name string
	// Module is the Go module path; it defaults to Name.
	Module string
	// Dir is the target directory; it defaults to Name and must be absent or empty.
	Dir string
	// Database is one of Databases (aliases such as "postgres" and
	// "sqlite3" are accepted) or empty for none. It selects the adapter,
	// the compose and CI services and the development settings.
	Database string
}

// templateData is what templates see.
type templateData struct {
	Options
	// DB is nil without a database.
	DB *databaseProfile
	// DBUser and DBName are identifiers derived from Name.
	DBUser string
	DBName string
	// DBHostPort is where compose publishes a database server on the host.
	DBHostPort int
	// AppDatabaseURL is used inside compose, DevDatabaseURL on the host and
	// TestDatabaseURL by tests on the host (empty when tests need none).
	AppDatabaseURL  string
	DevDatabaseURL  string
	TestDatabaseURL string
}

type file struct {
	template string
	path     func(name string) string
	// databaseOnly files are generated only with Options.Database.
	databaseOnly bool
}

func fixed(path string) func(string) string { return func(string) string { return path } }

var files = []file{
	{"go.mod.tmpl", fixed("go.mod"), false},
	{"main.go.tmpl", func(name string) string { return filepath.Join("cmd", name, "main.go") }, false},
	{"bootstrap.go.tmpl", fixed(filepath.Join("app", "bootstrap", "bootstrap.go")), false},
	{"bootstrap_test.go.tmpl", fixed(filepath.Join("app", "bootstrap", "bootstrap_test.go")), false},
	{"Dockerfile.tmpl", fixed("Dockerfile"), false},
	{"dockerignore.tmpl", fixed(".dockerignore"), false},
	{"compose.yaml.tmpl", fixed("compose.yaml"), false},
	{"gitignore.tmpl", fixed(".gitignore"), false},
	{"ci.yml.tmpl", fixed(filepath.Join(".github", "workflows", "ci.yml")), false},
	{"dependabot.yml.tmpl", fixed(filepath.Join(".github", "dependabot.yml")), false},
	{"README.md.tmpl", fixed("README.md"), false},
	{"env.development.tmpl", fixed(".env.development"), false},
	{"db.go.tmpl", fixed(filepath.Join("db", "db.go")), true},
	{"keep.tmpl", fixed(filepath.Join("db", "migrations", ".keep")), true},
}

// Normalize validates options and fills defaults.
func Normalize(opts Options) (Options, error) {
	if !namePattern.MatchString(opts.Name) {
		return Options{}, errors.New("application name must start with a lowercase letter and contain only lowercase letters, digits and hyphens (max 63)")
	}
	if opts.Module == "" {
		opts.Module = opts.Name
	}
	if !modulePattern.MatchString(opts.Module) || strings.Contains(opts.Module, "..") {
		return Options{}, fmt.Errorf("invalid Go module path %q", opts.Module)
	}
	if opts.Dir == "" {
		opts.Dir = opts.Name
	}
	database, err := normalizeDatabase(opts.Database)
	if err != nil {
		return Options{}, err
	}
	opts.Database = database
	return opts, nil
}

// Generate renders every file and writes it under opts.Dir. It never
// overwrites: an existing non-empty target directory is rejected, and a
// directory created by Generate is removed again if writing fails.
func Generate(opts Options) ([]string, error) {
	opts, err := Normalize(opts)
	if err != nil {
		return nil, err
	}
	rendered, err := render(opts)
	if err != nil {
		return nil, err
	}

	created, err := prepareDir(opts.Dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for _, f := range selectedFiles(opts) {
		relative := f.path(opts.Name)
		target := filepath.Join(opts.Dir, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return nil, cleanup(opts.Dir, created, err)
		}
		if err := os.WriteFile(target, rendered[relative], 0o600); err != nil {
			return nil, cleanup(opts.Dir, created, err)
		}
		paths = append(paths, relative)
	}
	return paths, nil
}

func render(opts Options) (map[string][]byte, error) {
	templates, err := template.New("").Option("missingkey=error").ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	data := templateData{Options: opts, DBHostPort: dbHostPort(opts.Name)}
	data.DBUser, data.DBName = databaseIdentifiers(opts.Name)
	if data.DB = profileFor(opts.Database, data.DBUser, data.DBName); data.DB != nil {
		data.AppDatabaseURL, data.DevDatabaseURL, data.TestDatabaseURL = databaseURLs(data.DB, data.DBUser, data.DBName, data.DBHostPort)
	}
	output := make(map[string][]byte, len(files))
	for _, f := range selectedFiles(opts) {
		var buffer bytes.Buffer
		if err := templates.ExecuteTemplate(&buffer, f.template, data); err != nil {
			return nil, fmt.Errorf("render %s: %w", f.template, err)
		}
		content := buffer.Bytes()
		path := f.path(opts.Name)
		if strings.HasSuffix(path, ".go") {
			if content, err = format.Source(content); err != nil {
				return nil, fmt.Errorf("format %s: %w", path, err)
			}
		}
		output[path] = content
	}
	return output, nil
}

func selectedFiles(opts Options) []file {
	selected := make([]file, 0, len(files))
	for _, f := range files {
		if !f.databaseOnly || opts.Database != "" {
			selected = append(selected, f)
		}
	}
	return selected
}

func prepareDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, err
	case len(entries) > 0:
		return false, fmt.Errorf("target directory %s is not empty", dir)
	default:
		return false, nil
	}
}

func cleanup(dir string, created bool, cause error) error {
	if created {
		if err := os.RemoveAll(dir); err != nil {
			return errors.Join(cause, fmt.Errorf("remove partially generated %s: %w", dir, err))
		}
	}
	return cause
}
