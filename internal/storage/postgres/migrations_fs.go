package postgres

import "embed"

// MigrationsFS holds every SQL migration shipped with the binary for the
// postgres dialect.
//
// The `all:` prefix is required because the file names contain multiple
// dots (`001_baseline.up.sql`) and Go's default embed pattern rejects
// those as "irregular". The runner filters `.down.sql` vs up files
// itself when picking the up or down path.
//
//go:embed all:migrations/*.sql
var MigrationsFS embed.FS
