// Package testdb provides isolated, migrated PostgreSQL databases for tests.
//
// It needs an administrator URL for a disposable cluster: ATI_TEST_PG_ADMIN_URL, or the URL written
// by tools/dev/pg.sh. Each test gets its own database cloned from a migrated template. Tests connect
// as the real least-privilege application role, so privilege mistakes fail tests.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
)

// DB is an isolated database for one test.
type DB struct {
	Name  string
	CP    *pgxpool.Pool // ati_control_plane: what the service uses
	Owner *pgxpool.Pool // owner/superuser: setup and assertions only
	admin string
}

// URLFor returns a connection URL for a role in this test database.
func (d *DB) URLFor(role string) string { return withUserDB(d.admin, role, d.Name) }

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

func adminURL(t testing.TB) string {
	if v := os.Getenv("ATI_TEST_PG_ADMIN_URL"); v != "" {
		return v
	}
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", ".cache", "pg", "admin_url")
	if b, err := os.ReadFile(p); err == nil {
		return strings.TrimSpace(string(b))
	}
	if os.Getenv("CI") != "" {
		t.Fatal("ATI_TEST_PG_ADMIN_URL is required in CI")
	}
	t.Skip("no test PostgreSQL: run tools/dev/pg.sh start (or set ATI_TEST_PG_ADMIN_URL)")
	return ""
}

func withUserDB(raw, user, db string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	u.User = url.User(user)
	u.Path = "/" + db
	return u.String()
}

func ensureTemplate(ctx context.Context, admin string) (string, error) {
	templateOnce.Do(func() {
		templateName = "ati_tpl_" + store.MigrationsDigest()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			templateErr = err
			return
		}
		defer conn.Close(ctx)
		// Packages run tests in parallel processes; serialize template creation cluster-wide.
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7302001)`); err != nil {
			templateErr = err
			return
		}
		defer conn.Exec(ctx, `SELECT pg_advisory_unlock(7302001)`) //nolint:errcheck
		if err := store.BootstrapRoles(ctx, admin); err != nil {
			templateErr = fmt.Errorf("bootstrap roles: %w", err)
			return
		}
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, templateName).Scan(&exists); err != nil {
			templateErr = err
			return
		}
		if exists {
			return
		}
		building := templateName + "_build"
		_, _ = conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{building}.Sanitize()+` WITH (FORCE)`)
		if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{building}.Sanitize()); err != nil {
			templateErr = err
			return
		}
		ownerURL := withUserDB(admin, "postgres", building)
		if err := store.Migrate(ctx, ownerURL); err != nil {
			templateErr = fmt.Errorf("migrate template: %w", err)
			return
		}
		owner, err := store.Open(ctx, ownerURL, 2)
		if err != nil {
			templateErr = err
			return
		}
		err = store.SeedEmbeddedPlans(ctx, owner)
		owner.Close()
		if err != nil {
			templateErr = fmt.Errorf("seed plans: %w", err)
			return
		}
		if _, err := conn.Exec(ctx, `ALTER DATABASE `+pgx.Identifier{building}.Sanitize()+` RENAME TO `+pgx.Identifier{templateName}.Sanitize()); err != nil {
			templateErr = err
		}
	})
	return templateName, templateErr
}

// New creates an isolated, migrated and seeded database for the test.
func New(t testing.TB) *DB {
	t.Helper()
	admin := adminURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tpl, err := ensureTemplate(ctx, admin)
	if err != nil {
		t.Fatalf("test template: %v", err)
	}
	name := "ati_t_" + strings.ToLower(ids.New("x")[2:14])
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()+` TEMPLATE `+pgx.Identifier{tpl}.Sanitize()); err != nil {
		conn.Close(ctx)
		t.Fatalf("create test database: %v", err)
	}
	conn.Close(ctx)
	d := &DB{Name: name, admin: admin}
	if d.CP, err = store.Open(ctx, d.URLFor("ati_control_plane"), 20); err != nil {
		t.Fatal(err)
	}
	if d.Owner, err = store.Open(ctx, d.URLFor("postgres"), 5); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.CP.Close()
		d.Owner.Close()
		c, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
			c.Close(context.Background())
		}
	})
	return d
}
