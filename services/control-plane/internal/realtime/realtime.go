// Package realtime integrates the LiveKit media layer behind an interface (ADR-003, ADR-016).
package realtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// CandidateIdentity is the fixed LiveKit identity of the candidate in every room. A second
// connection with the same identity replaces the first (refresh/reconnect semantics).
const CandidateIdentity = "candidate"

// Participant is a room member.
type Participant struct {
	Identity string
	Agent    bool
}

// Dispatch is an agent dispatch.
type Dispatch struct {
	ID        string
	AgentName string
	Metadata  string
}

// Webhook is a verified LiveKit webhook event (only the fields the control plane uses).
type Webhook struct {
	ID                  string
	Event               string
	CreatedAt           int64
	Room                string
	ParticipantIdentity string
	ParticipantAgent    bool
	TrackSID            string
	TrackSource         string
	EgressID            string
	EgressStatus        string
	EgressFiles         []EgressFile
}

// EgressFile is a finished egress output.
type EgressFile struct {
	Location  string
	Filename  string
	Size      int64
	StartedAt int64
	EndedAt   int64
}

// Media is everything the control plane needs from the media layer.
type Media interface {
	PublicURL() string
	EnsureRoom(ctx context.Context, room string) error
	DeleteRoom(ctx context.Context, room string) error
	CandidateToken(room string, canPublishAudio bool) (string, time.Time, error)
	Dispatch(ctx context.Context, room, metadata string) (string, error)
	ListDispatches(ctx context.Context, room string) ([]Dispatch, error)
	ListParticipants(ctx context.Context, room string) ([]Participant, error)
	RemoveParticipant(ctx context.Context, room, identity string) error
	ReceiveWebhook(r *http.Request) (*Webhook, error)
}

// Options configure the LiveKit implementation.
type Options struct {
	PublicURL        string // ws/wss URL for clients
	APIURL           string // http/https URL for the server API
	APIKey           string
	APISecret        string
	WebhookKey       string // dedicated key: only events signed with it are accepted
	WebhookSecret    string
	AgentName        string
	CandidateTTL     time.Duration
	DepartureTimeout time.Duration
}

// LiveKit is the production Media implementation.
type LiveKit struct {
	opts     Options
	rooms    *lksdk.RoomServiceClient
	dispatch *lksdk.AgentDispatchClient
	webhooks auth.KeyProvider
}

// NewLiveKit builds the LiveKit client.
func NewLiveKit(o Options) *LiveKit {
	return &LiveKit{
		opts:     o,
		rooms:    lksdk.NewRoomServiceClient(o.APIURL, o.APIKey, o.APISecret),
		dispatch: lksdk.NewAgentDispatchServiceClient(o.APIURL, o.APIKey, o.APISecret),
		webhooks: auth.NewSimpleKeyProvider(o.WebhookKey, o.WebhookSecret),
	}
}

// PublicURL is the URL handed to browsers.
func (l *LiveKit) PublicURL() string { return l.opts.PublicURL }

// EnsureRoom creates the room if needed (rooms are never auto-created by clients).
func (l *LiveKit) EnsureRoom(ctx context.Context, room string) error {
	_, err := l.rooms.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:             room,
		EmptyTimeout:     300,
		DepartureTimeout: uint32(l.opts.DepartureTimeout / time.Second),
		MaxParticipants:  3, // candidate, agent, and slack for an agent hand-over
	})
	return err
}

// DeleteRoom removes the room; connected tokens stop working (ADR-016).
func (l *LiveKit) DeleteRoom(ctx context.Context, room string) error {
	_, err := l.rooms.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: room})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil
	}
	return err
}

// CandidateToken issues a short-lived token: microphone-only publishing (none in text mode),
// subscribing allowed, no data channel, no metadata updates.
func (l *LiveKit) CandidateToken(room string, canPublishAudio bool) (string, time.Time, error) {
	f, t := false, true
	grant := &auth.VideoGrant{
		RoomJoin:             true,
		Room:                 room,
		CanPublish:           &canPublishAudio,
		CanSubscribe:         &t,
		CanPublishData:       &f,
		CanUpdateOwnMetadata: &f,
	}
	if canPublishAudio {
		grant.CanPublishSources = []string{"microphone"}
	}
	expires := time.Now().Add(l.opts.CandidateTTL)
	jwt, err := auth.NewAccessToken(l.opts.APIKey, l.opts.APISecret).
		SetIdentity(CandidateIdentity).
		SetValidFor(l.opts.CandidateTTL).
		SetVideoGrant(grant).
		ToJWT()
	return jwt, expires, err
}

// Dispatch creates an explicit agent dispatch. Metadata must not contain secrets (ADR-016).
func (l *LiveKit) Dispatch(ctx context.Context, room, metadata string) (string, error) {
	d, err := l.dispatch.CreateDispatch(ctx, &livekit.CreateAgentDispatchRequest{AgentName: l.opts.AgentName, Room: room, Metadata: metadata})
	if err != nil {
		return "", err
	}
	return d.Id, nil
}

// ListDispatches lists dispatches in a room.
func (l *LiveKit) ListDispatches(ctx context.Context, room string) ([]Dispatch, error) {
	res, err := l.dispatch.ListDispatch(ctx, &livekit.ListAgentDispatchRequest{Room: room})
	if err != nil {
		return nil, err
	}
	out := make([]Dispatch, 0, len(res.AgentDispatches))
	for _, d := range res.AgentDispatches {
		out = append(out, Dispatch{ID: d.Id, AgentName: d.AgentName, Metadata: d.Metadata})
	}
	return out, nil
}

// ListParticipants lists room members.
func (l *LiveKit) ListParticipants(ctx context.Context, room string) ([]Participant, error) {
	res, err := l.rooms.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: room})
	if err != nil {
		return nil, err
	}
	out := make([]Participant, 0, len(res.Participants))
	for _, p := range res.Participants {
		out = append(out, Participant{Identity: p.Identity, Agent: p.Kind == livekit.ParticipantInfo_AGENT})
	}
	return out, nil
}

// RemoveParticipant removes a member (stale agents after re-dispatch).
func (l *LiveKit) RemoveParticipant(ctx context.Context, room, identity string) error {
	_, err := l.rooms.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{Room: room, Identity: identity})
	return err
}

// ReceiveWebhook verifies the signature with the dedicated webhook key only.
func (l *LiveKit) ReceiveWebhook(r *http.Request) (*Webhook, error) {
	ev, err := webhook.ReceiveWebhookEvent(r, l.webhooks)
	if err != nil {
		return nil, fmt.Errorf("webhook rejected: %w", err)
	}
	return FromEvent(ev), nil
}

// FromEvent converts a protocol event.
func FromEvent(ev *livekit.WebhookEvent) *Webhook {
	w := &Webhook{ID: ev.Id, Event: ev.Event, CreatedAt: ev.CreatedAt}
	if ev.Room != nil {
		w.Room = ev.Room.Name
	}
	if ev.Participant != nil {
		w.ParticipantIdentity = ev.Participant.Identity
		w.ParticipantAgent = ev.Participant.Kind == livekit.ParticipantInfo_AGENT
	}
	if ev.Track != nil {
		w.TrackSID = ev.Track.Sid
		w.TrackSource = strings.ToLower(ev.Track.Source.String())
	}
	if ev.EgressInfo != nil {
		w.EgressID = ev.EgressInfo.EgressId
		w.EgressStatus = ev.EgressInfo.Status.String()
		for _, f := range ev.EgressInfo.FileResults {
			w.EgressFiles = append(w.EgressFiles, EgressFile{Location: f.Location, Filename: f.Filename, Size: f.Size, StartedAt: f.StartedAt, EndedAt: f.EndedAt})
		}
	}
	return w
}

// ErrUnavailable wraps media-layer failures surfaced to callers.
var ErrUnavailable = errors.New("media layer unavailable")
