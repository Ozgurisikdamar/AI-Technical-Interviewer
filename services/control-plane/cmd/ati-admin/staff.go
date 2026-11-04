package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// listStaffTokens prints the tenant's staff tokens: id, role, label, expiry and revocation. The
// tokens themselves exist nowhere to print; this is how an administrator finds the id to revoke.
func listStaffTokens(ctx context.Context, pool *pgxpool.Pool, tenant string, out io.Writer) error {
	rows, err := pool.Query(ctx, `SELECT id, role, label, expires_at, revoked_at FROM ati.staff_tokens WHERE tenant_id = $1 ORDER BY created_at, id`, tenant)
	if err != nil {
		return err
	}
	defer rows.Close()
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tROLE\tLABEL\tEXPIRES\tREVOKED")
	for rows.Next() {
		var id, role, label string
		var expires time.Time
		var revoked *time.Time
		if err := rows.Scan(&id, &role, &label, &expires, &revoked); err != nil {
			return err
		}
		r := "-"
		if revoked != nil {
			r = revoked.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, role, label, expires.UTC().Format(time.RFC3339), r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return w.Flush()
}

// revokeStaffToken revokes a staff token at once, and the browser sessions made from it, in one
// audited transaction. Revoking a revoked token changes nothing and is not an error.
func revokeStaffToken(ctx context.Context, pool *pgxpool.Pool, id string) (int64, error) {
	var sessions int64
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var tenant string
		var revoked *time.Time
		err := tx.QueryRow(ctx, `SELECT tenant_id, revoked_at FROM ati.staff_tokens WHERE id = $1 FOR UPDATE`, id).Scan(&tenant, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("staff token %s not found", id)
		}
		if err != nil {
			return err
		}
		if revoked == nil {
			if _, err := tx.Exec(ctx, `UPDATE ati.staff_tokens SET revoked_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
		}
		t, err := tx.Exec(ctx, `UPDATE ati.staff_sessions SET revoked_at = now() WHERE staff_token_id = $1 AND revoked_at IS NULL`, id)
		if err != nil {
			return err
		}
		sessions = t.RowsAffected()
		_, err = tx.Exec(ctx, `INSERT INTO ati.audit_logs (tenant_id, actor_type, actor_id, action, subject_type, subject_id, outcome, details)
			VALUES ($1, 'system', 'ati-admin', 'staff_token.revoke', 'staff_token', $2, 'allowed',
			jsonb_build_object('already_revoked', $3::boolean, 'staff_sessions', $4::bigint))`, tenant, id, revoked != nil, sessions)
		return err
	})
	return sessions, err
}
