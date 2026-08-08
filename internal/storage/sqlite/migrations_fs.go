package sqlite

import "embed"

// MigrationsFS holds every SQL migration shipped with the binary.
//
// It is exported so the cmd/orenda package can apply pending migrations at
// server startup; the same FS is also used by tests.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
