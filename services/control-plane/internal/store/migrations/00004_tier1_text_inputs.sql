-- Text response mode (ADR-018): the Tier-1 agent sees the candidate's text answers for the open
-- question only, as untrusted input. No other candidate text is ever exposed to Tier-1.

-- +goose Up
CREATE VIEW ati.tier1_text_inputs_v AS
SELECT t.session_id, t.id AS segment_id, t.activation_id, t.text AS untrusted_text, t.sequence
FROM ati.transcript_segments t
JOIN ati.question_activations a ON a.id = t.activation_id
WHERE t.source = 'candidate_text' AND t.producer = 'control_plane' AND a.closed_at IS NULL;
GRANT SELECT ON ati.tier1_text_inputs_v TO ati_tier1_ctx;

-- +goose Down
DROP VIEW ati.tier1_text_inputs_v;
