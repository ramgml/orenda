package sqlite

import "embed"

// MigrationsFS holds every SQL migration shipped with the binary.
//
// It is exported so the cmd/orenda package can apply pending migrations at
// server startup; the same FS is also used by tests.
//
// The `all:` prefix is required because some files have multiple dots
// in their name (e.g. `001_init.down.sql`) and Go's default embed
// pattern rejects those as "irregular". The runner filters `.down.sql`
// vs `.sql` itself when picking the up or down path.
//
//go:embed all:migrations/*.sql
var MigrationsFS embed.FS
