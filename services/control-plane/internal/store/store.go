// Package store owns database connectivity, migrations and least-privilege checks.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts/schema"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed bootstrap_roles.sql
var bootstrapRoles string

// Open creates a connection pool.
func Open(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "ati-control-plane"
	cfg.ConnConfig.RuntimeParams["search_path"] = "ati"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	return pool, nil
}

// BootstrapRoles creates the application roles if they do not exist (administrator connection).
func BootstrapRoles(ctx context.Context, adminURL string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, bootstrapRoles)
	return err
}

// MigrationsDigest identifies the migration set (used for test template databases).
func MigrationsDigest() string {
	entries, _ := migrations.ReadDir("migrations")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, _ := migrations.ReadFile("migrations/" + n)
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Migrate applies all migrations using the migrating (owner) role.
func Migrate(ctx context.Context, migratorURL string) error {
	db, err := sql.Open("pgx", migratorURL)
	if err != nil {
		return err
	}
	defer db.Close()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// VerifyLeastPrivilege fails if the application connects as a superuser or owns application
// tables (ADR-020). Enforced in production; reported elsewhere.
func VerifyLeastPrivilege(ctx context.Context, pool *pgxpool.Pool) error {
	var super bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		return err
	}
	if super {
		return errors.New("database role is a superuser; the control plane must use a least-privilege role")
	}
	var owned int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'ati' AND tableowner = current_user`).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		return fmt.Errorf("database role owns %d application tables; migrations must run as a separate owner role", owned)
	}
	return nil
}

// SeedPlan inserts a published plan and its questions (owner connection). Idempotent per (plan, version).
func SeedPlan(ctx context.Context, owner *pgxpool.Pool, raw []byte) error {
	if err := schema.ValidateJSON(contracts.InterviewPlanPath, raw); err != nil {
		return fmt.Errorf("plan does not match the plan contract: %w", err)
	}
	var plan struct {
		PlanID        string `json:"plan_id"`
		Version       int    `json:"version"`
		Status        string `json:"status"`
		Language      string `json:"language"`
		SeniorityBand string `json:"seniority_band"`
		Synthetic     bool   `json:"synthetic"`
		Items         []struct {
			ItemID      string `json:"item_id"`
			Text        string `json:"text"`
			Modality    string `json:"modality"`
			TurnProfile string `json:"turn_profile"`
			RubricRef   *struct {
				RubricID string `json:"rubric_id"`
				Version  int    `json:"version"`
			} `json:"rubric_ref"`
			FollowUps []struct {
				ItemID string `json:"item_id"`
				Text   string `json:"text"`
			} `json:"follow_ups"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return err
	}
	if plan.Status != "published" {
		return fmt.Errorf("only published plans can be seeded (got %s)", plan.Status)
	}
	sum := sha256.Sum256(raw)
	tx, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ati.interview_plans WHERE plan_id=$1 AND version=$2)`, plan.PlanID, plan.Version).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ati.interview_plans (plan_id, version, status, language, seniority_band, synthetic, content, content_sha256)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, plan.PlanID, plan.Version, plan.Status, plan.Language, plan.SeniorityBand, plan.Synthetic, raw, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	ordinal := 0
	for _, it := range plan.Items {
		ordinal++
		var rid *string
		var rver *int
		if it.RubricRef != nil {
			rid, rver = &it.RubricRef.RubricID, &it.RubricRef.Version
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ati.interview_questions (plan_id, plan_version, item_id, parent_item_id, ordinal, kind, text, modality, turn_profile, rubric_id, rubric_version)
			VALUES ($1,$2,$3,NULL,$4,'question',$5,$6,$7,$8,$9)`, plan.PlanID, plan.Version, it.ItemID, ordinal, it.Text, it.Modality, it.TurnProfile, rid, rver); err != nil {
			return err
		}
		for _, f := range it.FollowUps {
			ordinal++
			if _, err := tx.Exec(ctx, `INSERT INTO ati.interview_questions (plan_id, plan_version, item_id, parent_item_id, ordinal, kind, text, modality, turn_profile, rubric_id, rubric_version)
				VALUES ($1,$2,$3,$4,$5,'follow_up',$6,$7,$8,$9,$10)`, plan.PlanID, plan.Version, f.ItemID, it.ItemID, ordinal, f.Text, it.Modality, it.TurnProfile, rid, rver); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// SeedEmbeddedPlans seeds every plan shipped in contracts/content/plans.
func SeedEmbeddedPlans(ctx context.Context, owner *pgxpool.Pool) error {
	files, err := contracts.ListDir(contracts.PlansDir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := SeedPlan(ctx, owner, contracts.MustRead(f)); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
	}
	return nil
}
