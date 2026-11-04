package flow

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
)

func mustDefault(t *testing.T) *Machine {
	t.Helper()
	m, err := Default()
	if err != nil {
		t.Fatalf("load embedded contract: %v", err)
	}
	return m
}

func always(bool) GuardFunc { return func(string, Transition) (bool, error) { return true, nil } }

func guardsFrom(values map[string]bool) GuardFunc {
	return func(g string, _ Transition) (bool, error) { return values[g], nil }
}

func TestDefaultContractLoads(t *testing.T) {
	m := mustDefault(t)
	if m.Version != "1.2.0" || m.Initial != Created || m.MaxPhase != ImplementedPhase || ImplementedPhase != 2 {
		t.Fatalf("unexpected machine: version=%s initial=%s maxPhase=%d", m.Version, m.Initial, m.MaxPhase)
	}
	if got := len(m.States()); got != 14 {
		t.Fatalf("states = %d, want 14", got)
	}
	if got := len(m.Transitions()); got != 26 {
		t.Fatalf("transitions = %d, want 26", got)
	}
}

// Transitions belong to the phase that implements them: a Phase 1 machine refuses the evaluation
// transitions, the Phase 2 machine (contract v1.2, ADR-022) runs T14, T15 and T17. T16 resolves in
// the engine, but no control-plane operation fires it (every evaluation is human-reviewed).
func TestTransitionsBeyondTheImplementedPhaseAreRefused(t *testing.T) {
	m1, err := Load(contracts.MustRead(contracts.StateMachinePath), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.Resolve(InterviewCompleted, EvEvaluationEnqueued, ActorSystem, always(true)); !errors.Is(err, ErrPhaseNotWired) {
		t.Fatalf("a Phase 1 machine must refuse T14, got %v", err)
	}
	m := mustDefault(t)
	for _, c := range []struct {
		from  State
		event Event
		actor Actor
		want  string
	}{
		{InterviewCompleted, EvEvaluationEnqueued, ActorSystem, "T14"},
		{EvaluationPending, EvReviewRequired, ActorTier2, "T15"},
		{HumanReview, EvReviewCompleted, ActorReviewer, "T17"},
	} {
		if tr, err := m.Resolve(c.from, c.event, c.actor, always(true)); err != nil || tr.ID != c.want {
			t.Fatalf("%s from %s: %v %v", c.event, c.from, tr.ID, err)
		}
	}
	if _, err := m.Resolve(HumanReview, EvReviewCompleted, ActorTier2, always(true)); err == nil {
		t.Fatal("only a reviewer completes a review")
	}
}

func TestActorPermissionsAreEnforced(t *testing.T) {
	m := mustDefault(t)
	cases := []struct {
		from  State
		event Event
		actor Actor
		want  error
	}{
		{ConsentPending, EvConsentAccepted, ActorTier1, ErrActorNotPermitted},
		{ConsentPending, EvConsentAccepted, ActorTier2, ErrActorNotPermitted},
		{QuestionActive, EvAnswerStarted, ActorCandidate, ErrActorNotPermitted},
		{QuestionActive, EvAnswerStarted, ActorTier1, ErrActorNotPermitted},
		{CandidateSpeaking, EvAnswerFinalized, ActorCandidate, ErrActorNotPermitted}, // candidate only requests (R01)
		{Paused, EvResume, ActorTier1, ErrActorNotPermitted},
		{Paused, EvResume, ActorSystem, ErrActorNotPermitted},
		{QuestionActive, EvWithdraw, ActorTier1, ErrActorNotPermitted},
		{Paused, EvPauseExpired, ActorCandidate, ErrActorNotPermitted},
		{QuestionActive, EvNextQuestion, ActorCandidate, ErrNoTransition},
		{Calibration, EvAnswerFinalized, ActorTier1, ErrNoTransition},
	}
	for _, c := range cases {
		_, err := m.Resolve(c.from, c.event, c.actor, always(true))
		if !errors.Is(err, c.want) {
			t.Errorf("%s --%s--> as %s: want %v, got %v", c.from, c.event, c.actor, c.want, err)
		}
	}
}

func TestTier2AndReviewerCannotTouchLiveOrPreInterviewStates(t *testing.T) {
	m := mustDefault(t)
	m6, _ := Load(contracts.MustRead(contracts.StateMachinePath), 6)
	for _, mach := range []*Machine{m, m6} {
		for _, s := range mach.States() {
			info, _ := mach.StateInfo(s)
			if info.Kind == KindPostInterview || info.Kind == KindTerminal {
				continue
			}
			for _, tr := range mach.Transitions() {
				for _, a := range []Actor{ActorTier2, ActorReviewer} {
					if _, err := mach.Resolve(s, tr.Event, a, always(true)); err == nil {
						t.Errorf("%s performed %s from %s", a, tr.Event, s)
					}
				}
			}
		}
	}
}

func TestResumeIsDeterministicByPausedFrom(t *testing.T) {
	m := mustDefault(t)
	tr, err := m.Resolve(Paused, EvResume, ActorCandidate, guardsFrom(map[string]bool{"paused_from_question": true}))
	if err != nil || tr.To != QuestionActive || !tr.HasEffect("open_activation_same_item") {
		t.Fatalf("resume to question: %+v %v", tr, err)
	}
	tr, err = m.Resolve(Paused, EvResume, ActorCandidate, guardsFrom(map[string]bool{"paused_from_calibration": true}))
	if err != nil || tr.To != Calibration {
		t.Fatalf("resume to calibration: %+v %v", tr, err)
	}
	_, err = m.Resolve(Paused, EvResume, ActorCandidate, guardsFrom(nil))
	var te *TransitionError
	if !errors.As(err, &te) || !errors.Is(err, ErrGuardNotSatisfied) || len(te.FailedGuards) == 0 {
		t.Fatalf("resume without paused_from must fail with guard details, got %v", err)
	}
}

func TestChainSelectionFromAnswerProcessing(t *testing.T) {
	m := mustDefault(t)
	for guard, want := range map[string]State{
		"approved_follow_up_pending": FollowUp,
		"next_plan_item_exists":      QuestionActive,
		"plan_exhausted":             InterviewCompleted,
	} {
		tr, err := m.Chain(AnswerProcessing, guardsFrom(map[string]bool{guard: true}))
		if err != nil || tr.To != want {
			t.Errorf("%s: got %s %v, want %s", guard, tr.To, err, want)
		}
	}
	if _, err := m.Chain(AnswerProcessing, guardsFrom(map[string]bool{"plan_exhausted": true, "next_plan_item_exists": true})); !errors.Is(err, ErrAmbiguousTransition) {
		t.Errorf("overlapping guards must be reported as ambiguous, got %v", err)
	}
	if _, err := m.Chain(QuestionActive, always(true)); !errors.Is(err, ErrNoTransition) {
		t.Errorf("chain from a non-transient state must fail, got %v", err)
	}
	tr, err := m.Chain(FollowUp, always(true))
	if err != nil || tr.To != QuestionActive || !tr.HasEffect("open_activation_follow_up") {
		t.Errorf("follow-up activation: %+v %v", tr, err)
	}
}

func TestGuardErrorsPropagate(t *testing.T) {
	m := mustDefault(t)
	boom := errors.New("db down")
	_, err := m.Resolve(Ready, EvStart, ActorCandidate, func(string, Transition) (bool, error) { return false, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want guard error, got %v", err)
	}
}

func TestRequireGuards(t *testing.T) {
	m := mustDefault(t)
	if err := m.RequireGuards(map[string]bool{"plan_exhausted": true}); err == nil || !strings.Contains(err.Error(), "paused_from_question") {
		t.Fatalf("missing guards must be reported, got %v", err)
	}
	all := map[string]bool{}
	for _, g := range m.Guards() {
		all[g] = true
	}
	if err := m.RequireGuards(all); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsBrokenContracts(t *testing.T) {
	base := contracts.MustRead(contracts.StateMachinePath)
	mutate := func(fn func(c map[string]any)) []byte {
		var c map[string]any
		if err := json.Unmarshal(base, &c); err != nil {
			t.Fatal(err)
		}
		fn(c)
		b, _ := json.Marshal(c)
		return b
	}
	transitions := func(c map[string]any) []any { return c["transitions"].([]any) }
	cases := map[string][]byte{
		"unknown actor": mutate(func(c map[string]any) {
			transitions(c)[0].(map[string]any)["actors"] = []any{"llm"}
		}),
		"undefined guard": mutate(func(c map[string]any) {
			transitions(c)[0].(map[string]any)["guards"] = []any{"made_up"}
		}),
		"transient exit by candidate": mutate(func(c map[string]any) {
			for _, raw := range transitions(c) {
				tr := raw.(map[string]any)
				if tr["from"] == "ANSWER_PROCESSING" {
					tr["actors"] = []any{"candidate"}
				}
			}
		}),
		"terminal outgoing": mutate(func(c map[string]any) {
			c["transitions"] = append(transitions(c), map[string]any{"id": "T99", "from": "CANCELLED", "to": "READY", "event": "revive", "actors": []any{"system"}, "guards": []any{}, "effects": []any{}, "source": "ADR-012", "phase": 1})
		}),
		"duplicate id": mutate(func(c map[string]any) {
			c["transitions"] = append(transitions(c), transitions(c)[0])
		}),
	}
	for name, raw := range cases {
		if _, err := Load(raw, 1); err == nil {
			t.Errorf("%s: broken contract accepted", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Model-based random walk.
//
// A small world model provides guard answers and applies effects, the way the application
// does inside one transaction. Thousands of random (event, actor) sequences are driven through
// the engine and the contract invariants are checked after every step.

type world struct {
	state            State
	followUps        []int // follow-ups per top-level item
	item, follow     int   // current item; follow == -1 means the main question
	activationOpen   bool
	pausedFrom       State
	requestPending   bool
	requestResolved  bool
	segInActivation  bool
	calibrationSeg   bool
	consentOK        bool
	consentDecided   bool
	invitationValid  bool
	retention        bool
	pauseElapsed     bool
	finalized        map[[2]int]bool
	emittedCompleted bool
}

func (w *world) followPending() bool { return w.follow+1 < w.followUps[w.item] }

func (w *world) guard(g string, t Transition) (bool, error) {
	switch g {
	case "invitation_valid":
		return w.invitationValid, nil
	case "required_purposes_decided":
		return w.consentDecided && w.consentOK, nil
	case "any_required_purpose_declined":
		return w.consentDecided && !w.consentOK, nil
	case "notice_version_pinned":
		return w.consentDecided, nil
	case "retention_policy_configured":
		return w.retention, nil
	case "calibration_confirmed":
		return w.calibrationSeg, nil
	case "candidate_segment_in_open_activation":
		return w.activationOpen && w.segInActivation, nil
	case "completion_request_resolved":
		return w.requestPending && w.requestResolved, nil
	case "approved_follow_up_pending":
		return w.followPending(), nil
	case "next_plan_item_exists":
		return !w.followPending() && w.item+1 < len(w.followUps), nil
	case "plan_exhausted":
		return !w.followPending() && w.item+1 >= len(w.followUps), nil
	case "paused_from_question":
		return w.pausedFrom == QuestionActive || w.pausedFrom == CandidateSpeaking, nil
	case "paused_from_calibration":
		return w.pausedFrom == Calibration, nil
	case "pause_timeout_elapsed":
		return w.pauseElapsed, nil
	}
	return false, errors.New("model does not implement guard " + g + " for " + t.ID)
}

func (w *world) apply(t *testing.T, tr Transition) {
	for _, e := range tr.Effects {
		switch e {
		case "open_activation_first_item":
			w.item, w.follow = 0, -1
			w.open(t, tr)
		case "open_activation_follow_up":
			w.follow++
			w.open(t, tr)
		case "open_activation_next_item":
			w.item, w.follow = w.item+1, -1
			w.open(t, tr)
		case "open_activation_same_item":
			w.open(t, tr)
		case "close_activation":
			if !w.activationOpen {
				t.Fatalf("%s closes an activation that is not open", tr.ID)
			}
			w.activationOpen, w.segInActivation = false, false
		case "finalize_answer":
			k := [2]int{w.item, w.follow}
			if w.finalized[k] {
				t.Fatalf("INV-4 violated: item %v finalized twice", k)
			}
			w.finalized[k] = true
			w.requestPending, w.requestResolved = false, false
		case "record_paused_from":
			w.pausedFrom = tr.From
		case "cancel_completion_request":
			w.requestPending, w.requestResolved = false, false
		case "emit_interview_completed":
			w.emittedCompleted = true
		}
	}
	w.state = tr.To
}

func (w *world) open(t *testing.T, tr Transition) {
	if w.activationOpen {
		t.Fatalf("INV-1 violated: %s opens a second activation", tr.ID)
	}
	w.activationOpen = true
}

func (w *world) perturb(r *rand.Rand) {
	flip := func(p float64) bool { return r.Float64() < p }
	if flip(0.3) {
		w.segInActivation = w.activationOpen && !w.segInActivation
	}
	if flip(0.3) && w.state == CandidateSpeaking {
		w.requestPending = true
	}
	if flip(0.3) {
		w.requestResolved = !w.requestResolved
	}
	if flip(0.2) {
		w.calibrationSeg = !w.calibrationSeg
	}
	if flip(0.1) {
		w.consentDecided, w.consentOK = true, flip(0.7)
	}
	if flip(0.1) {
		w.invitationValid = !w.invitationValid
	}
	if flip(0.1) {
		w.retention = !w.retention
	}
	if flip(0.05) {
		w.pauseElapsed = !w.pauseElapsed
	}
}

func TestModelBasedRandomWalk(t *testing.T) {
	m := mustDefault(t)
	events := map[Event]bool{}
	for _, tr := range m.Transitions() {
		events[tr.Event] = true
	}
	var eventList []Event
	for e := range events {
		eventList = append(eventList, e)
	}
	slices.Sort(eventList)
	actors := []Actor{ActorCandidate, ActorTier1, ActorSystem, ActorTier2, ActorReviewer}
	terminating := map[Event]bool{EvWithdraw: true, EvConsentDeclined: true, EvPauseExpired: true}
	reachedCompleted, reachedCancelled := 0, 0

	// pick biases half of the steps toward events that exist from the current state (so long
	// interviews are explored) while the rest probe arbitrary, mostly invalid, combinations.
	pick := func(r *rand.Rand, w *world) (Event, Actor) {
		if r.Float64() < 0.5 {
			var local []Transition
			for _, tr := range m.Transitions() {
				if tr.From == w.state && (!terminating[tr.Event] || r.Float64() < 0.05) {
					local = append(local, tr)
				}
			}
			if len(local) > 0 {
				tr := local[r.IntN(len(local))]
				return tr.Event, tr.Actors[r.IntN(len(tr.Actors))]
			}
		}
		for {
			ev := eventList[r.IntN(len(eventList))]
			if !terminating[ev] || r.Float64() < 0.05 {
				return ev, actors[r.IntN(len(actors))]
			}
		}
	}

	for seed := uint64(1); seed <= 3000; seed++ {
		r := rand.New(rand.NewPCG(seed, seed*7919))
		w := &world{state: m.Initial, finalized: map[[2]int]bool{}, invitationValid: true, retention: true}
		for n := 1 + r.IntN(4); n > 0; n-- {
			w.followUps = append(w.followUps, r.IntN(3))
		}
		for step := 0; step < 2000; step++ {
			w.perturb(r)
			ev, actor := pick(r, w)
			before := w.state
			tr, err := m.Resolve(before, ev, actor, w.guard)
			if err != nil {
				var te *TransitionError
				if !errors.As(err, &te) {
					t.Fatalf("seed %d: unexpected error type %v", seed, err)
				}
				continue
			}
			checkTier1Subset(t, m, tr)
			if info, _ := m.StateInfo(before); (actor == ActorTier2 || actor == ActorReviewer) && info.Kind != KindPostInterview {
				t.Fatalf("seed %d: INV-6 violated by %s", seed, tr.ID)
			}
			w.apply(t, tr)
			for guard := 0; ; guard++ {
				info, _ := m.StateInfo(w.state)
				if !info.Transient {
					break
				}
				if guard > 5 {
					t.Fatalf("seed %d: chain did not terminate", seed)
				}
				next, err := m.Chain(w.state, w.guard)
				if err != nil {
					t.Fatalf("seed %d: chain from %s failed: %v", seed, w.state, err)
				}
				w.apply(t, next)
			}
			info, _ := m.StateInfo(w.state)
			if info.Transient {
				t.Fatalf("seed %d: INV-9 violated: resting in %s", seed, w.state)
			}
			if info.RequiresOpenActivation != w.activationOpen {
				t.Fatalf("seed %d: INV-1 activation mismatch in %s (open=%v) after %s", seed, w.state, w.activationOpen, tr.ID)
			}
			if w.state == Cancelled && w.emittedCompleted {
				t.Fatalf("seed %d: INV-10 violated", seed)
			}
			if info.Terminal {
				assertInert(t, m, w)
				break
			}
		}
		// From Phase 2 a completed interview continues into evaluation and review, so completion
		// is counted when it happened, not by the state the walk ends in.
		switch {
		case w.emittedCompleted:
			reachedCompleted++
			if len(w.finalized) != total(w.followUps) {
				t.Fatalf("seed %d: completed with %d/%d answers", seed, len(w.finalized), total(w.followUps))
			}
		case w.state == Cancelled:
			reachedCancelled++
		}
	}
	if reachedCompleted == 0 || reachedCancelled == 0 {
		t.Fatalf("walk did not explore both endings: completed=%d cancelled=%d", reachedCompleted, reachedCancelled)
	}
	t.Logf("random walk: %d completed, %d cancelled", reachedCompleted, reachedCancelled)
}

func total(followUps []int) int {
	n := 0
	for _, f := range followUps {
		n += 1 + f
	}
	return n
}

func checkTier1Subset(t *testing.T, m *Machine, tr Transition) {
	t.Helper()
	if !tr.Allows(ActorTier1) {
		return
	}
	if tr.Allows(ActorCandidate) {
		return
	}
	if req, ok := m.Request(tr.RequiresRequest); ok && slices.Contains(req.Actors, ActorCandidate) {
		return
	}
	t.Fatalf("INV-8 violated: tier1 can perform %s but the candidate cannot", tr.ID)
}

func assertInert(t *testing.T, m *Machine, w *world) {
	t.Helper()
	for _, tr := range m.Transitions() {
		for _, a := range []Actor{ActorCandidate, ActorTier1, ActorSystem, ActorTier2, ActorReviewer} {
			if _, err := m.Resolve(w.state, tr.Event, a, always(true)); err == nil {
				t.Fatalf("terminal state %s accepted %s", w.state, tr.Event)
			}
		}
	}
}

func FuzzResolve(f *testing.F) {
	m, err := Default()
	if err != nil {
		f.Fatal(err)
	}
	f.Add("QUESTION_ACTIVE", "pause", "candidate", uint8(0xff))
	f.Add("PAUSED", "resume", "tier1_agent", uint8(1))
	f.Add("", "", "", uint8(0))
	f.Fuzz(func(t *testing.T, from, event, actor string, guardBits uint8) {
		guards := m.Guards()
		tr, err := m.Resolve(State(from), Event(event), Actor(actor), func(g string, _ Transition) (bool, error) {
			i := slices.Index(guards, g)
			return guardBits&(1<<(uint(i)%8)) != 0, nil
		})
		if err != nil {
			return
		}
		got, ok := m.Transition(tr.ID)
		if !ok || got.From != State(from) || got.Event != Event(event) || !got.Allows(Actor(actor)) {
			t.Fatalf("resolved transition inconsistent with contract: %+v", tr)
		}
	})
}
