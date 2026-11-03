package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Fake is an in-memory Media for tests. Webhooks are read from the body without signatures, so it
// must never be used outside tests.
type Fake struct {
	mu           sync.Mutex
	Rooms        map[string]bool
	Deleted      []string
	Dispatches   map[string][]Dispatch
	Participants map[string][]Participant
	Removed      []string
	Tokens       []FakeToken
	seq          int
	FailEnsure   error
}

// FakeToken records an issued candidate token.
type FakeToken struct {
	Room            string
	CanPublishAudio bool
}

// NewFake returns an empty fake.
func NewFake() *Fake {
	return &Fake{Rooms: map[string]bool{}, Dispatches: map[string][]Dispatch{}, Participants: map[string][]Participant{}}
}

func (f *Fake) PublicURL() string { return "ws://fake-livekit" }

func (f *Fake) EnsureRoom(_ context.Context, room string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailEnsure != nil {
		return f.FailEnsure
	}
	f.Rooms[room] = true
	return nil
}

func (f *Fake) DeleteRoom(_ context.Context, room string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Rooms, room)
	f.Deleted = append(f.Deleted, room)
	return nil
}

func (f *Fake) CandidateToken(room string, canPublishAudio bool) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Tokens = append(f.Tokens, FakeToken{Room: room, CanPublishAudio: canPublishAudio})
	return fmt.Sprintf("fake-token-%d", len(f.Tokens)), time.Now().Add(10 * time.Minute), nil
}

func (f *Fake) Dispatch(_ context.Context, room, metadata string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("AD_fake%d", f.seq)
	f.Dispatches[room] = append(f.Dispatches[room], Dispatch{ID: id, AgentName: "ati-tier1-interviewer", Metadata: metadata})
	return id, nil
}

func (f *Fake) ListDispatches(_ context.Context, room string) ([]Dispatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Dispatch(nil), f.Dispatches[room]...), nil
}

func (f *Fake) ListParticipants(_ context.Context, room string) ([]Participant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Participant(nil), f.Participants[room]...), nil
}

func (f *Fake) RemoveParticipant(_ context.Context, room, identity string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []Participant
	for _, p := range f.Participants[room] {
		if p.Identity != identity {
			keep = append(keep, p)
		}
	}
	f.Participants[room] = keep
	f.Removed = append(f.Removed, identity)
	return nil
}

// Join simulates a participant joining.
func (f *Fake) Join(room, identity string, agent bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Participants[room] = append(f.Participants[room], Participant{Identity: identity, Agent: agent})
}

// PublishMic simulates the candidate publishing (or, with "", unpublishing) a microphone track.
func (f *Fake) PublishMic(room, identity, trackSID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, p := range f.Participants[room] {
		if p.Identity == identity {
			f.Participants[room][i].MicTrackSID = trackSID
		}
	}
}

// Leave simulates a participant leaving.
func (f *Fake) Leave(room, identity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []Participant
	for _, p := range f.Participants[room] {
		if p.Identity != identity {
			keep = append(keep, p)
		}
	}
	f.Participants[room] = keep
}

// ReceiveWebhook decodes a Webhook from the body (no signature: tests only).
func (f *Fake) ReceiveWebhook(r *http.Request) (*Webhook, error) {
	if r.Header.Get("Authorization") != "fake-valid-signature" {
		return nil, fmt.Errorf("webhook rejected: bad signature")
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var w Webhook
	return &w, json.Unmarshal(b, &w)
}

var _ Media = (*Fake)(nil)
var _ Media = (*LiveKit)(nil)

// FakeEgress records egress requests in memory (tests only).
type FakeEgress struct {
	mu      sync.Mutex
	Started []FakeEgressStart
	Stopped []string
	seq     int
	Fail    error
	// Hang makes StartTrackEgress block until its context ends, like LiveKit when no egress
	// service is connected (the request is never answered).
	Hang bool
}

// FakeEgressStart is one recorded StartTrackEgress call.
type FakeEgressStart struct {
	EgressID, Room, TrackSID, Filepath string
}

func (f *FakeEgress) StartTrackEgress(ctx context.Context, room, trackSID, filepath string) (string, error) {
	f.mu.Lock()
	hang := f.Hang
	f.mu.Unlock()
	if hang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return "", f.Fail
	}
	f.seq++
	id := fmt.Sprintf("EG_fake%04d", f.seq)
	f.Started = append(f.Started, FakeEgressStart{EgressID: id, Room: room, TrackSID: trackSID, Filepath: filepath})
	return id, nil
}

func (f *FakeEgress) StopEgress(_ context.Context, egressID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Stopped = append(f.Stopped, egressID)
	return nil
}

// Snapshot returns copies of the recorded calls.
func (f *FakeEgress) Snapshot() ([]FakeEgressStart, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeEgressStart(nil), f.Started...), append([]string(nil), f.Stopped...)
}
