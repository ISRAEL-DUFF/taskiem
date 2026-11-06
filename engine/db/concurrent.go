package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing/fstest"
)

// Concurrent index migrations (CONTRIBUTING.md, Migrations) need two things
// a migration otherwise must not have.
//
//   - No lock timeout. CREATE INDEX CONCURRENTLY waits for every
//     transaction older than it, on any table, and that wait is a lock wait.
//     It holds no lock a query waits for (SHARE UPDATE EXCLUSIVE conflicts
//     with no reads or writes), so waiting delays only the migration; with
//     the timeout a report running for longer failed it.
//   - A retry that recovers. A failed build leaves an invalid index of the
//     same name, so running the migration again failed with "already
//     exists" until someone dropped it by hand. Each build in a pending
//     migration is therefore preceded by DROP INDEX CONCURRENTLY IF EXISTS
//     of its name: the migration has not been applied, so nothing relies
//     on an index of that name yet.
//
// A migration counts as one only when it is NO TRANSACTION and its Up
// section holds nothing but concurrent index builds and drops; anything
// else runs as written, with the lock timeout.

// concurrentBuild is a concurrent index build: its name and table.
var concurrentBuild = regexp.MustCompile(`(?is)^CREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s+ON\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)\b`)

// concurrentOther is what else such a migration may hold.
var concurrentOther = regexp.MustCompile(`(?is)^(DROP\s+INDEX\s+CONCURRENTLY\s|SET\s+lock_timeout\b|RESET\s+lock_timeout\b)`)

// indexBuild is one concurrent build of a migration.
type indexBuild struct{ name, table string }

// concurrentMigration reports whether src is a concurrent index migration
// and, if so, returns it with each build preceded by a drop of its name,
// and the builds.
func concurrentMigration(src string) (string, []indexBuild, bool) {
	lines := strings.Split(src, "\n")
	noTx, up, down := false, -1, len(lines)
	for i, l := range lines {
		switch strings.ToLower(strings.Join(strings.Fields(l), " ")) {
		case "-- +goose no transaction":
			noTx = true
		case "-- +goose up":
			up = i
		case "-- +goose down":
			if up >= 0 && down == len(lines) {
				down = i
			}
		}
	}
	if !noTx || up < 0 {
		return "", nil, false
	}
	var body []string
	for _, l := range lines[up+1 : down] {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "--") {
			body = append(body, l)
		}
	}
	var stmts []string
	var builds []indexBuild
	for _, s := range strings.Split(strings.Join(body, "\n"), ";") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if m := concurrentBuild.FindStringSubmatch(s); m != nil {
			b := indexBuild{name: strings.ToLower(m[1]), table: strings.ToLower(m[2])}
			builds = append(builds, b)
			stmts = append(stmts, "DROP INDEX CONCURRENTLY IF EXISTS "+b.name+";")
		} else if !concurrentOther.MatchString(s) {
			return "", nil, false
		}
		stmts = append(stmts, s+";")
	}
	if len(builds) == 0 {
		return "", nil, false
	}
	out := append(append(append([]string{}, lines[:up+1]...), stmts...), lines[down:]...)
	return strings.Join(out, "\n"), builds, true
}

// concurrentMigrations reads every migration in fsys and returns a copy of
// fsys with the concurrent index migrations rewritten, and their builds by
// path.
func concurrentMigrations(fsys fs.FS) (fs.FS, map[string][]indexBuild, error) {
	paths, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, nil, err
	}
	out := fstest.MapFS{}
	builds := map[string][]indexBuild{}
	for _, p := range paths {
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, nil, err
		}
		if src, bs, ok := concurrentMigration(string(b)); ok {
			b, builds[p] = []byte(src), bs
		}
		out[p] = &fstest.MapFile{Data: b, Mode: 0o444}
	}
	return out, builds, nil
}

// checkIndexNames refuses a build whose name another table's index already
// has: the drop before it would remove that index.
func checkIndexNames(ctx context.Context, sqlDB *sql.DB, path string, builds []indexBuild) error {
	for _, b := range builds {
		var table string
		err := sqlDB.QueryRowContext(ctx, `SELECT t.relname FROM pg_index i
			JOIN pg_class x ON x.oid = i.indexrelid JOIN pg_class t ON t.oid = i.indrelid
			WHERE x.relname = $1 AND x.relnamespace = current_schema()::regnamespace`, b.name).Scan(&table)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if table != b.table {
			return fmt.Errorf("%s builds index %s on %s, but %s already has an index of that name", path, b.name, b.table, table)
		}
	}
	return nil
}
