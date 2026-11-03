-- Staff invitation revocation (ADR-016) pauses a live interview with its own reason, so the
-- pause record says why the candidate lost access.

-- +goose Up
ALTER TABLE ati.pause_intervals DROP CONSTRAINT pause_intervals_reason_check;
ALTER TABLE ati.pause_intervals ADD CONSTRAINT pause_intervals_reason_check
  CHECK (reason IN ('candidate_request', 'device_lost', 'network', 'agent_unavailable', 'provider_failure', 'audio_unavailable', 'access_revoked'));

-- +goose Down
ALTER TABLE ati.pause_intervals DROP CONSTRAINT pause_intervals_reason_check;
ALTER TABLE ati.pause_intervals ADD CONSTRAINT pause_intervals_reason_check
  CHECK (reason IN ('candidate_request', 'device_lost', 'network', 'agent_unavailable', 'provider_failure', 'audio_unavailable'));
