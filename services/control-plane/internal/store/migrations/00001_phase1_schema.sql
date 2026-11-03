-- Phase 1 schema (ADR-012, ADR-014, ADR-015, ADR-016). PostgreSQL is canonical (ADR-005).
-- Requires the roles in bootstrap_roles.sql. Objects are owned by the migrating role, never by
-- an application role.

-- +goose Up
CREATE SCHEMA ati;
REVOKE ALL ON SCHEMA ati FROM PUBLIC;
GRANT USAGE ON SCHEMA ati TO ati_control_plane, ati_tier1_ctx, ati_tier2_evaluator;

-- +goose StatementBegin
CREATE FUNCTION ati.forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'ati.% is append-only', TG_TABLE_NAME USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd

CREATE TABLE ati.tenants (
  id         text PRIMARY KEY CHECK (id ~ '^ten_[A-Za-z0-9]{16,}$'),
  name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ati.staff_tokens (
  id         text PRIMARY KEY CHECK (id ~ '^stf_[A-Za-z0-9]{16,}$'),
  tenant_id  text NOT NULL REFERENCES ati.tenants (id),
  token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  role       text NOT NULL CHECK (role IN ('admin', 'recruiter')),
  label      text NOT NULL CHECK (length(label) BETWEEN 1 AND 100),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz
);

-- PII-minimal: only an opaque reference supplied by the hiring system (ADR-009).
CREATE TABLE ati.candidates (
  id           text PRIMARY KEY CHECK (id ~ '^cand_[A-Za-z0-9]{16,}$'),
  tenant_id    text NOT NULL REFERENCES ati.tenants (id),
  external_ref text NOT NULL CHECK (length(external_ref) BETWEEN 1 AND 128),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, external_ref)
);

-- Published plans are immutable (P&E section 5: evaluate against the rubric declared before the interview).
CREATE TABLE ati.interview_plans (
  plan_id        text NOT NULL CHECK (plan_id ~ '^plan_[a-z0-9_]{3,60}$'),
  version        int  NOT NULL CHECK (version >= 1),
  status         text NOT NULL CHECK (status = 'published'),
  language       text NOT NULL CHECK (language IN ('en', 'tr')),
  seniority_band text NOT NULL CHECK (seniority_band IN ('junior', 'mid', 'senior', 'staff')),
  synthetic      boolean NOT NULL,
  content        jsonb NOT NULL,
  content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (plan_id, version)
);

CREATE TABLE ati.interview_questions (
  plan_id        text NOT NULL,
  plan_version   int  NOT NULL,
  item_id        text NOT NULL,
  parent_item_id text,
  ordinal        int  NOT NULL CHECK (ordinal >= 1),
  kind           text NOT NULL CHECK (kind IN ('question', 'follow_up')),
  text           text NOT NULL CHECK (length(text) BETWEEN 1 AND 4000),
  modality       text NOT NULL CHECK (modality IN ('voice_qa')),
  turn_profile   text NOT NULL CHECK (turn_profile IN ('standard', 'extended_pause')),
  rubric_id      text,
  rubric_version int,
  PRIMARY KEY (plan_id, plan_version, item_id),
  UNIQUE (plan_id, plan_version, ordinal),
  FOREIGN KEY (plan_id, plan_version) REFERENCES ati.interview_plans (plan_id, version),
  CHECK ((kind = 'follow_up') = (parent_item_id IS NOT NULL))
);

CREATE TABLE ati.interview_sessions (
  id               text PRIMARY KEY CHECK (id ~ '^ses_[A-Za-z0-9]{16,}$'),
  tenant_id        text NOT NULL REFERENCES ati.tenants (id),
  candidate_id     text NOT NULL REFERENCES ati.candidates (id),
  plan_id          text NOT NULL,
  plan_version     int  NOT NULL,
  notice_id        text NOT NULL,
  notice_version   text NOT NULL,
  notice_language  text NOT NULL CHECK (notice_language IN ('en', 'tr')),
  state            text NOT NULL CHECK (state IN ('CREATED', 'CONSENT_PENDING', 'READY', 'CALIBRATION', 'QUESTION_ACTIVE',
                     'CANDIDATE_SPEAKING', 'ANSWER_PROCESSING', 'FOLLOW_UP', 'PAUSED', 'INTERVIEW_COMPLETED',
                     'EVALUATION_PENDING', 'HUMAN_REVIEW', 'REPORT_READY', 'CANCELLED')),
  state_version    bigint NOT NULL DEFAULT 1 CHECK (state_version >= 1),
  input_mode       text CHECK (input_mode IN ('voice', 'text')),
  paused_from      text CHECK (paused_from IN ('CALIBRATION', 'QUESTION_ACTIVE', 'CANDIDATE_SPEAKING')),
  paused_at        timestamptz,
  timeline_epoch   timestamptz,
  room_name        text NOT NULL UNIQUE CHECK (room_name ~ '^rm_[A-Za-z0-9]{16,}$'),
  segment_seq      bigint NOT NULL DEFAULT 0,
  lease_fencing    bigint NOT NULL DEFAULT 0,
  retention        jsonb NOT NULL,
  recording_policy jsonb NOT NULL,
  created_by       text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  completed_at     timestamptz,
  cancelled_at     timestamptz,
  cancel_reason    text CHECK (cancel_reason IN ('consent_declined', 'withdrawn', 'expired')),
  FOREIGN KEY (plan_id, plan_version) REFERENCES ati.interview_plans (plan_id, version),
  -- INV-9: transient states are resolved inside one transaction and never stored.
  CHECK (state NOT IN ('ANSWER_PROCESSING', 'FOLLOW_UP')),
  CHECK ((state = 'PAUSED') = (paused_from IS NOT NULL)),
  CHECK ((state = 'PAUSED') = (paused_at IS NOT NULL)),
  CHECK ((state = 'CANCELLED') = (cancel_reason IS NOT NULL)),
  CHECK (state IN ('CREATED', 'CONSENT_PENDING', 'READY', 'CANCELLED') OR (input_mode IS NOT NULL AND timeline_epoch IS NOT NULL))
);
CREATE INDEX interview_sessions_tenant ON ati.interview_sessions (tenant_id, created_at);
CREATE INDEX interview_sessions_paused ON ati.interview_sessions (paused_at) WHERE state = 'PAUSED';

CREATE TABLE ati.invitations (
  id           text PRIMARY KEY CHECK (id ~ '^inv_[A-Za-z0-9]{16,}$'),
  session_id   text NOT NULL REFERENCES ati.interview_sessions (id),
  token_hash   text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  first_exchanged_at timestamptz,
  revoked_at   timestamptz
);

CREATE TABLE ati.candidate_sessions (
  id         text PRIMARY KEY,
  session_id text NOT NULL REFERENCES ati.interview_sessions (id),
  token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz
);
CREATE UNIQUE INDEX candidate_sessions_one_active ON ati.candidate_sessions (session_id) WHERE revoked_at IS NULL;

CREATE TABLE ati.consent_records (
  id             text PRIMARY KEY CHECK (id ~ '^cns_[A-Za-z0-9]{16,}$'),
  session_id     text NOT NULL REFERENCES ati.interview_sessions (id),
  notice_id      text NOT NULL,
  notice_version text NOT NULL,
  purpose_id     text NOT NULL,
  required       boolean NOT NULL,
  granted        boolean NOT NULL,
  basis          text NOT NULL,
  retention_days int,
  decided_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (session_id, notice_version, purpose_id)
);

CREATE TABLE ati.question_activations (
  id                text PRIMARY KEY CHECK (id ~ '^act_[A-Za-z0-9]{16,}$'),
  session_id        text NOT NULL REFERENCES ati.interview_sessions (id),
  item_id           text NOT NULL,
  parent_item_id    text,
  kind              text NOT NULL CHECK (kind IN ('question', 'follow_up')),
  attempt           int  NOT NULL CHECK (attempt >= 1),
  opened_at         timestamptz NOT NULL DEFAULT now(),
  opened_ms         bigint NOT NULL CHECK (opened_ms >= 0),
  prompt_started_ms bigint,
  closed_at         timestamptz,
  closed_ms         bigint,
  close_reason      text CHECK (close_reason IN ('finalized', 'paused', 'withdrawn', 'expired')),
  UNIQUE (session_id, item_id, attempt),
  CHECK ((closed_at IS NULL) = (close_reason IS NULL)),
  CHECK ((closed_at IS NULL) = (closed_ms IS NULL)),
  CHECK (closed_ms IS NULL OR closed_ms >= opened_ms)
);
-- INV-1 enforced by the database: at most one open activation per session.
CREATE UNIQUE INDEX question_activations_one_open ON ati.question_activations (session_id) WHERE closed_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION ati.guard_activation_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.closed_at IS NOT NULL THEN
    RAISE EXCEPTION 'closed activation % is immutable', OLD.id USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.id <> OLD.id OR NEW.session_id <> OLD.session_id OR NEW.item_id <> OLD.item_id OR NEW.attempt <> OLD.attempt
     OR NEW.kind <> OLD.kind OR NEW.opened_at <> OLD.opened_at OR NEW.opened_ms <> OLD.opened_ms
     OR NEW.parent_item_id IS DISTINCT FROM OLD.parent_item_id THEN
    RAISE EXCEPTION 'activation identity is immutable' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF OLD.prompt_started_ms IS NOT NULL AND NEW.prompt_started_ms IS DISTINCT FROM OLD.prompt_started_ms THEN
    RAISE EXCEPTION 'prompt start can only be recorded once' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER question_activations_guard BEFORE UPDATE ON ati.question_activations
  FOR EACH ROW EXECUTE FUNCTION ati.guard_activation_update();

CREATE TABLE ati.transcript_segments (
  id                 text PRIMARY KEY CHECK (id ~ '^seg_[A-Za-z0-9]{16,}$'),
  session_id         text NOT NULL REFERENCES ati.interview_sessions (id),
  sequence           bigint NOT NULL CHECK (sequence >= 1),
  segment_key        text NOT NULL,
  content_sha256     text NOT NULL,
  transcript_version int  NOT NULL DEFAULT 1 CHECK (transcript_version >= 1),
  speaker            text NOT NULL CHECK (speaker IN ('candidate', 'interviewer')),
  text               text NOT NULL,
  start_ms           bigint NOT NULL CHECK (start_ms >= 0),
  end_ms             bigint NOT NULL,
  source             text NOT NULL CHECK (source IN ('realtime_transcription', 'interviewer_output', 'candidate_text', 'synthetic_test')),
  confidence         real CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
  language           text,
  activation_id      text REFERENCES ati.question_activations (id),
  item_id            text,
  turn_id            text,
  evidence_eligible  boolean NOT NULL,
  quality            text NOT NULL DEFAULT 'unverified' CHECK (quality IN ('unverified', 'human_verified')),
  interrupted        boolean NOT NULL DEFAULT false,
  flags              jsonb NOT NULL,
  prompt_similarity  real CHECK (prompt_similarity IS NULL OR prompt_similarity BETWEEN 0 AND 1),
  producer           text NOT NULL CHECK (producer IN ('tier1_agent', 'control_plane')),
  lease_id           text,
  fencing_token      bigint,
  provider_item_id   text,
  asr_model          text,
  engine             text CHECK (engine IN ('openai_realtime', 'scripted')),
  audio_recording_id text,
  audio_offset_ms    bigint,
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (session_id, sequence),
  UNIQUE (session_id, segment_key, transcript_version),
  CHECK (end_ms >= start_ms),
  CHECK (speaker = 'candidate' OR NOT evidence_eligible),
  CHECK ((producer = 'tier1_agent') = (fencing_token IS NOT NULL))
);
CREATE INDEX transcript_segments_activation ON ati.transcript_segments (activation_id);
CREATE INDEX transcript_segments_timeline ON ati.transcript_segments (session_id, start_ms, sequence);

CREATE TABLE ati.candidate_answers (
  id                 text PRIMARY KEY CHECK (id ~ '^ans_[A-Za-z0-9]{16,}$'),
  session_id         text NOT NULL REFERENCES ati.interview_sessions (id),
  item_id            text NOT NULL,
  parent_item_id     text,
  answer_version     int  NOT NULL DEFAULT 1 CHECK (answer_version >= 1),
  transcript_version int  NOT NULL DEFAULT 1,
  segment_ids        text[] NOT NULL,
  last_sequence      bigint,
  missing_segments   boolean NOT NULL,
  integrity_flags    text[] NOT NULL DEFAULT '{}',
  finalization_cause text NOT NULL CHECK (finalization_cause IN ('agent_tool', 'candidate_request', 'candidate_request_deadline')),
  finalized_by       text NOT NULL CHECK (finalized_by IN ('tier1_agent', 'system')),
  finalized_at       timestamptz NOT NULL DEFAULT now(),
  -- INV-4: one answer per (session, plan item, answer_version).
  UNIQUE (session_id, item_id, answer_version)
);

CREATE TABLE ati.completion_requests (
  id            bigserial PRIMARY KEY,
  session_id    text NOT NULL REFERENCES ati.interview_sessions (id),
  activation_id text NOT NULL REFERENCES ati.question_activations (id),
  requested_by  text NOT NULL CHECK (requested_by IN ('candidate', 'tier1_agent')),
  requested_at  timestamptz NOT NULL DEFAULT now(),
  deadline      timestamptz NOT NULL,
  resolved_at   timestamptz,
  resolution    text CHECK (resolution IN ('finalized', 'cancelled')),
  CHECK ((resolved_at IS NULL) = (resolution IS NULL))
);
CREATE UNIQUE INDEX completion_requests_one_open ON ati.completion_requests (activation_id) WHERE resolved_at IS NULL;
CREATE INDEX completion_requests_deadline ON ati.completion_requests (deadline) WHERE resolved_at IS NULL;

CREATE TABLE ati.pause_intervals (
  id         bigserial PRIMARY KEY,
  session_id text NOT NULL REFERENCES ati.interview_sessions (id),
  started_ms bigint NOT NULL CHECK (started_ms >= 0),
  ended_ms   bigint,
  reason     text NOT NULL CHECK (reason IN ('candidate_request', 'device_lost', 'network', 'agent_unavailable', 'provider_failure', 'audio_unavailable')),
  CHECK (ended_ms IS NULL OR ended_ms >= started_ms)
);
CREATE UNIQUE INDEX pause_intervals_one_open ON ati.pause_intervals (session_id) WHERE ended_ms IS NULL;

CREATE TABLE ati.session_events (
  id            bigserial PRIMARY KEY,
  session_id    text NOT NULL REFERENCES ati.interview_sessions (id),
  state_version bigint NOT NULL,
  transition_id text NOT NULL,
  event         text NOT NULL,
  from_state    text NOT NULL,
  to_state      text NOT NULL,
  actor         text NOT NULL CHECK (actor IN ('candidate', 'tier1_agent', 'system', 'tier2_pipeline', 'reviewer')),
  cause         text,
  request_id    text,
  occurred_at   timestamptz NOT NULL DEFAULT now(),
  details       jsonb NOT NULL DEFAULT '{}',
  UNIQUE (session_id, state_version)
);

CREATE TABLE ati.integrity_events (
  id          bigserial PRIMARY KEY,
  session_id  text NOT NULL REFERENCES ati.interview_sessions (id),
  kind        text NOT NULL,
  details     jsonb NOT NULL DEFAULT '{}',
  occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ati.audit_logs (
  id           bigserial PRIMARY KEY,
  tenant_id    text,
  occurred_at  timestamptz NOT NULL DEFAULT now(),
  actor_type   text NOT NULL,
  actor_id     text,
  action       text NOT NULL,
  subject_type text NOT NULL,
  subject_id   text,
  outcome      text NOT NULL CHECK (outcome IN ('allowed', 'denied', 'error')),
  request_id   text,
  details      jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE ati.outbox_events (
  id            text PRIMARY KEY CHECK (id ~ '^evt_[A-Za-z0-9]{16,}$'),
  seq           bigserial NOT NULL UNIQUE,  -- insertion order; relays publish in seq order
  tenant_id     text NOT NULL,
  session_id    text NOT NULL REFERENCES ati.interview_sessions (id),
  event_type    text NOT NULL CHECK (event_type IN ('answer.finalized', 'interview.completed')),
  event_version int  NOT NULL,
  payload       jsonb NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  published_at  timestamptz
);
CREATE INDEX outbox_events_unpublished ON ati.outbox_events (seq) WHERE published_at IS NULL;

CREATE TABLE ati.idempotency_records (
  principal      text NOT NULL,
  idem_key       text NOT NULL,
  route          text NOT NULL,
  request_sha256 text NOT NULL,
  status         int,
  response       jsonb,
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (principal, idem_key)
);

CREATE TABLE ati.agent_dispatches (
  id                  bigserial PRIMARY KEY,
  session_id          text NOT NULL REFERENCES ati.interview_sessions (id),
  livekit_dispatch_id text,
  nonce_hash          text NOT NULL UNIQUE CHECK (nonce_hash ~ '^[0-9a-f]{64}$'),
  nonce_expires_at    timestamptz NOT NULL,
  claimed_at          timestamptz,
  claimed_job_id      text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  superseded_at       timestamptz
);

CREATE TABLE ati.agent_leases (
  id             text PRIMARY KEY CHECK (id ~ '^lea_[A-Za-z0-9]{16,}$'),
  session_id     text NOT NULL REFERENCES ati.interview_sessions (id),
  fencing_token  bigint NOT NULL CHECK (fencing_token >= 1),
  grant_hash     text NOT NULL UNIQUE CHECK (grant_hash ~ '^[0-9a-f]{64}$'),
  job_id         text NOT NULL,
  agent_identity text NOT NULL,
  issued_at      timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  ended_at       timestamptz,
  end_reason     text CHECK (end_reason IN ('released', 'superseded', 'expired', 'session_ended')),
  UNIQUE (session_id, fencing_token),
  CHECK ((ended_at IS NULL) = (end_reason IS NULL))
);
CREATE UNIQUE INDEX agent_leases_one_active ON ati.agent_leases (session_id) WHERE ended_at IS NULL;

CREATE TABLE ati.webhook_events (
  event_id     text PRIMARY KEY,
  event_type   text NOT NULL,
  created_unix bigint NOT NULL,
  received_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ati.session_usage (
  session_id          text PRIMARY KEY REFERENCES ati.interview_sessions (id),
  engine              text NOT NULL CHECK (engine IN ('openai_realtime', 'scripted')),
  model               text,
  input_audio_ms      bigint NOT NULL DEFAULT 0 CHECK (input_audio_ms >= 0),
  output_audio_ms     bigint NOT NULL DEFAULT 0 CHECK (output_audio_ms >= 0),
  input_text_tokens   bigint,
  output_text_tokens  bigint,
  input_audio_tokens  bigint,
  output_audio_tokens bigint,
  updated_at          timestamptz NOT NULL DEFAULT now()
);

-- Append-only enforcement for evidence, decisions and audit (ADR-014, ADR-008).
CREATE TRIGGER consent_records_append_only BEFORE UPDATE OR DELETE ON ati.consent_records FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER transcript_segments_append_only BEFORE UPDATE OR DELETE ON ati.transcript_segments FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER candidate_answers_append_only BEFORE UPDATE OR DELETE ON ati.candidate_answers FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER session_events_append_only BEFORE UPDATE OR DELETE ON ati.session_events FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER integrity_events_append_only BEFORE UPDATE OR DELETE ON ati.integrity_events FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER audit_logs_append_only BEFORE UPDATE OR DELETE ON ati.audit_logs FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER interview_plans_immutable BEFORE UPDATE OR DELETE ON ati.interview_plans FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER interview_questions_immutable BEFORE UPDATE OR DELETE ON ati.interview_questions FOR EACH ROW EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER consent_records_no_truncate BEFORE TRUNCATE ON ati.consent_records FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER transcript_segments_no_truncate BEFORE TRUNCATE ON ati.transcript_segments FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER candidate_answers_no_truncate BEFORE TRUNCATE ON ati.candidate_answers FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER session_events_no_truncate BEFORE TRUNCATE ON ati.session_events FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();
CREATE TRIGGER audit_logs_no_truncate BEFORE TRUNCATE ON ati.audit_logs FOR EACH STATEMENT EXECUTE FUNCTION ati.forbid_mutation();

-- Least-privilege grants. The control plane never receives DELETE or DDL; append-only tables get
-- SELECT/INSERT only. Retention purges run through separately audited procedures (ADR-017).
GRANT SELECT ON ati.tenants, ati.staff_tokens, ati.interview_plans, ati.interview_questions TO ati_control_plane;
GRANT SELECT, INSERT ON ati.candidates, ati.consent_records, ati.transcript_segments, ati.candidate_answers,
  ati.session_events, ati.integrity_events, ati.audit_logs, ati.outbox_events, ati.webhook_events TO ati_control_plane;
GRANT SELECT, INSERT, UPDATE ON ati.interview_sessions, ati.invitations, ati.candidate_sessions, ati.question_activations,
  ati.completion_requests, ati.pause_intervals, ati.idempotency_records, ati.agent_dispatches, ati.agent_leases,
  ati.session_usage TO ati_control_plane;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA ati TO ati_control_plane;

-- Tier-2 evaluator (NOLOGIN until Phase 2): evidence reads only, never live-interview writes.
GRANT SELECT ON ati.transcript_segments, ati.candidate_answers, ati.question_activations, ati.interview_questions,
  ati.outbox_events TO ati_tier2_evaluator;

-- +goose Down
DROP SCHEMA ati CASCADE;
