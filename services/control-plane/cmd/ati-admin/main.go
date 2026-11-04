// Command ati-admin performs administrative tasks with an owner/administrator database URL:
// role bootstrap, migrations, plan seeding, tenants, staff tokens and their revocation, tenant
// recording policy, the recording purge and the purge of operational records. Tokens are printed
// once and stored only as hashes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/recording"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
)

const usage = `usage: ati-admin <command> [flags]

commands:
  bootstrap-roles     --admin-url URL                 create application roles (idempotent)
  migrate             --url URL                       apply migrations as the owner role
  seed-plans          --url URL                       insert the shipped synthetic demo rubrics and plans
  create-tenant       --url URL --name NAME           print a new tenant id
  create-staff-token  --url URL --tenant ID --role recruiter|reviewer|admin --label L [--ttl 720h]
                                                      print a new staff token (shown once)
  list-staff-tokens   --url URL --tenant ID           list the tenant's staff tokens (never the tokens)
  revoke-staff-token  --url URL --token-id ID         revoke a staff token and its browser sessions (audited)
  tenant-recording    --url URL --tenant ID --allowed=true|false
                                                      set whether a tenant allows consented recordings
  recordings          --url URL purge-expired        delete expired recordings (storage: ATI_RECORDING_S3_*),
                                                      leaving audited tombstones
  purge-operational   --url URL [--older-than 24h]    delete webhook dedupe and idempotency records, and staff
                                                      sessions that ended, older than the window (audited;
                                                      replays hold response bodies)
  validate-evaluations --input FILE                   apply the evaluation validator to JSON lines of
                                                      {answer, rubric, output} (offline: no database)
`

var errUnknownCommand = errors.New("unknown command")

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(context.Background(), os.Args[1], os.Args[2:], os.Stdout, os.Getenv); err != nil {
		if errors.Is(err, errUnknownCommand) || errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stderr, usage)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run executes one command, writing its result (an id, a token shown once, a count) to out.
func run(ctx context.Context, cmd string, args []string, out io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	url := fs.String("url", "", "owner database URL")
	adminURL := fs.String("admin-url", "", "administrator database URL")
	name := fs.String("name", "", "tenant name")
	tenant := fs.String("tenant", "", "tenant id")
	role := fs.String("role", "recruiter", "staff role")
	label := fs.String("label", "", "token label")
	ttl := fs.Duration("ttl", 30*24*time.Hour, "token lifetime")
	allowed := fs.Bool("allowed", false, "tenant-recording: whether the tenant allows consented recordings")
	olderThan := fs.Duration("older-than", 24*time.Hour, "purge-operational: keep records younger than this")
	input := fs.String("input", "", "validate-evaluations: JSON lines of {answer, rubric, output}")
	tokenID := fs.String("token-id", "", "revoke-staff-token: the staff token's id (stf_...)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch cmd {
	case "validate-evaluations":
		if *input == "" {
			return fmt.Errorf("--input is required")
		}
		return validateEvaluations(*input, out)
	case "bootstrap-roles":
		return store.BootstrapRoles(ctx, *adminURL)
	case "migrate":
		return store.Migrate(ctx, *url)
	case "seed-plans":
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		return store.SeedEmbeddedContent(ctx, pool)
	case "create-tenant":
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		id := ids.New(ids.Tenant)
		if _, err := pool.Exec(ctx, `INSERT INTO ati.tenants (id, name) VALUES ($1, $2)`, id, *name); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, id)
		return err
	case "create-staff-token":
		if *tenant == "" || *label == "" || (*role != "recruiter" && *role != "reviewer" && *role != "admin") {
			return fmt.Errorf("--tenant, --label and --role recruiter|reviewer|admin are required")
		}
		if *ttl <= 0 {
			return fmt.Errorf("--ttl must be positive")
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		token := "ati_st_" + ids.Token()
		if _, err := pool.Exec(ctx, `INSERT INTO ati.staff_tokens (id, tenant_id, token_hash, role, label, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			ids.New(ids.Staff), *tenant, ids.Hash(token), *role, *label, time.Now().Add(*ttl)); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, token)
		return err
	case "list-staff-tokens":
		if *tenant == "" {
			return fmt.Errorf("--tenant is required")
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		return listStaffTokens(ctx, pool, *tenant, out)
	case "revoke-staff-token":
		if !regexp.MustCompile(`^stf_[A-Za-z0-9]{16,}$`).MatchString(*tokenID) {
			return fmt.Errorf("--token-id must be a staff token id (stf_...)")
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		sessions, err := revokeStaffToken(ctx, pool, *tokenID)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "revoked %s and %d browser session(s)\n", *tokenID, sessions)
		return err
	case "tenant-recording":
		// Owner connection: the control plane itself cannot change tenant policy.
		if *tenant == "" {
			return fmt.Errorf("--tenant is required")
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		tag, err := pool.Exec(ctx, `UPDATE ati.tenants SET recording_allowed = $2 WHERE id = $1`, *tenant, *allowed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("tenant %s not found", *tenant)
		}
		_, err = pool.Exec(ctx, `INSERT INTO ati.audit_logs (tenant_id, actor_type, actor_id, action, subject_type, subject_id, outcome, details)
			VALUES ($1, 'system', 'ati-admin', 'tenant.recording_policy', 'tenant', $1, 'allowed', jsonb_build_object('recording_allowed', $2::boolean))`, *tenant, *allowed)
		return err
	case "recordings":
		// recordings purge-expired: delete expired recording objects and leave audited tombstones.
		if len(fs.Args()) != 1 || fs.Arg(0) != "purge-expired" {
			return fmt.Errorf("usage: ati-admin recordings --url <control-plane role URL> purge-expired (storage from ATI_RECORDING_S3_*)")
		}
		objects, err := recording.NewS3Store(getenv("ATI_RECORDING_S3_ENDPOINT"), getenv("ATI_RECORDING_S3_BUCKET"),
			getenv("ATI_RECORDING_S3_ACCESS_KEY"), getenv("ATI_RECORDING_S3_SECRET_KEY"), getenv("ATI_RECORDING_S3_USE_SSL") == "true")
		if err != nil {
			return err
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		n, err := recording.PurgeExpired(ctx, pool, objects, time.Now())
		if _, werr := fmt.Fprintf(out, "purged %d recording(s)\n", n); werr != nil && err == nil {
			err = werr
		}
		return err
	case "purge-operational":
		// Webhook dedupe rows matter only while LiveKit may still deliver an event (minutes);
		// idempotency records only while a client may still retry (hours). Replays hold response
		// bodies, so neither is kept indefinitely (ADR-009). The control plane cannot delete them.
		if *olderThan < time.Hour {
			return fmt.Errorf("--older-than must be at least 1h")
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		cutoff := time.Now().Add(-*olderThan).UTC()
		var webhooks, replays, sessions int64
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			t, err := tx.Exec(ctx, `DELETE FROM ati.webhook_events WHERE received_at < $1`, cutoff)
			if err != nil {
				return err
			}
			webhooks = t.RowsAffected()
			if t, err = tx.Exec(ctx, `DELETE FROM ati.idempotency_records WHERE created_at < $1`, cutoff); err != nil {
				return err
			}
			replays = t.RowsAffected()
			// A staff session ends when it expires or is revoked, whichever is first (ADR-023).
			if t, err = tx.Exec(ctx, `DELETE FROM ati.staff_sessions WHERE LEAST(expires_at, revoked_at) < $1`, cutoff); err != nil {
				return err
			}
			sessions = t.RowsAffected()
			_, err = tx.Exec(ctx, `INSERT INTO ati.audit_logs (actor_type, actor_id, action, subject_type, outcome, details)
				VALUES ('system', 'ati-admin', 'retention.purge_operational', 'database', 'allowed',
				jsonb_build_object('webhook_events', $1::bigint, 'idempotency_records', $2::bigint, 'staff_sessions', $3::bigint, 'older_than', $4::text))`,
				webhooks, replays, sessions, olderThan.String())
			return err
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "purged %d webhook event(s), %d idempotency record(s) and %d staff session(s)\n", webhooks, replays, sessions)
		return err
	}
	return fmt.Errorf("%w %q", errUnknownCommand, cmd)
}
