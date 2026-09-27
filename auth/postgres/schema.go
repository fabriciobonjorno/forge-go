// Package postgres provides PostgreSQL persistence for Forge authentication
// and identity. It is an adapter over the auth core package.
package postgres

import (
	"embed"
	"io/fs"
)

// migrations contains the opinionated Forge identity/session schema.
//
//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the PostgreSQL identity migrations rooted at "." so they
// can be passed directly to postgrestest.NewMigrated or loaded by migrate.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}
