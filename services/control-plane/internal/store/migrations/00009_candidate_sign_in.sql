-- +goose Up
-- Candidate sign-in with an application number and an access code (ADR-024).
-- The application number is a public identifier of the interview, safe to show or read aloud. The
-- access code is a second credential of the interview's invitation, stored only as an argon2id hash;
-- it stops working when the invitation expires or is revoked. Failed sign-ins are counted on the
-- invitation, and the fifth in a row locks code sign-in for a while (the invitation link still
-- works). Interviews created before this migration have neither: their link is the only way in.
-- The control plane's table-level grants on both tables cover the new columns.
ALTER TABLE ati.interview_sessions
  ADD COLUMN application_number text UNIQUE CHECK (application_number ~ '^[0-9A-HJKMNP-TV-Z]{8}$');

ALTER TABLE ati.invitations
  ADD COLUMN access_code_hash text
    CHECK (access_code_hash ~ '^[$]argon2id[$]v=19[$]m=[0-9]{1,6},t=[0-9]{1,2},p=[0-9]{1,2}[$][A-Za-z0-9+/]{22}[$][A-Za-z0-9+/]{43}$'),
  ADD COLUMN access_failures int NOT NULL DEFAULT 0 CHECK (access_failures BETWEEN 0 AND 10),
  ADD COLUMN access_locked_until timestamptz;

-- Revocation and sign-in find an interview's invitation by its session.
CREATE INDEX invitations_session ON ati.invitations (session_id);

-- +goose Down
DROP INDEX ati.invitations_session;
ALTER TABLE ati.invitations DROP COLUMN access_locked_until, DROP COLUMN access_failures, DROP COLUMN access_code_hash;
ALTER TABLE ati.interview_sessions DROP COLUMN application_number;
