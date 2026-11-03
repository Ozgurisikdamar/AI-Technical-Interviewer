// Command ati-admin performs administrative tasks with an owner/administrator database URL:
// role bootstrap, migrations, plan seeding, tenants, staff tokens, tenant recording policy and the
// recording purge. Tokens are printed once
// and stored only as hashes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/recording"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
)

const usage = `usage: ati-admin <command> [flags]

commands:
  bootstrap-roles     --admin-url URL                 create application roles (idempotent)
  migrate             --url URL                       apply migrations as the owner role
  seed-plans          --url URL                       insert the shipped synthetic demo plans
  create-tenant       --url URL --name NAME           print a new tenant id
  create-staff-token  --url URL --tenant ID --role recruiter|admin --label L [--ttl 720h]
                                                      print a new staff token (shown once)
  tenant-recording    --url URL --tenant ID --allowed=true|false
                                                      set whether a tenant allows consented recordings
  recordings          --url URL purge-expired        delete expired recordings (storage: ATI_RECORDING_S3_*),
                                                      leaving audited tombstones
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	url := fs.String("url", "", "owner database URL")
	adminURL := fs.String("admin-url", "", "administrator database URL")
	name := fs.String("name", "", "tenant name")
	tenant := fs.String("tenant", "", "tenant id")
	role := fs.String("role", "recruiter", "staff role")
	label := fs.String("label", "", "token label")
	ttl := fs.Duration("ttl", 30*24*time.Hour, "token lifetime")
	allowed := fs.Bool("allowed", false, "tenant-recording: whether the tenant allows consented recordings")
	_ = fs.Parse(args)
	ctx := context.Background()
	switch cmd {
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
		return store.SeedEmbeddedPlans(ctx, pool)
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
		fmt.Println(id)
		return nil
	case "create-staff-token":
		if *tenant == "" || *label == "" || (*role != "recruiter" && *role != "admin") {
			return fmt.Errorf("--tenant, --label and --role recruiter|admin are required")
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
		fmt.Println(token)
		return nil
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
		objects, err := recording.NewS3Store(os.Getenv("ATI_RECORDING_S3_ENDPOINT"), os.Getenv("ATI_RECORDING_S3_BUCKET"),
			os.Getenv("ATI_RECORDING_S3_ACCESS_KEY"), os.Getenv("ATI_RECORDING_S3_SECRET_KEY"), os.Getenv("ATI_RECORDING_S3_USE_SSL") == "true")
		if err != nil {
			return err
		}
		pool, err := store.Open(ctx, *url, 2)
		if err != nil {
			return err
		}
		defer pool.Close()
		n, err := recording.PurgeExpired(ctx, pool, objects, time.Now())
		fmt.Printf("purged %d recording(s)\n", n)
		return err
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}
