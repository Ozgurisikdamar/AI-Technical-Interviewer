-- Optional audio recording, off by default and fail-closed (ADR-017).
-- A tenant must opt in explicitly; each session additionally needs the candidate's consent, an
-- explicit retention period, attested storage encryption and voice mode (checked in code).
-- Rows are never deleted: purging removes the object and leaves an audited tombstone.

-- +goose Up
ALTER TABLE ati.tenants ADD COLUMN recording_allowed boolean NOT NULL DEFAULT false;

CREATE TABLE ati.audio_recordings (
  id              text PRIMARY KEY CHECK (id ~ '^rec_[A-Za-z0-9]{16,}$'),
  tenant_id       text NOT NULL REFERENCES ati.tenants (id),
  session_id      text NOT NULL REFERENCES ati.interview_sessions (id),
  egress_id       text UNIQUE,
  track_sid       text NOT NULL,
  -- Opaque key: <tenant>/<session>/<recording>.ogg. No candidate data in names.
  object_key      text NOT NULL UNIQUE CHECK (object_key ~ '^ten_[A-Za-z0-9]{16,}/ses_[A-Za-z0-9]{16,}/rec_[A-Za-z0-9]{16,}\.ogg$'),
  status          text NOT NULL CHECK (status IN ('starting', 'active', 'stopping', 'complete', 'failed')),
  -- Session-timeline bounds (ms since the session epoch); set from the egress lifecycle.
  started_ms      bigint CHECK (started_ms >= 0),
  ended_ms        bigint CHECK (ended_ms >= started_ms),
  failure         text CHECK (length(failure) <= 500),
  retention_until timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  purged_at       timestamptz,
  purge_reason    text CHECK (purge_reason IN ('retention_expired', 'deletion_request')),
  CHECK ((purged_at IS NULL) = (purge_reason IS NULL)),
  CHECK (purged_at IS NULL OR status IN ('complete', 'failed'))
);
-- At most one live recording per session.
CREATE UNIQUE INDEX audio_recordings_one_live ON ati.audio_recordings (session_id) WHERE status IN ('starting', 'active', 'stopping');
CREATE INDEX audio_recordings_retention ON ati.audio_recordings (retention_until) WHERE purged_at IS NULL;

ALTER TABLE ati.transcript_segments
  ADD CONSTRAINT transcript_segments_audio_recording_fk FOREIGN KEY (audio_recording_id) REFERENCES ati.audio_recordings (id);

GRANT SELECT, INSERT, UPDATE ON ati.audio_recordings TO ati_control_plane;

-- Tier-1 learns only whether a recording is running (tier1-context/v1 recording_active), never keys or files.
CREATE VIEW ati.tier1_recording_v AS
SELECT session_id, true AS recording_active
FROM ati.audio_recordings
WHERE status IN ('starting', 'active');
GRANT SELECT ON ati.tier1_recording_v TO ati_tier1_ctx;

-- +goose Down
DROP VIEW ati.tier1_recording_v;
ALTER TABLE ati.transcript_segments DROP CONSTRAINT transcript_segments_audio_recording_fk;
DROP TABLE ati.audio_recordings;
ALTER TABLE ati.tenants DROP COLUMN recording_allowed;
