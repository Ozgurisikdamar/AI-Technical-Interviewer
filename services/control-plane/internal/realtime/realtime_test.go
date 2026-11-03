package realtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/livekit/protocol/auth"
)

func TestIsNotFound(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("twirp error not_found: requested room does not exist"), true},
		{errors.New("room not found"), true},
		{errors.New("twirp error unavailable: connection refused"), false},
	} {
		if got := isNotFound(tc.err); got != tc.want {
			t.Errorf("isNotFound(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

const (
	testAPIKey        = "test-only-api-key"
	testAPISecret     = "test-only-api-secret-0123456789abcdef"
	testWebhookKey    = "test-only-webhook-key"
	testWebhookSecret = "test-only-webhook-secret-0123456789abcdef"
)

func testLiveKit() *LiveKit {
	return NewLiveKit(Options{
		PublicURL: "ws://127.0.0.1:7880", APIURL: "http://127.0.0.1:7880",
		APIKey: testAPIKey, APISecret: testAPISecret,
		WebhookKey: testWebhookKey, WebhookSecret: testWebhookSecret,
		AgentName: "ati-interviewer", CandidateTTL: 10 * time.Minute,
	})
}

// decode verifies the token with the API secret and returns its claims.
func decode(t *testing.T, token string) (*auth.ClaimGrants, time.Time) {
	t.Helper()
	v, err := auth.ParseAPIToken(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v.APIKey() != testAPIKey {
		t.Fatalf("issuer = %q", v.APIKey())
	}
	reg, grants, err := v.Verify(testAPISecret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, _, err := v.Verify("some-other-secret-0123456789abcdef"); err == nil {
		t.Fatal("token verified with a foreign secret")
	}
	return grants, reg.ExpiresAt.Time
}

// The candidate's grant is what LiveKit enforces server-side (ADR-016, ADR-018): it must not
// depend on the browser behaving.
func TestCandidateTokenGrants(t *testing.T) {
	lk := testLiveKit()
	for _, voice := range []bool{true, false} {
		before := time.Now()
		token, expires, err := lk.CandidateToken("room_abc", voice)
		if err != nil {
			t.Fatal(err)
		}
		g, exp := decode(t, token)
		if g.Identity != CandidateIdentity {
			t.Errorf("identity = %q", g.Identity)
		}
		v := g.Video
		if v == nil || !v.RoomJoin || v.Room != "room_abc" {
			t.Fatalf("voice=%v: room grant %+v", voice, v)
		}
		if v.RoomCreate || v.RoomAdmin || v.RoomList || v.RoomRecord || v.Recorder || v.Hidden || v.Agent || v.IngressAdmin {
			t.Errorf("voice=%v: privileged grant %+v", voice, v)
		}
		if v.GetCanPublishData() || v.GetCanUpdateOwnMetadata() || !v.GetCanSubscribe() {
			t.Errorf("voice=%v: data=%v metadata=%v subscribe=%v", voice, v.GetCanPublishData(), v.GetCanUpdateOwnMetadata(), v.GetCanSubscribe())
		}
		if voice {
			if !v.GetCanPublish() || !slices.Equal(v.CanPublishSources, []string{"microphone"}) {
				t.Errorf("voice: publish=%v sources=%v", v.GetCanPublish(), v.CanPublishSources)
			}
		} else if v.GetCanPublish() || len(v.CanPublishSources) != 0 {
			t.Errorf("text mode may not publish: publish=%v sources=%v", v.GetCanPublish(), v.CanPublishSources)
		}
		for _, ts := range []time.Time{exp, expires} {
			if d := ts.Sub(before); d < 9*time.Minute || d > 11*time.Minute {
				t.Errorf("voice=%v: lifetime %v, want about 10 minutes", voice, d)
			}
		}
	}
}

func signedWebhook(t *testing.T, body []byte, key, secret string, tamper bool) *http.Request {
	t.Helper()
	jwt := ""
	if key != "" {
		sum := sha256.Sum256(body)
		var err error
		if jwt, err = auth.NewAccessToken(key, secret).SetValidFor(time.Minute).SetSha256(base64.StdEncoding.EncodeToString(sum[:])).ToJWT(); err != nil {
			t.Fatal(err)
		}
	}
	if tamper {
		body = bytes.Replace(body, []byte("room_abc"), []byte("room_xyz"), 1)
	}
	r := httptest.NewRequest(http.MethodPost, "/internal/v1/livekit/webhook", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/webhook+json")
	if jwt != "" {
		r.Header.Set("Authorization", jwt)
	}
	return r
}

// Only events signed with the dedicated webhook key are accepted (ADR-016): any holder of the
// LiveKit API key (agent workers included) must not be able to forge one.
func TestReceiveWebhookAcceptsOnlyTheDedicatedKey(t *testing.T) {
	lk := testLiveKit()
	body := []byte(`{"id":"EV_1","event":"participant_joined","createdAt":"1700000000","room":{"name":"room_abc"},"participant":{"identity":"candidate"}}`)

	w, err := lk.ReceiveWebhook(signedWebhook(t, body, testWebhookKey, testWebhookSecret, false))
	if err != nil {
		t.Fatalf("dedicated key rejected: %v", err)
	}
	if w.ID != "EV_1" || w.Event != "participant_joined" || w.Room != "room_abc" || w.ParticipantIdentity != "candidate" {
		t.Errorf("parsed %+v", w)
	}

	for name, r := range map[string]*http.Request{
		"api key":       signedWebhook(t, body, testAPIKey, testAPISecret, false),
		"wrong secret":  signedWebhook(t, body, testWebhookKey, "not-the-webhook-secret-0123456789abcdef", false),
		"unsigned":      signedWebhook(t, body, "", "", false),
		"tampered body": signedWebhook(t, body, testWebhookKey, testWebhookSecret, true),
		"garbage header": func() *http.Request {
			r := signedWebhook(t, body, "", "", false)
			r.Header.Set("Authorization", "Bearer nope")
			return r
		}(),
	} {
		if _, err := lk.ReceiveWebhook(r); err == nil {
			t.Errorf("%s: webhook accepted", name)
		}
	}
}
