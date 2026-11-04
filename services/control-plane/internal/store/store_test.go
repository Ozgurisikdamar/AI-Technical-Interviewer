package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/ids"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/store"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/testdb"
)

// fixture inserts a tenant, candidate, session, activation and segment through the owner role.
type fixture struct {
	tenant, session, activation, segment string
}

func seed(t *testing.T, owner *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	f := fixture{tenant: ids.New(ids.Tenant), session: ids.New(ids.Session), activation: ids.New(ids.Activation), segment: ids.New(ids.Segment)}
	cand := ids.New(ids.Candidate)
	mustExec(t, owner, `INSERT INTO ati.tenants (id, name) VALUES ($1, 'Test tenant')`, f.tenant)
	mustExec(t, owner, `INSERT INTO ati.candidates (id, tenant_id, external_ref) VALUES ($1, $2, 'ref-1')`, cand, f.tenant)
	mustExec(t, owner, `INSERT INTO ati.interview_sessions (id, tenant_id, candidate_id, plan_id, plan_version, notice_id, notice_version, notice_language,
		state, input_mode, timeline_epoch, room_name, retention, recording_policy, created_by)
		VALUES ($1,$2,$3,'plan_demo_backend_en',1,'notice_default','2026-11-draft.1','en','QUESTION_ACTIVE','voice',now(),$4,'{}','{}','test')`,
		f.session, f.tenant, cand, ids.New(ids.Room))
	mustExec(t, owner, `INSERT INTO ati.question_activations (id, session_id, item_id, kind, attempt, opened_ms) VALUES ($1,$2,'q1','question',1,0)`, f.activation, f.session)
	mustExec(t, owner, `INSERT INTO ati.transcript_segments (id, session_id, sequence, segment_key, content_sha256, speaker, text, start_ms, end_ms, source,
		activation_id, item_id, evidence_eligible, flags, producer, fencing_token, engine)
		VALUES ($1,$2,1,'key-00000001','x','candidate','MVCC keeps row versions',100,900,'synthetic_test',$3,'q1',true,'{}','tier1_agent',1,'scripted')`,
		f.segment, f.session, f.activation)
	_ = ctx
	return f
}

func mustExec(t *testing.T, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", strings.Fields(sql)[0], err)
	}
}

func expectDenied(t *testing.T, p *pgxpool.Pool, label, sql string, args ...any) {
	t.Helper()
	_, err := p.Exec(context.Background(), sql, args...)
	if err == nil {
		t.Errorf("%s: unexpectedly allowed", label)
		return
	}
	if !strings.Contains(err.Error(), "permission denied") && !strings.Contains(err.Error(), "append-only") && !strings.Contains(err.Error(), "SQLSTATE 42501") {
		t.Errorf("%s: want a privilege error, got %v", label, err)
	}
}

func TestControlPlaneRoleCannotRewriteEvidenceOrAudit(t *testing.T) {
	db := testdb.New(t)
	f := seed(t, db.Owner)
	mustExec(t, db.Owner, `INSERT INTO ati.audit_logs (actor_type, action, subject_type, outcome) VALUES ('system','x','session','allowed')`)
	for _, c := range []struct{ label, sql string }{
		{"update segment text", `UPDATE ati.transcript_segments SET text = 'changed' WHERE id = '` + f.segment + `'`},
		{"delete segment", `DELETE FROM ati.transcript_segments WHERE id = '` + f.segment + `'`},
		{"update audit", `UPDATE ati.audit_logs SET outcome = 'denied'`},
		{"delete audit", `DELETE FROM ati.audit_logs`},
		{"delete session", `DELETE FROM ati.interview_sessions WHERE id = '` + f.session + `'`},
		{"modify plan", `UPDATE ati.interview_plans SET synthetic = false`},
		{"insert plan", `INSERT INTO ati.interview_plans (plan_id, version, status, language, seniority_band, synthetic, content, content_sha256) VALUES ('plan_evil',1,'published','en','mid',true,'{}','` + strings.Repeat("a", 64) + `')`},
		{"truncate events", `TRUNCATE ati.session_events`},
		{"create table", `CREATE TABLE ati.backdoor (x int)`},
		{"read other schema objects via public", `CREATE TABLE public.backdoor (x int)`},
	} {
		expectDenied(t, db.CP, c.label, c.sql)
	}
}

func TestAppendOnlyHoldsEvenForOwner(t *testing.T) {
	db := testdb.New(t)
	f := seed(t, db.Owner)
	expectDenied(t, db.Owner, "owner updates segment", `UPDATE ati.transcript_segments SET text = 'x' WHERE id = $1`, f.segment)
	expectDenied(t, db.Owner, "owner deletes segment", `DELETE FROM ati.transcript_segments WHERE id = $1`, f.segment)
	// CASCADE: Phase 2 tables reference answers, and the trigger must refuse even a cascading truncate.
	expectDenied(t, db.Owner, "owner truncates answers", `TRUNCATE ati.candidate_answers CASCADE`)
	expectDenied(t, db.Owner, "owner rewrites a rubric", `UPDATE ati.rubrics SET synthetic = false`)
	expectDenied(t, db.Owner, "owner deletes a rubric", `DELETE FROM ati.rubrics`)
	expectDenied(t, db.Owner, "owner truncates review outcomes", `TRUNCATE ati.review_outcomes CASCADE`)
	expectDenied(t, db.Owner, "owner truncates reports", `TRUNCATE ati.reports`)
}

func TestTier2EvaluatorRoleReadsEvidenceButNeverWritesLiveState(t *testing.T) {
	db := testdb.New(t)
	f := seed(t, db.Owner)
	ctx := context.Background()
	conn, err := db.Owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE ati_tier2_evaluator`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ati.transcript_segments WHERE session_id = $1`, f.session).Scan(&n); err != nil || n != 1 {
		t.Fatalf("evaluator must read evidence: n=%d err=%v", n, err)
	}
	for _, sql := range []string{
		`UPDATE ati.interview_sessions SET state = 'PAUSED'`,
		`INSERT INTO ati.session_events (session_id, state_version, transition_id, event, from_state, to_state, actor) VALUES ('` + f.session + `', 99, 'T12', 'pause', 'QUESTION_ACTIVE', 'PAUSED', 'tier2_pipeline')`,
		`UPDATE ati.question_activations SET closed_at = now(), closed_ms = 1, close_reason = 'paused'`,
		`SELECT * FROM ati.candidates`,
		`SELECT * FROM ati.staff_tokens`,
		`SELECT * FROM ati.candidate_sessions`,
		`SELECT * FROM ati.agent_leases`,
	} {
		if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("evaluator: %q should be denied, got %v", sql, err)
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`); err != nil {
			t.Fatal(err)
		}
	}
	var canLogin bool
	if err := db.Owner.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname = 'ati_tier2_evaluator'`).Scan(&canLogin); err != nil || !canLogin {
		t.Fatalf("the evaluator logs in from Phase 2 (ADR-022): canLogin=%v err=%v", canLogin, err)
	}
}

// Phase 2 (ADR-022): the evaluator reads evidence, rubrics and two narrow views, and nothing it
// could use to identify the candidate or to write a result; results reach the database only
// through the control plane.
func TestTier2EvaluatorRoleReadsOnlyEvidenceAndNarrowViews(t *testing.T) {
	db := testdb.New(t)
	f := seed(t, db.Owner)
	ctx := context.Background()
	conn, err := db.Owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE ati_tier2_evaluator`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`SELECT count(*) FROM ati.rubrics`,
		`SELECT state, plan_id, language FROM ati.tier2_session_v WHERE session_id = '` + f.session + `'`,
		`SELECT count(*) FROM ati.tier2_evaluation_keys_v`,
	} {
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Errorf("evaluator must be able to run %q: %v", sql, err)
		}
	}
	for _, sql := range []string{
		`SELECT * FROM ati.interview_sessions`,
		`SELECT * FROM ati.evaluation_attempts`,
		`SELECT * FROM ati.evaluations`,
		`SELECT * FROM ati.review_tasks`,
		`SELECT * FROM ati.review_outcomes`,
		`SELECT * FROM ati.reports`,
		`SELECT * FROM ati.consent_records`,
		`SELECT * FROM ati.audio_recordings`,
		`INSERT INTO ati.evaluations (id) VALUES ('evl_x')`,
		`INSERT INTO ati.review_tasks (id) VALUES ('rvt_x')`,
		`UPDATE ati.outbox_events SET published_at = now()`,
	} {
		if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("evaluator: %q should be denied, got %v", sql, err)
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`); err != nil {
			t.Fatal(err)
		}
	}
}

// The control plane writes evaluation records once: attempts, evaluations, outcomes and reports
// are insert-only for it; only a review task's status changes; outbox rows change only to record
// their publication.
func TestControlPlaneWritesEvaluationRecordsOnce(t *testing.T) {
	db := testdb.New(t)
	seed(t, db.Owner)
	for _, c := range []struct{ label, sql string }{
		{"update attempt", `UPDATE ati.evaluation_attempts SET outcome = 'valid'`},
		{"delete attempt", `DELETE FROM ati.evaluation_attempts`},
		{"update evaluation", `UPDATE ati.evaluations SET weighted_score_centi = 10000`},
		{"delete evaluation", `DELETE FROM ati.evaluations`},
		{"update outcome", `UPDATE ati.review_outcomes SET rationale = 'changed rationale'`},
		{"update report", `UPDATE ati.reports SET body = '{}'`},
		{"insert rubric", `INSERT INTO ati.rubrics (rubric_id, version, sha256, synthetic, body) VALUES ('rub_evil', 1, '` + strings.Repeat("a", 64) + `', true, '{}')`},
		{"rewrite outbox payload", `UPDATE ati.outbox_events SET payload = '{}'`},
		{"delete review task", `DELETE FROM ati.review_tasks`},
	} {
		expectDenied(t, db.CP, c.label, c.sql)
	}
	mustExec(t, db.CP, `UPDATE ati.review_tasks SET status = 'open' WHERE false`)
	mustExec(t, db.CP, `UPDATE ati.outbox_events SET published_at = now() WHERE false`)
}

func TestTier1ContextRoleHasNoTableAccess(t *testing.T) {
	db := testdb.New(t)
	seed(t, db.Owner)
	ctx := context.Background()
	tier1, err := store.Open(ctx, db.URLFor("ati_tier1_ctx"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer tier1.Close()
	for _, table := range []string{"interview_sessions", "transcript_segments", "interview_questions", "candidate_answers", "candidates", "consent_records"} {
		if _, err := tier1.Exec(ctx, `SELECT 1 FROM ati.`+table+` LIMIT 1`); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("tier1_ctx role reads %s: %v", table, err)
		}
	}
}

func TestDatabaseEnforcesSingleOpenActivationAndNoRestingTransientState(t *testing.T) {
	db := testdb.New(t)
	f := seed(t, db.Owner)
	_, err := db.CP.Exec(context.Background(), `INSERT INTO ati.question_activations (id, session_id, item_id, kind, attempt, opened_ms) VALUES ($1,$2,'q2','question',1,5)`, ids.New(ids.Activation), f.session)
	if err == nil || !strings.Contains(err.Error(), "question_activations_one_open") {
		t.Fatalf("INV-1: a second open activation must be rejected, got %v", err)
	}
	for _, s := range []string{"ANSWER_PROCESSING", "FOLLOW_UP"} {
		if _, err := db.CP.Exec(context.Background(), `UPDATE ati.interview_sessions SET state = $1 WHERE id = $2`, s, f.session); err == nil {
			t.Errorf("INV-9: storing transient state %s must be rejected", s)
		}
	}
	if _, err := db.CP.Exec(context.Background(), `UPDATE ati.question_activations SET closed_at = now(), closed_ms = 10, close_reason = 'finalized' WHERE id = $1`, f.activation); err != nil {
		t.Fatalf("closing an activation: %v", err)
	}
	if _, err := db.CP.Exec(context.Background(), `UPDATE ati.question_activations SET closed_at = NULL, closed_ms = NULL, close_reason = NULL WHERE id = $1`, f.activation); err == nil {
		t.Fatal("a closed activation must not be reopened")
	}
}

func TestLeastPrivilegeCheck(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	if err := store.VerifyLeastPrivilege(ctx, db.CP); err != nil {
		t.Fatalf("application role should pass: %v", err)
	}
	if err := store.VerifyLeastPrivilege(ctx, db.Owner); err == nil {
		t.Fatal("superuser connection must be refused")
	}
	// A role that is not a superuser but owns application tables (for example the migration role
	// reused by the service) is refused too.
	role := pgx.Identifier{"ati_lp_probe_" + strings.TrimPrefix(db.Name, "ati_t_")}.Sanitize()
	for _, stmt := range []string{
		`CREATE ROLE ` + role + ` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE`,
		`CREATE TABLE ati.lp_probe (id int)`,
		`ALTER TABLE ati.lp_probe OWNER TO ` + role,
	} {
		if _, err := db.Owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Owner.Exec(context.Background(), `DROP TABLE IF EXISTS ati.lp_probe`)
		_, _ = db.Owner.Exec(context.Background(), `DROP ROLE IF EXISTS `+role)
	})
	probe, err := store.Open(ctx, db.URLFor(strings.Trim(role, `"`)), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := store.VerifyLeastPrivilege(ctx, probe); err == nil || !strings.Contains(err.Error(), "owns 1 application tables") {
		t.Fatalf("a non-superuser table owner must be refused, got %v", err)
	}
}

func TestEmbeddedPlansAreSeededAndImmutable(t *testing.T) {
	db := testdb.New(t)
	var n int
	if err := db.CP.QueryRow(context.Background(), `SELECT count(*) FROM ati.interview_questions WHERE plan_id = 'plan_demo_backend_en'`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("demo plan questions: n=%d err=%v", n, err)
	}
	if err := store.SeedPlan(context.Background(), db.Owner, []byte(`{"plan_id":"plan_bad"}`)); err == nil {
		t.Fatal("plans that do not match the plan contract must be rejected")
	}
}
