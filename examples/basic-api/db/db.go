// Package db holds the service's SQL migrations, embedded in the binary so
// the production image can migrate its own database ("basic-api migrate").
package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the migration files at the root of the FS, the layout
// postgres.Commands and postgrestest.NewMigrated expect.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		// Unreachable: "migrations" is a valid path embedded at build time.
		panic(err)
	}
	return sub
}
