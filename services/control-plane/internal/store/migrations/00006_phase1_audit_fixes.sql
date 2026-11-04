-- Phase 1 audit fixes (docs/qa/PHASE1_AUDIT.md).
--
-- Webhooks: a delivery counts as a duplicate only once an earlier delivery was processed, so an
-- event whose processing failed is processed again when LiveKit retries it (it retries 5xx).
--
-- Indexes for the per-segment lookups of transcript ingestion, the agent dispatch checks and the
-- sweepers. In production, create them with CREATE INDEX CONCURRENTLY outside a transaction.

-- +goose Up
-- An idempotency claim whose request never finished (the process stopped mid-request) can be
-- taken over once it is older than any request can run.
ALTER TABLE ati.idempotency_records ADD COLUMN claimed_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE ati.webhook_events ADD COLUMN processed_at timestamptz;
UPDATE ati.webhook_events SET processed_at = received_at;
GRANT UPDATE (processed_at) ON ati.webhook_events TO ati_control_plane;

CREATE INDEX pause_intervals_session_started ON ati.pause_intervals (session_id, started_ms);
CREATE INDEX audio_recordings_session_started ON ati.audio_recordings (session_id, started_ms);
CREATE INDEX agent_dispatches_pending ON ati.agent_dispatches (session_id) WHERE claimed_at IS NULL AND superseded_at IS NULL;
-- The predicate matches the missing-agent sweeper's (SweepMissingAgents) so the planner can use it.
CREATE INDEX interview_sessions_live_updated ON ati.interview_sessions (updated_at)
  WHERE state IN ('CALIBRATION', 'QUESTION_ACTIVE', 'CANDIDATE_SPEAKING');
CREATE INDEX agent_leases_expiry ON ati.agent_leases (expires_at) WHERE ended_at IS NULL;

-- +goose Down
DROP INDEX ati.agent_leases_expiry;
DROP INDEX ati.interview_sessions_live_updated;
DROP INDEX ati.agent_dispatches_pending;
DROP INDEX ati.audio_recordings_session_started;
DROP INDEX ati.pause_intervals_session_started;
REVOKE UPDATE (processed_at) ON ati.webhook_events FROM ati_control_plane;
ALTER TABLE ati.webhook_events DROP COLUMN processed_at;
ALTER TABLE ati.idempotency_records DROP COLUMN claimed_at;
