# ADR-017 — Optional audio recording, off by default

**Status:** Proposed (owner-approved direction, 2026-11-03) — pending architecture review
**Date:** 2026-11-03
**Relates to:** ADR-009, BLUEPRINT §11, §13, §15; THREAT_MODEL_AND_QA §4

## Context

BLUEPRINT §11 requires an appeal process. Without audio, a claim that "the transcript is wrong about what I said" cannot be checked, and an evidence transcript cannot be re-transcribed by an independent, higher-accuracy process (ADR-014). Raw voice is sensitive personal data, so ADR-009 requires minimization, disclosure, encryption, retention limits and deletion.

## Decision

1. Recording is **disabled by default** and **fail-closed**. A session is recorded only if *all* of these hold:
   - The feature flag is on.
   - Tenant policy allows recording.
   - The candidate granted the optional `audio_recording` purpose on the pinned notice.
   - An explicit audio retention period is configured (there is no default).
   - Storage encryption at rest is attested in configuration.
   - The session is in voice mode.

   Otherwise nothing starts and the attempt is audited as `recording.skipped` with every failing reason. Tenant policy is `tenants.recording_allowed` (default false, set with `ati-admin tenant-recording`). When the feature is off for the deployment, no recorder runs at all.
2. **Scope:** only the candidate's microphone track, through LiveKit TrackEgress (OGG/Opus, no transcoding), and only while an activation is open. Calibration, pauses and the interviewer's voice are never recorded. Recording is attempted by the `start_recording` effect of T05 (first question after calibration) and T13 (resume), and when the candidate's microphone is published while a question is open; it stops on the `stop_recording` effects (pause, withdrawal, completion) and when the microphone track is unpublished. A new recording segment starts after resume. Status: `starting` → `active` (egress started) → `stopping` → `complete` or `failed`; at most one live recording per session (database index).
3. **Storage:** S3-compatible object storage under opaque keys (`<tenant>/<session>/<recording>.ogg`). Storage credentials live on the egress service and are never sent per request. The `audio_recordings` table holds egress ID, object key, server-timeline bounds, status, `retention_until` and deletion state. Candidate transcript segments get `audio_recording_id` and `audio_offset_ms` when their start lies inside a recording's bounds. Tier-1 learns only whether a recording is running (`tier1_recording_v`).
4. **Retention and deletion:** `ati-admin recordings purge-expired` (control-plane role; storage credentials from `ATI_RECORDING_S3_*`) deletes the objects of finished recordings whose retention has ended, never live ones, and keeps the row as an audited tombstone (`purged_at`, `purge_reason`). The application role cannot delete recording rows. Recordings can never be used to assess voice, accent or tone (BLUEPRINT §7), and Tier-2 never receives audio in Phase 2 without a further ADR.
5. **Access:** Phase 1 has no download or playback API. Reviewer access with an audit trail is a Phase 5 item.

## Consequences

Appeals and re-transcription become possible for candidates who opt in, and declining has no effect on the interview. Real egress cannot be verified in this development environment (no egress service). The policy, API calls, webhooks and storage lifecycle are tested against fakes and an in-process S3 server, and are reported as unverified against a real egress service.

## Verification

Unit and integration tests cover the policy matrix (each precondition alone disables recording), start and stop on every transition, egress webhook handling, `audio_ref` offsets, and purge with tombstones against an in-process S3-compatible server.
