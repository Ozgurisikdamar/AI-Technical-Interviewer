"""Structural tests of the interview state-machine contract v1.1 (BLUEPRINT section 4, ADR-012)."""

from __future__ import annotations

from collections import deque
from typing import Any

from jsonschema import Draft202012Validator

from conftest import CONTRACTS, load_json

LIVE_OR_PRE = {"pre_interview", "live"}


def _states(sm: dict[str, Any]) -> dict[str, dict[str, Any]]:
    return {s["name"]: s for s in sm["states"]}


def test_contract_matches_its_format_schema(state_machine: dict[str, Any]) -> None:
    schema = load_json(CONTRACTS / "state-machine/state-machine.schema.json")
    Draft202012Validator.check_schema(schema)
    errors = list(Draft202012Validator(schema).iter_errors(state_machine))
    assert not errors, [e.message for e in errors]


def test_references_are_closed(state_machine: dict[str, Any]) -> None:
    states = _states(state_machine)
    assert len(states) == len(state_machine["states"]), "duplicate state names"
    assert state_machine["initial_state"] in states
    ids = [t["id"] for t in state_machine["transitions"]]
    assert len(ids) == len(set(ids)), "duplicate transition ids"
    actors = set(state_machine["actors"])
    guards = set(state_machine["guards"])
    requests = {r["id"]: r for r in state_machine["requests"]}
    for t in state_machine["transitions"]:
        assert t["from"] in states and t["to"] in states, t["id"]
        assert set(t["actors"]) <= actors, t["id"]
        assert set(t["guards"]) <= guards, f"{t['id']} uses an undefined guard"
        if "requires_request" in t:
            assert t["requires_request"] in requests, t["id"]
    for r in requests.values():
        assert set(r["actors"]) <= actors
        assert set(r["allowed_in"]) <= set(states)
        assert r["resolves_to"] in ids
    used_guards = {g for t in state_machine["transitions"] for g in t["guards"]}
    assert guards == used_guards, f"unused guard definitions: {sorted(guards - used_guards)}"


# Guard families whose members are mutually exclusive by definition (paused_from has one value).
EXCLUSIVE_GUARDS = [{"paused_from_question", "paused_from_calibration"}]
# Outgoing chain steps from ANSWER_PROCESSING must be mutually exclusive, so the flow engine is deterministic.
ANSWER_PROCESSING_GUARDS = {"approved_follow_up_pending", "next_plan_item_exists", "plan_exhausted"}


def test_events_are_deterministic(state_machine: dict[str, Any]) -> None:
    by_key: dict[tuple[str, str], list[dict[str, Any]]] = {}
    for t in state_machine["transitions"]:
        by_key.setdefault((t["from"], t["event"]), []).append(t)
    for (src, event), ts in by_key.items():
        if len(ts) == 1:
            continue
        guard_sets = [set(t["guards"]) for t in ts]
        assert all(guard_sets), f"{event} from {src}: duplicates need guards"
        union = set().union(*guard_sets)
        assert any(union <= family for family in EXCLUSIVE_GUARDS), f"{event} from {src}: guards not provably exclusive"
        assert sum(len(g) for g in guard_sets) == len(union), f"{event} from {src}: overlapping guards"
    outgoing = [t for t in state_machine["transitions"] if t["from"] == "ANSWER_PROCESSING"]
    assert {g for t in outgoing for g in t["guards"]} == ANSWER_PROCESSING_GUARDS
    assert all(len(t["guards"]) == 1 for t in outgoing)
    descriptions = state_machine["guards"]
    assert "no unconditional follow-up is pending" in descriptions["next_plan_item_exists"].lower()
    assert "no further plan item or follow-up exists" in descriptions["plan_exhausted"].lower()


def test_terminal_states_have_no_outgoing_transitions(state_machine: dict[str, Any]) -> None:
    terminal = {s["name"] for s in state_machine["states"] if s["terminal"]}
    assert terminal == {"REPORT_READY", "CANCELLED"}
    assert not [t for t in state_machine["transitions"] if t["from"] in terminal]


def test_every_state_reachable_from_initial(state_machine: dict[str, Any]) -> None:
    edges: dict[str, set[str]] = {}
    for t in state_machine["transitions"]:
        edges.setdefault(t["from"], set()).add(t["to"])
    seen = {state_machine["initial_state"]}
    queue = deque(seen)
    while queue:
        for nxt in edges.get(queue.popleft(), ()):
            if nxt not in seen:
                seen.add(nxt)
                queue.append(nxt)
    assert seen == set(_states(state_machine)), f"unreachable: {set(_states(state_machine)) - seen}"


def test_transient_states_are_entered_and_left_by_system_chains(state_machine: dict[str, Any]) -> None:
    """INV-9: transient states are never observed at rest."""
    transient = {s["name"] for s in state_machine["states"] if s["transient"]}
    assert transient == {"ANSWER_PROCESSING", "FOLLOW_UP"}
    for t in state_machine["transitions"]:
        if t["from"] in transient:
            assert t.get("chain") is True and t["actors"] == ["system"], t["id"]
    # Every transient state must always have a way out, so a chain can never get stuck.
    for name in transient:
        assert any(t["from"] == name for t in state_machine["transitions"]), name
    # A transient state can't be paused, withdrawn or resumed into.
    assert not [t for t in state_machine["transitions"] if t["to"] in transient and t["event"] in {"resume", "pause", "withdraw"}]


def test_inv1_activation_lifecycle_is_structurally_balanced(state_machine: dict[str, Any]) -> None:
    """INV-1: entering an activation-bearing state opens an activation unless one is already open;
    leaving to a state without an activation closes it."""
    states = _states(state_machine)
    for t in state_machine["transitions"]:
        src_has = states[t["from"]]["requires_open_activation"]
        dst_has = states[t["to"]]["requires_open_activation"]
        opens = [e for e in t["effects"] if e.startswith("open_activation")]
        closes = "close_activation" in t["effects"]
        if dst_has and not src_has:
            assert len(opens) == 1, f"{t['id']} must open exactly one activation"
        if src_has and not dst_has:
            assert closes, f"{t['id']} must close the open activation"
        if src_has and dst_has:
            assert not opens and not closes, f"{t['id']} must keep the same activation"
        if not dst_has:
            assert not opens, f"{t['id']} opens an activation into a state that has none"


def test_inv3_no_transition_is_driven_by_barge_in_or_vad(state_machine: dict[str, Any]) -> None:
    forbidden = ("barge", "vad", "interrupt", "speech_started", "speech_stopped", "turn_end")
    for t in state_machine["transitions"]:
        assert not any(f in t["event"] for f in forbidden), t["id"]


def test_inv6_tier2_and_reviewer_have_no_live_or_pre_interview_transitions(state_machine: dict[str, Any]) -> None:
    states = _states(state_machine)
    for t in state_machine["transitions"]:
        if {"tier2_pipeline", "reviewer"} & set(t["actors"]):
            assert states[t["from"]]["kind"] == "post_interview", t["id"]
    for r in state_machine["requests"]:
        assert not {"tier2_pipeline", "reviewer"} & set(r["actors"])


def test_inv8_tier1_capability_is_a_subset_of_candidate_capability(state_machine: dict[str, Any]) -> None:
    requests = {r["id"]: r for r in state_machine["requests"]}
    for t in state_machine["transitions"]:
        if "tier1_agent" not in t["actors"]:
            continue
        candidate_direct = "candidate" in t["actors"]
        candidate_via_request = "requires_request" in t and "candidate" in requests[t["requires_request"]]["actors"]
        assert candidate_direct or candidate_via_request, f"{t['id']}: Tier-1 may not do more than the candidate"


def test_llm_reachable_request_is_only_answer_completion(state_machine: dict[str, Any]) -> None:
    """INV-5: the only model-reachable operation is R01 (tool mark_answer_complete)."""
    tier1_requests = [r for r in state_machine["requests"] if "tier1_agent" in r["actors"]]
    assert [r["name"] for r in tier1_requests] == ["answer_completion_requested"]
    assert tier1_requests[0]["allowed_in"] == ["CANDIDATE_SPEAKING"]


def test_answer_finalization_requires_a_request(state_machine: dict[str, Any]) -> None:
    finals = [t for t in state_machine["transitions"] if t["event"] == "answer_finalized"]
    assert len(finals) == 1
    assert finals[0]["requires_request"] == "R01"
    assert "finalize_answer" in finals[0]["effects"] and "emit_answer_finalized" in finals[0]["effects"]


def test_inv10_cancellation_never_emits_completion(state_machine: dict[str, Any]) -> None:
    for t in state_machine["transitions"]:
        if t["to"] == "CANCELLED":
            assert not [e for e in t["effects"] if e.startswith("emit_")], t["id"]
    completes = [t for t in state_machine["transitions"] if "emit_interview_completed" in t["effects"]]
    assert [t["to"] for t in completes] == ["INTERVIEW_COMPLETED"]


def test_owner_decision_pause_withdraw_expiry_is_complete(state_machine: dict[str, Any]) -> None:
    """ADR-012: pause from CALIBRATION/QUESTION_ACTIVE/CANDIDATE_SPEAKING; resume to the same question
    or calibration; withdraw from every pre-completion state after consent; expiry from PAUSED."""
    edges = {(t["from"], t["event"]): t for t in state_machine["transitions"]}
    for src in ("CALIBRATION", "QUESTION_ACTIVE", "CANDIDATE_SPEAKING"):
        assert edges[(src, "pause")]["to"] == "PAUSED"
        assert set(edges[(src, "pause")]["actors"]) == {"candidate", "system", "tier1_agent"}
    resumes = sorted(t["to"] for t in state_machine["transitions"] if t["event"] == "resume")
    assert resumes == ["CALIBRATION", "QUESTION_ACTIVE"]
    for src in ("READY", "CALIBRATION", "QUESTION_ACTIVE", "CANDIDATE_SPEAKING", "PAUSED"):
        assert edges[(src, "withdraw")]["to"] == "CANCELLED"
        assert edges[(src, "withdraw")]["actors"] == ["candidate"]
    assert edges[("PAUSED", "pause_expired")]["actors"] == ["system"]
    # Withdrawal and expiry delete the LiveKit room so stale tokens cannot rejoin (ADR-016).
    for t in state_machine["transitions"]:
        if t["to"] == "CANCELLED" and t["event"] in {"withdraw", "pause_expired"}:
            assert "delete_room" in t["effects"], t["id"]


def test_recording_stops_whenever_audio_intake_should_stop(state_machine: dict[str, Any]) -> None:
    """Pause, withdraw and completion must stop any active recording (ADR-017)."""
    # Recording is only active while an activation is open (QUESTION_ACTIVE / CANDIDATE_SPEAKING),
    # so every transition that closes an activation, and the final completion, must stop it.
    for t in state_machine["transitions"]:
        if "close_activation" in t["effects"] and t["event"] != "answer_finalized":
            assert "stop_recording" in t["effects"], t["id"]
    completion = [t for t in state_machine["transitions"] if t["to"] == "INTERVIEW_COMPLETED"]
    assert all("stop_recording" in t["effects"] for t in completion)


def test_invariants_cover_blueprint_list(state_machine: dict[str, Any]) -> None:
    ids = [i["id"] for i in state_machine["invariants"]]
    assert ids[:7] == [f"INV-{n}" for n in range(1, 8)]
    assert all(i["source"].startswith("BLUEPRINT 4.1") for i in state_machine["invariants"][:7])
