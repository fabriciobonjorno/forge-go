package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/fabriciobonjorno/forge-go/config"
	"github.com/fabriciobonjorno/forge-go/internal/version"
	"github.com/fabriciobonjorno/forge-go/uuid"
)

const maxUUIDCount = 1000

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "version":
		err = runVersion(args[1:], stdout)
	case "uuid":
		err = runUUID(args[1:], stdout, stderr)
	case "doctor":
		err = runDoctor(args[1:], stdout)
	case "new":
		err = runNew(args[1:], stdout, stderr)
	case "dev":
		err = runDev(args[1:], stdout, stderr)
	case "build":
		err = runBuild(args[1:], stdout, stderr)
	case "test":
		err = runTest(args[1:], stdout, stderr)
	case "migrate":
		err = runMigrate(args[1:], stdout, stderr)
	case "rollback":
		err = runRollback(args[1:], stdout, stderr)
	case "generate":
		err = runGenerate(args[1:], stdout)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "forge: %v\n", err)
		return 1
	}
	return 0
}

func runVersion(args []string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("version accepts no arguments")
	}
	_, err := fmt.Fprintf(output, "forge %s (%s, %s)\n", version.Version, version.Commit, version.Date)
	return err
}

func runUUID(args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("uuid", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	count := flags.Int("count", 1, "number of UUIDv7 values")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *count < 1 || *count > maxUUIDCount {
		return fmt.Errorf("count must be between 1 and %d", maxUUIDCount)
	}
	for range *count {
		id, err := uuid.New()
		if err != nil {
			return fmt.Errorf("generate UUIDv7: %w", err)
		}
		if _, err := fmt.Fprintln(output, id); err != nil {
			return err
		}
	}
	return nil
}

func runDoctor(args []string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("doctor accepts no arguments")
	}
	env, err := environment(".", os.Environ())
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithLookup(lookupIn(env))
	if err != nil {
		return fmt.Errorf("configuration invalid: %w", err)
	}
	database := "not configured"
	if !cfg.Database.URL.IsZero() {
		database = describeDatabase(cfg.Database.URL.Reveal())
	}
	_, err = fmt.Fprintf(output, "configuration: ok\nenvironment: %s\nhttp address: %s\ntransport: %s\ndatabase: %s\n",
		cfg.Environment, cfg.HTTP.Address, cfg.HTTP.Transport, database)
	return err
}

// describeDatabase prints only scheme, user, host, database or file, and
// the transport-security parameter (sslmode, tls). Passwords can hide in the
// userinfo and in query parameters (password, sslpassword), so nothing else
// of the URL is echoed.
func describeDatabase(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "configured (unparseable)"
	}
	safe := url.URL{Scheme: parsed.Scheme, Opaque: parsed.Opaque, Host: parsed.Host, Path: parsed.Path}
	if parsed.User != nil {
		safe.User = url.User(parsed.User.Username())
	}
	kept := url.Values{}
	for _, key := range []string{"sslmode", "tls"} {
		if value := parsed.Query().Get(key); value != "" {
			kept.Set(key, value)
		}
	}
	safe.RawQuery = kept.Encode()
	return safe.String()
}

// lookupIn adapts a KEY=VALUE list to an environment lookup; later entries win.
func lookupIn(env []string) func(string) (string, bool) {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func usage(output io.Writer) {
	fmt.Fprint(output, `usage: forge <command> [arguments]

commands:
  new NAME            generate an application with Dockerfile, compose and CI;
                      --database postgresql (default), mysql, mariadb, sqlite or none
  dev                 run the application in development mode
  test [ARGS]         go test with .env.development loaded (default ./...)
  build               compile the application into bin/
  migrate [status]    apply pending migrations, or list them
  rollback [-steps N] revert the latest migrations
  generate migration NAME
                      create db/migrations/<timestamp>_NAME.{up,down}.sql
  doctor              validate the FORGE_* configuration
  uuid [--count N]    generate UUIDv7 identifiers
  version             print the Forge version
`)
}
