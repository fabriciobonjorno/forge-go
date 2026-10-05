// Package postgres persists agent execution attempts and their outcomes.
package postgres

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations returns the agent journal migrations rooted at ".".
func Migrations() fs.FS {
	root, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	return root
}
