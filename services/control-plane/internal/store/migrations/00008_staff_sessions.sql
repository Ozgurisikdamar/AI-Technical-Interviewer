-- Staff browser sessions (ADR-023). A staff member signs in to the review console by pasting a
-- staff token once; the control plane answers with a short-lived, revocable session cookie and
-- keeps only its hash, as it does for candidate sessions (ADR-016). A session is valid only while
-- its staff token is: revoking or expiring the token ends every session made from it.
--
-- The control plane may create sessions and mark them revoked, nothing else. Ended sessions are
-- deleted by the audited operational purge (ati-admin purge-operational).
--
-- review_outcomes.ai_draft_viewed records whether the reviewer opened the AI draft before
-- deciding, so automation bias can be studied later (DATA_AND_ML_LIFECYCLE.md). Existing outcomes
-- predate the console and are recorded as not viewed.

-- +goose Up
CREATE TABLE ati.staff_sessions (
  id             text PRIMARY KEY CHECK (id ~ '^sss_[A-Za-z0-9]{16,}$'),
  staff_token_id text NOT NULL REFERENCES ati.staff_tokens (id),
  token_hash     text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  created_at     timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  revoked_at     timestamptz,
  CHECK (expires_at > created_at)
);
CREATE INDEX staff_sessions_token ON ati.staff_sessions (staff_token_id);
GRANT SELECT, INSERT ON ati.staff_sessions TO ati_control_plane;
GRANT UPDATE (revoked_at) ON ati.staff_sessions TO ati_control_plane;

ALTER TABLE ati.review_outcomes ADD COLUMN ai_draft_viewed boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE ati.review_outcomes DROP COLUMN ai_draft_viewed;
REVOKE ALL ON ati.staff_sessions FROM ati_control_plane;
DROP TABLE ati.staff_sessions;
