-- Phase 2 evaluation core (ADR-022): versioned rubrics, raw evaluation attempts, canonical
-- evaluations, human review and reports.
--
-- Tier-2 reads evidence through ati_tier2_evaluator (identifiers, transcripts, questions, rubrics;
-- never candidate identity) and writes nothing: its results reach these tables only through the
-- control plane, which validates them first (ADR-007, ADR-015).
--
-- Raw attempts and canonical evaluations are append-only for the control plane by grants; their
-- quotes of candidate speech carry a retention date, and a separately audited purge (gap G1 of
-- the data inventory) may clear raw_output. Review outcomes and reports are immutable records.

-- +goose Up
CREATE TABLE ati.rubrics (
  rubric_id  text NOT NULL CHECK (rubric_id ~ '^rub_[a-z0-9_]{3,60}$'),
  version    int  NOT NULL CHECK (version >= 1),
  sha256     text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  synthetic  boolean NOT NULL,
  body       jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (rubric_id, version)
);
CREATE TRIGGER rubrics_immutable BEFORE UPDATE OR DELETE ON ati.rubrics FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER rubrics_no_truncate BEFORE TRUNCATE ON ati.rubrics FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();

-- Every evaluator response, valid or not, exactly as received (PROMPT_AND_EVALUATION section 5).
CREATE TABLE ati.evaluation_attempts (
  id                  text PRIMARY KEY CHECK (id ~ '^eva_[A-Za-z0-9]{16,}$'),
  seq                 bigserial NOT NULL UNIQUE, -- insertion order (timestamps can tie)
  tenant_id           text NOT NULL REFERENCES ati.tenants (id),
  session_id          text NOT NULL REFERENCES ati.interview_sessions (id),
  answer_id           text NOT NULL REFERENCES ati.candidate_answers (id),
  transcript_version  int  NOT NULL CHECK (transcript_version >= 1),
  rubric_id           text NOT NULL,
  rubric_version      int  NOT NULL,
  evaluator_version   text NOT NULL CHECK (length(evaluator_version) BETWEEN 1 AND 200),
  provider            text NOT NULL CHECK (provider IN ('reference', 'openai', 'anthropic')),
  model_id            text CHECK (length(model_id) <= 128),
  prompt_sha256       text CHECK (prompt_sha256 ~ '^[0-9a-f]{64}$'),
  provider_request_id text CHECK (length(provider_request_id) <= 200),
  outcome             text NOT NULL CHECK (outcome IN ('valid', 'invalid', 'abstained', 'failed')),
  problems            text[] NOT NULL DEFAULT '{}',
  raw_output          jsonb,
  latency_ms          int CHECK (latency_ms >= 0),
  input_tokens        int CHECK (input_tokens >= 0),
  output_tokens       int CHECK (output_tokens >= 0),
  retention_until     timestamptz NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (rubric_id, rubric_version) REFERENCES ati.rubrics (rubric_id, version),
  CHECK (outcome = 'valid' OR cardinality(problems) >= 1)
);
CREATE INDEX evaluation_attempts_answer ON ati.evaluation_attempts (answer_id, seq);

-- Validated evaluations only: one per answer, transcript version, rubric and evaluator (INV-2).
CREATE TABLE ati.evaluations (
  id                   text PRIMARY KEY CHECK (id ~ '^evl_[A-Za-z0-9]{16,}$'),
  seq                  bigserial NOT NULL UNIQUE, -- insertion order: the first valid evaluation is the reviewer's draft
  attempt_id           text NOT NULL UNIQUE REFERENCES ati.evaluation_attempts (id),
  tenant_id            text NOT NULL REFERENCES ati.tenants (id),
  session_id           text NOT NULL REFERENCES ati.interview_sessions (id),
  answer_id            text NOT NULL REFERENCES ati.candidate_answers (id),
  transcript_version   int  NOT NULL,
  rubric_id            text NOT NULL,
  rubric_version       int  NOT NULL,
  evaluator_version    text NOT NULL,
  scores               jsonb NOT NULL,
  -- Recomputed by the backend from the scores and the rubric's weights, in hundredths of a point.
  weighted_score_centi int  NOT NULL CHECK (weighted_score_centi BETWEEN 0 AND 10000),
  confidence           numeric(4,3) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  calibrated           boolean NOT NULL DEFAULT false CHECK (NOT calibrated),
  evidence             jsonb NOT NULL,
  routing_reasons      text[] NOT NULL CHECK (cardinality(routing_reasons) >= 1),
  recommendation       text NOT NULL CHECK (recommendation IN ('NEXT_QUESTION', 'FOLLOW_UP', 'HUMAN_REVIEW')),
  output_risk          text NOT NULL CHECK (output_risk IN ('low', 'medium', 'high')),
  retention_until      timestamptz NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (rubric_id, rubric_version) REFERENCES ati.rubrics (rubric_id, version),
  UNIQUE (answer_id, transcript_version, rubric_id, rubric_version, evaluator_version)
);

-- One human review task per answer (ADR-008, ADR-022: every answer is reviewed in this phase).
CREATE TABLE ati.review_tasks (
  id            text PRIMARY KEY CHECK (id ~ '^rvt_[A-Za-z0-9]{16,}$'),
  tenant_id     text NOT NULL REFERENCES ati.tenants (id),
  session_id    text NOT NULL REFERENCES ati.interview_sessions (id),
  answer_id     text NOT NULL UNIQUE REFERENCES ati.candidate_answers (id),
  evaluation_id text REFERENCES ati.evaluations (id),
  reasons       text[] NOT NULL CHECK (cardinality(reasons) >= 1),
  status        text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'completed')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  completed_at  timestamptz,
  CHECK ((status = 'completed') = (completed_at IS NOT NULL))
);
CREATE INDEX review_tasks_open ON ati.review_tasks (tenant_id, created_at) WHERE status = 'open';
CREATE INDEX review_tasks_session ON ati.review_tasks (session_id);

-- The reviewer's decision on one answer, with the prior AI scores it replaces (PROMPT_AND_EVALUATION section 6).
-- An answer whose question has no rubric is still reviewed, with a rationale and no scores.
CREATE TABLE ati.review_outcomes (
  id                         text PRIMARY KEY CHECK (id ~ '^rvo_[A-Za-z0-9]{16,}$'),
  task_id                    text NOT NULL UNIQUE REFERENCES ati.review_tasks (id),
  tenant_id                  text NOT NULL REFERENCES ati.tenants (id),
  session_id                 text NOT NULL REFERENCES ati.interview_sessions (id),
  answer_id                  text NOT NULL REFERENCES ati.candidate_answers (id),
  reviewer_id                text NOT NULL REFERENCES ati.staff_tokens (id),
  rubric_id                  text,
  rubric_version             int,
  scores                     jsonb,
  weighted_score_centi       int CHECK (weighted_score_centi BETWEEN 0 AND 10000),
  prior_evaluation_id        text REFERENCES ati.evaluations (id),
  prior_scores               jsonb,
  prior_weighted_score_centi int CHECK (prior_weighted_score_centi BETWEEN 0 AND 10000),
  rationale                  text NOT NULL CHECK (length(rationale) BETWEEN 10 AND 4000),
  decided_at                 timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (rubric_id, rubric_version) REFERENCES ati.rubrics (rubric_id, version),
  CHECK ((prior_evaluation_id IS NULL) = (prior_scores IS NULL)),
  CHECK ((rubric_id IS NULL) = (rubric_version IS NULL) AND (rubric_id IS NULL) = (scores IS NULL)
    AND (scores IS NULL) = (weighted_score_centi IS NULL) AND (rubric_id IS NOT NULL OR prior_evaluation_id IS NULL))
);
CREATE TRIGGER review_outcomes_append_only BEFORE UPDATE OR DELETE ON ati.review_outcomes FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER review_outcomes_no_truncate BEFORE TRUNCATE ON ati.review_outcomes FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();

-- The report snapshot written when the last review completes (T17). Decision support only.
CREATE TABLE ati.reports (
  id         text PRIMARY KEY CHECK (id ~ '^rep_[A-Za-z0-9]{16,}$'),
  tenant_id  text NOT NULL REFERENCES ati.tenants (id),
  session_id text NOT NULL UNIQUE REFERENCES ati.interview_sessions (id),
  body       jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER reports_append_only BEFORE UPDATE OR DELETE ON ati.reports FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER reports_no_truncate BEFORE TRUNCATE ON ati.reports FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();

-- Reviewers decide review tasks; recruiters manage interviews and read reports.
ALTER TABLE ati.staff_tokens DROP CONSTRAINT staff_tokens_role_check;
ALTER TABLE ati.staff_tokens ADD CONSTRAINT staff_tokens_role_check CHECK (role IN ('admin', 'recruiter', 'reviewer'));

-- What the Tier-2 evaluator may read beyond Phase 1's evidence grants: the session's state and
-- plan (no candidate, notice or room), and which evaluations already exist (to skip redeliveries).
CREATE VIEW ati.tier2_session_v AS
SELECT id AS session_id, tenant_id, state, plan_id, plan_version, notice_language AS language
FROM ati.interview_sessions;
CREATE VIEW ati.tier2_evaluation_keys_v AS
SELECT answer_id, transcript_version, rubric_id, rubric_version, evaluator_version FROM ati.evaluations;

GRANT SELECT ON ati.rubrics TO ati_control_plane;
GRANT SELECT, INSERT ON ati.evaluation_attempts, ati.evaluations, ati.review_outcomes, ati.reports TO ati_control_plane;
GRANT SELECT, INSERT, UPDATE ON ati.review_tasks TO ati_control_plane;
GRANT UPDATE (published_at) ON ati.outbox_events TO ati_control_plane;
GRANT USAGE ON SEQUENCE ati.evaluation_attempts_seq_seq, ati.evaluations_seq_seq TO ati_control_plane;
GRANT SELECT ON ati.rubrics, ati.tier2_session_v, ati.tier2_evaluation_keys_v TO ati_tier2_evaluator;

-- +goose Down
REVOKE SELECT ON ati.rubrics, ati.tier2_session_v, ati.tier2_evaluation_keys_v FROM ati_tier2_evaluator;
REVOKE UPDATE (published_at) ON ati.outbox_events FROM ati_control_plane;
DROP VIEW ati.tier2_evaluation_keys_v;
DROP VIEW ati.tier2_session_v;
ALTER TABLE ati.staff_tokens DROP CONSTRAINT staff_tokens_role_check;
ALTER TABLE ati.staff_tokens ADD CONSTRAINT staff_tokens_role_check CHECK (role IN ('admin', 'recruiter'));
DROP TABLE ati.reports;
DROP TABLE ati.review_outcomes;
DROP TABLE ati.review_tasks;
DROP TABLE ati.evaluations;
DROP TABLE ati.evaluation_attempts;
DROP TABLE ati.rubrics;
