-- Tier-1 context views (ADR-011, ADR-016, conflict C10). The ati_tier1_ctx role can read these
-- views and nothing else. They expose only the allowlisted columns of tier1-context/v1: no rubric
-- references, no evaluation or review data, no candidate identity, no other session's data.
-- Post-interview states are collapsed so Tier-1 cannot learn evaluation or review outcomes.

-- +goose Up
CREATE VIEW ati.tier1_session_v AS
SELECT s.id AS session_id,
       CASE WHEN s.state IN ('EVALUATION_PENDING', 'HUMAN_REVIEW', 'REPORT_READY') THEN 'INTERVIEW_COMPLETED' ELSE s.state END AS state,
       s.state_version,
       p.language,
       p.seniority_band,
       s.input_mode,
       (extract(epoch FROM s.timeline_epoch) * 1000)::bigint AS epoch_unix_ms
FROM ati.interview_sessions s
JOIN ati.interview_plans p ON p.plan_id = s.plan_id AND p.version = s.plan_version
WHERE s.state NOT IN ('CREATED', 'CONSENT_PENDING', 'READY');

CREATE VIEW ati.tier1_active_prompt_v AS
SELECT a.session_id,
       a.id AS activation_id,
       a.item_id,
       a.kind,
       q.text,
       q.turn_profile,
       (a.prompt_started_ms IS NOT NULL) AS prompt_started
FROM ati.question_activations a
JOIN ati.interview_sessions s ON s.id = a.session_id
JOIN ati.interview_questions q ON q.plan_id = s.plan_id AND q.plan_version = s.plan_version AND q.item_id = a.item_id
WHERE a.closed_at IS NULL;

-- Question texts of finalized items only; never any candidate text.
CREATE VIEW ati.tier1_completed_prompts_v AS
SELECT ans.session_id, q.item_id, q.kind, q.text, q.ordinal
FROM ati.candidate_answers ans
JOIN ati.interview_sessions s ON s.id = ans.session_id
JOIN ati.interview_questions q ON q.plan_id = s.plan_id AND q.plan_version = s.plan_version AND q.item_id = ans.item_id;

CREATE VIEW ati.tier1_pending_completion_v AS
SELECT session_id, activation_id, (extract(epoch FROM deadline) * 1000)::bigint AS deadline_unix_ms
FROM ati.completion_requests
WHERE resolved_at IS NULL AND requested_by = 'candidate';

GRANT SELECT ON ati.tier1_session_v, ati.tier1_active_prompt_v, ati.tier1_completed_prompts_v, ati.tier1_pending_completion_v TO ati_tier1_ctx;

-- +goose Down
DROP VIEW ati.tier1_pending_completion_v;
DROP VIEW ati.tier1_completed_prompts_v;
DROP VIEW ati.tier1_active_prompt_v;
DROP VIEW ati.tier1_session_v;
