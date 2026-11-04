// Package flow is the deterministic interview flow engine. It loads the state-machine contract
// (contracts/state-machine/interview-session.v1.2.json) and resolves transitions. It has no I/O,
// no clock and no knowledge of evaluation data: the application supplies guard results and executes
// effects inside its own transaction.
package flow

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
)

// State, Event and Actor are the contract vocabulary.
type (
	State string
	Event string
	Actor string
	Kind  string
)

// Actors defined by the contract.
const (
	ActorCandidate Actor = "candidate"
	ActorTier1     Actor = "tier1_agent"
	ActorSystem    Actor = "system"
	ActorTier2     Actor = "tier2_pipeline"
	ActorReviewer  Actor = "reviewer"
)

// States defined by the contract.
const (
	Created            State = "CREATED"
	ConsentPending     State = "CONSENT_PENDING"
	Ready              State = "READY"
	Calibration        State = "CALIBRATION"
	QuestionActive     State = "QUESTION_ACTIVE"
	CandidateSpeaking  State = "CANDIDATE_SPEAKING"
	AnswerProcessing   State = "ANSWER_PROCESSING"
	FollowUp           State = "FOLLOW_UP"
	Paused             State = "PAUSED"
	InterviewCompleted State = "INTERVIEW_COMPLETED"
	EvaluationPending  State = "EVALUATION_PENDING"
	HumanReview        State = "HUMAN_REVIEW"
	ReportReady        State = "REPORT_READY"
	Cancelled          State = "CANCELLED"
)

// Events used by the application.
const (
	EvInvitationExchanged Event = "invitation_exchanged"
	EvConsentAccepted     Event = "consent_accepted"
	EvConsentDeclined     Event = "consent_declined"
	EvStart               Event = "start"
	EvCalibrationComplete Event = "calibration_complete"
	EvAnswerStarted       Event = "answer_started"
	EvAnswerFinalized     Event = "answer_finalized"
	EvFollowUpSelected    Event = "follow_up_selected"
	EvFollowUpActivated   Event = "follow_up_activated"
	EvNextQuestion        Event = "next_question"
	EvInterviewCompleted  Event = "interview_completed"
	EvPause               Event = "pause"
	EvResume              Event = "resume"
	EvWithdraw            Event = "withdraw"
	EvPauseExpired        Event = "pause_expired"
	EvEvaluationEnqueued  Event = "evaluation_enqueued"
	EvReviewRequired      Event = "review_required"
	EvReportReady         Event = "report_ready"
	EvReviewCompleted     Event = "review_completed"
)

// Kinds of states.
const (
	KindPreInterview  Kind = "pre_interview"
	KindLive          Kind = "live"
	KindPostInterview Kind = "post_interview"
	KindTerminal      Kind = "terminal"
)

// StateInfo describes one state.
type StateInfo struct {
	Name                   State  `json:"name"`
	Kind                   Kind   `json:"kind"`
	Terminal               bool   `json:"terminal"`
	Transient              bool   `json:"transient"`
	RequiresOpenActivation bool   `json:"requires_open_activation"`
	Description            string `json:"description"`
}

// Transition is one contract edge.
type Transition struct {
	ID              string   `json:"id"`
	From            State    `json:"from"`
	To              State    `json:"to"`
	Event           Event    `json:"event"`
	Actors          []Actor  `json:"actors"`
	RequiresRequest string   `json:"requires_request"`
	Guards          []string `json:"guards"`
	Effects         []string `json:"effects"`
	Source          string   `json:"source"`
	DiagramLabel    string   `json:"diagram_label"`
	Chain           bool     `json:"chain"`
	Phase           int      `json:"phase"`
}

// HasEffect reports whether the transition declares the effect.
func (t Transition) HasEffect(e string) bool { return slices.Contains(t.Effects, e) }

// Allows reports whether the actor may perform the transition.
func (t Transition) Allows(a Actor) bool { return slices.Contains(t.Actors, a) }

// Request is a contract request (R01).
type Request struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Actors     []Actor `json:"actors"`
	AllowedIn  []State `json:"allowed_in"`
	ResolvesTo string  `json:"resolves_to"`
}

type contractFile struct {
	ID          string            `json:"id"`
	Version     string            `json:"version"`
	Initial     State             `json:"initial_state"`
	Actors      map[string]string `json:"actors"`
	States      []StateInfo       `json:"states"`
	Guards      map[string]string `json:"guards"`
	Requests    []Request         `json:"requests"`
	Transitions []Transition      `json:"transitions"`
}

type edgeKey struct {
	from  State
	event Event
}

// Machine is an immutable, validated state machine.
type Machine struct {
	Version  string
	Initial  State
	MaxPhase int

	states   map[State]StateInfo
	actors   map[Actor]bool
	guards   map[string]string
	requests map[string]Request
	byID     map[string]Transition
	byEdge   map[edgeKey][]Transition
	chains   map[State][]Transition
	all      []Transition
}

// Load parses and validates a contract. maxPhase limits which transitions may execute;
// later-phase transitions exist in the contract but are refused until they are wired.
func Load(raw []byte, maxPhase int) (*Machine, error) {
	var c contractFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse state machine: %w", err)
	}
	m := &Machine{
		Version: c.Version, Initial: c.Initial, MaxPhase: maxPhase,
		states: map[State]StateInfo{}, actors: map[Actor]bool{}, guards: c.Guards,
		requests: map[string]Request{}, byID: map[string]Transition{},
		byEdge: map[edgeKey][]Transition{}, chains: map[State][]Transition{}, all: c.Transitions,
	}
	for a := range c.Actors {
		m.actors[Actor(a)] = true
	}
	for _, s := range c.States {
		if _, dup := m.states[s.Name]; dup {
			return nil, fmt.Errorf("duplicate state %s", s.Name)
		}
		m.states[s.Name] = s
	}
	if _, ok := m.states[c.Initial]; !ok {
		return nil, fmt.Errorf("initial state %s undefined", c.Initial)
	}
	for _, r := range c.Requests {
		m.requests[r.ID] = r
	}
	for _, t := range c.Transitions {
		if err := m.validateTransition(t); err != nil {
			return nil, err
		}
		m.byID[t.ID] = t
		k := edgeKey{t.From, t.Event}
		m.byEdge[k] = append(m.byEdge[k], t)
		if t.Chain {
			m.chains[t.From] = append(m.chains[t.From], t)
		}
	}
	for _, r := range c.Requests {
		if _, ok := m.byID[r.ResolvesTo]; !ok {
			return nil, fmt.Errorf("request %s resolves to unknown transition %s", r.ID, r.ResolvesTo)
		}
	}
	for name, s := range m.states {
		if s.Transient && len(m.chains[name]) == 0 {
			return nil, fmt.Errorf("transient state %s has no chain exit", name)
		}
	}
	return m, nil
}

func (m *Machine) validateTransition(t Transition) error {
	if _, dup := m.byID[t.ID]; dup {
		return fmt.Errorf("duplicate transition id %s", t.ID)
	}
	from, ok := m.states[t.From]
	if !ok {
		return fmt.Errorf("%s: unknown from-state %s", t.ID, t.From)
	}
	if _, ok := m.states[t.To]; !ok {
		return fmt.Errorf("%s: unknown to-state %s", t.ID, t.To)
	}
	if from.Terminal {
		return fmt.Errorf("%s: transition out of terminal state %s", t.ID, t.From)
	}
	for _, a := range t.Actors {
		if !m.actors[a] {
			return fmt.Errorf("%s: unknown actor %s", t.ID, a)
		}
	}
	for _, g := range t.Guards {
		if _, ok := m.guards[g]; !ok {
			return fmt.Errorf("%s: undefined guard %s", t.ID, g)
		}
	}
	if t.RequiresRequest != "" {
		if _, ok := m.requests[t.RequiresRequest]; !ok {
			return fmt.Errorf("%s: unknown request %s", t.ID, t.RequiresRequest)
		}
	}
	if from.Transient && (!t.Chain || len(t.Actors) != 1 || t.Actors[0] != ActorSystem) {
		return fmt.Errorf("%s: transient state %s may only be left by a system chain step", t.ID, t.From)
	}
	return nil
}

// ImplementedPhase is the highest roadmap phase whose transitions the control plane executes:
// Phase 2 wires T14 (evaluation enqueued), T15 (human review required) and, from contract v1.2,
// T17 (review completed). T16 (automatic report) belongs to Phase 2 but no operation fires it:
// every evaluation is human-reviewed (ADR-022).
const ImplementedPhase = 2

// Default loads the embedded contract for the implemented phases.
func Default() (*Machine, error) {
	raw, err := contracts.Read(contracts.StateMachinePath)
	if err != nil {
		return nil, err
	}
	return Load(raw, ImplementedPhase)
}

// StateInfo returns the description of a state.
func (m *Machine) StateInfo(s State) (StateInfo, bool) {
	info, ok := m.states[s]
	return info, ok
}

// States returns every state name, sorted.
func (m *Machine) States() []State {
	out := make([]State, 0, len(m.states))
	for s := range m.states {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Transitions returns a copy of every transition in contract order.
func (m *Machine) Transitions() []Transition { return slices.Clone(m.all) }

// Transition returns a transition by id.
func (m *Machine) Transition(id string) (Transition, bool) {
	t, ok := m.byID[id]
	return t, ok
}

// Request returns a request by id.
func (m *Machine) Request(id string) (Request, bool) {
	r, ok := m.requests[id]
	return r, ok
}

// Guards returns the contract guard names, sorted.
func (m *Machine) Guards() []string {
	out := make([]string, 0, len(m.guards))
	for g := range m.guards {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// RequireGuards fails unless the application implements every guard the contract names.
func (m *Machine) RequireGuards(implemented map[string]bool) error {
	var missing []string
	for _, g := range m.Guards() {
		if !implemented[g] {
			missing = append(missing, g)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("guards not implemented: %v", missing)
	}
	return nil
}

// GuardFunc evaluates a named guard for the transition being resolved.
type GuardFunc func(guard string, t Transition) (bool, error)

// Error codes returned by Resolve and Chain.
var (
	ErrUnknownState        = errors.New("unknown state")
	ErrNoTransition        = errors.New("no such transition from this state")
	ErrPhaseNotWired       = errors.New("transition belongs to a later phase")
	ErrActorNotPermitted   = errors.New("actor not permitted")
	ErrGuardNotSatisfied   = errors.New("guard not satisfied")
	ErrAmbiguousTransition = errors.New("more than one transition matched")
)

// TransitionError carries the details of a refused transition.
type TransitionError struct {
	Err          error
	From         State
	Event        Event
	Actor        Actor
	FailedGuards []string
}

func (e *TransitionError) Error() string {
	if len(e.FailedGuards) > 0 {
		return fmt.Sprintf("%v: %s --%s--> (actor %s), failed guards %v", e.Err, e.From, e.Event, e.Actor, e.FailedGuards)
	}
	return fmt.Sprintf("%v: %s --%s--> (actor %s)", e.Err, e.From, e.Event, e.Actor)
}

func (e *TransitionError) Unwrap() error { return e.Err }

// Resolve returns the single transition for (from, event) that the actor may perform and whose
// guards are all satisfied.
func (m *Machine) Resolve(from State, event Event, actor Actor, guard GuardFunc) (Transition, error) {
	fail := func(err error, failed []string) (Transition, error) {
		return Transition{}, &TransitionError{Err: err, From: from, Event: event, Actor: actor, FailedGuards: failed}
	}
	if _, ok := m.states[from]; !ok {
		return fail(ErrUnknownState, nil)
	}
	candidates := m.byEdge[edgeKey{from, event}]
	if len(candidates) == 0 {
		return fail(ErrNoTransition, nil)
	}
	var wired []Transition
	for _, t := range candidates {
		if t.Phase <= m.MaxPhase {
			wired = append(wired, t)
		}
	}
	if len(wired) == 0 {
		return fail(ErrPhaseNotWired, nil)
	}
	var permitted []Transition
	for _, t := range wired {
		if t.Allows(actor) {
			permitted = append(permitted, t)
		}
	}
	if len(permitted) == 0 {
		return fail(ErrActorNotPermitted, nil)
	}
	return m.pick(permitted, guard, fail)
}

// Chain returns the system chain step out of a transient state.
func (m *Machine) Chain(from State, guard GuardFunc) (Transition, error) {
	fail := func(err error, failed []string) (Transition, error) {
		return Transition{}, &TransitionError{Err: err, From: from, Actor: ActorSystem, FailedGuards: failed}
	}
	info, ok := m.states[from]
	if !ok {
		return fail(ErrUnknownState, nil)
	}
	if !info.Transient {
		return fail(ErrNoTransition, nil)
	}
	return m.pick(m.chains[from], guard, fail)
}

func (m *Machine) pick(candidates []Transition, guard GuardFunc, fail func(error, []string) (Transition, error)) (Transition, error) {
	var matched []Transition
	var failed []string
	for _, t := range candidates {
		ok := true
		for _, g := range t.Guards {
			pass, err := guard(g, t)
			if err != nil {
				return Transition{}, fmt.Errorf("guard %s: %w", g, err)
			}
			if !pass {
				ok = false
				failed = append(failed, g)
				break
			}
		}
		if ok {
			matched = append(matched, t)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return fail(ErrGuardNotSatisfied, failed)
	default:
		return fail(ErrAmbiguousTransition, nil)
	}
}
