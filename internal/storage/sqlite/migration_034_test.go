package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migration 034: project ↔ wiki-page link (wiki:project-wiki-link).
//
// Contracts:
//  1. Up migration adds projects.wiki_slug TEXT NULL with an FK to
//     wiki_pages.slug, ON DELETE SET NULL.
//  2. Existing rows keep wiki_slug = NULL after the migration.
//  3. The new index idx_projects_wiki_slug is created.
//  4. Setting wiki_slug to an existing wiki page slug round-trips.
//  5. Setting wiki_slug to a slug that doesn't exist violates the
//     FK (the storage layer is the only thing that knows about the
//     constraint; handlers do the friendly 422).
//  6. Deleting a wiki page clears wiki_slug on linked projects
//     (ON DELETE SET NULL — the project survives, only the link
//     breaks).
//  7. Down migration drops the column and the index.
func TestMigrate_034ProjectWikiSlug(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "033_task_numbers")

	const (
		ownerID    = "u-034"
		projectID1 = "p-034-1"
		projectID2 = "p-034-2"
		pageSlug   = "roadmap"
	)

	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "u@034.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO projects (id, name, owner_id) VALUES (?, ?, ?)`,
		projectID1, "P1", ownerID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO projects (id, name, owner_id) VALUES (?, ?, ?)`,
		projectID2, "P2", ownerID)
	require.NoError(t, err)

	// Seed a wiki page so we can test the FK + SET NULL chain.
	_, err = db.ExecContext(ctx,
		`INSERT INTO wiki_pages (id, slug, title, content_md, position) VALUES (?, ?, ?, ?, 0)`,
		"w-034-1", pageSlug, "Roadmap", "# Roadmap")
	require.NoError(t, err)

	// Contract 1 + 2: column exists, default NULL on existing rows.
	body, err := MigrationsFS.ReadFile("migrations/034_project_wiki_slug.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	var wikiSlug sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT wiki_slug FROM projects WHERE id = ?`, projectID1).Scan(&wikiSlug))
	assert.False(t, wikiSlug.Valid, "existing row must default wiki_slug to NULL")

	// Contract 3: index created.
	assert.Contains(t, listIndexes(t, ctx, db, "projects"), "idx_projects_wiki_slug",
		"idx_projects_wiki_slug must exist after migration")

	// Contract 4: setting to an existing slug round-trips.
	_, err = db.ExecContext(ctx,
		`UPDATE projects SET wiki_slug = ? WHERE id = ?`, pageSlug, projectID1)
	require.NoError(t, err)
	var linked sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT wiki_slug FROM projects WHERE id = ?`, projectID1).Scan(&linked))
	require.True(t, linked.Valid)
	assert.Equal(t, pageSlug, linked.String)

	// Contract 5: FK rejects unknown slug.
	_, err = db.ExecContext(ctx,
		`UPDATE projects SET wiki_slug = ? WHERE id = ?`, "no-such-page", projectID2)
	require.Error(t, err, "FK must reject unknown slug")

	// Contract 6: deleting the wiki page clears wiki_slug on the
	// linked project (SET NULL — project survives).
	_, err = db.ExecContext(ctx, `DELETE FROM wiki_pages WHERE slug = ?`, pageSlug)
	require.NoError(t, err)
	var afterDel sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT wiki_slug FROM projects WHERE id = ?`, projectID1).Scan(&afterDel))
	assert.False(t, afterDel.Valid, "wiki-page delete must SET NULL the link")

	var stillThere int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM projects WHERE id = ?`, projectID1).Scan(&stillThere))
	assert.Equal(t, 1, stillThere, "project must survive a wiki-page delete")

	// Contract 7: down migration drops the column and the index.
	downBody, err := MigrationsFS.ReadFile("migrations/034_project_wiki_slug.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `SELECT wiki_slug FROM projects LIMIT 1`)
	require.Error(t, err, "projects.wiki_slug must be dropped after down")

	assert.NotContains(t, listIndexes(t, ctx, db, "projects"), "idx_projects_wiki_slug",
		"idx_projects_wiki_slug must be dropped after down")
}
