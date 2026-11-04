// Package outbox publishes the transactional outbox to Redis Streams for Tier-2 (ADR-006, ADR-015,
// ADR-022). Events are published in seq order; for interview.completed the relay first applies T14
// (the evaluation is enqueued), so a consumer never sees a completed interview that is not pending
// evaluation. A crash between the steps publishes an event again: consumers are idempotent.
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Stream is the Redis stream Tier-2 consumes.
const Stream = "ati:tier2:events"

// Publisher appends one entry to a stream.
type Publisher interface {
	Publish(ctx context.Context, stream string, fields map[string]any) error
}

// Enqueuer applies T14 for a session whose interview.completed event is about to be published.
type Enqueuer interface {
	EnqueueEvaluation(ctx context.Context, sessionID string) error
}

// Relay moves unpublished outbox events to the stream.
type Relay struct {
	pool  *pgxpool.Pool
	pub   Publisher
	svc   Enqueuer
	log   *slog.Logger
	batch int
	now   func() time.Time
}

// NewRelay returns a relay; now is the clock (nil: time.Now).
func NewRelay(pool *pgxpool.Pool, pub Publisher, svc Enqueuer, log *slog.Logger, now func() time.Time) *Relay {
	if now == nil {
		now = time.Now
	}
	return &Relay{pool: pool, pub: pub, svc: svc, log: log, batch: 100, now: now}
}

type event struct {
	id, sessionID, tenantID, eventType string
	seq                                int64
	payload                            []byte
}

// RunOnce publishes up to one batch of events in seq order and reports how many were published.
// It stops at the first failure so that order is preserved; the next pass resumes there.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, seq, session_id, tenant_id, event_type, payload FROM ati.outbox_events
		WHERE published_at IS NULL ORDER BY seq LIMIT $1`, r.batch)
	if err != nil {
		return 0, err
	}
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.id, &e.seq, &e.sessionID, &e.tenantID, &e.eventType, &e.payload); err != nil {
			rows.Close()
			return 0, err
		}
		events = append(events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	published := 0
	for _, e := range events {
		if e.eventType == "interview.completed" {
			if err := r.svc.EnqueueEvaluation(ctx, e.sessionID); err != nil {
				return published, fmt.Errorf("enqueue evaluation for %s: %w", e.sessionID, err)
			}
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := r.pub.Publish(call, Stream, map[string]any{
			"event_id": e.id, "event_type": e.eventType, "session_id": e.sessionID, "tenant_id": e.tenantID, "payload": string(e.payload),
		})
		cancel()
		if err != nil {
			return published, fmt.Errorf("publish %s: %w", e.id, err)
		}
		if _, err := r.pool.Exec(ctx, `UPDATE ati.outbox_events SET published_at = $2 WHERE id = $1 AND published_at IS NULL`, e.id, r.now().UTC()); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}

// RedisPublisher publishes with XADD and trims the stream approximately to MaxLen entries
// (entries carry identifiers only; consumers acknowledge them, ADR-006).
type RedisPublisher struct {
	Client *redis.Client
	MaxLen int64
}

// NewRedisPublisher connects to a redis:// or rediss:// URL.
func NewRedisPublisher(url string) (*RedisPublisher, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("ATI_REDIS_URL: %w", err)
	}
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = 3*time.Second, 3*time.Second, 3*time.Second
	return &RedisPublisher{Client: redis.NewClient(opt), MaxLen: 100_000}, nil
}

func (p *RedisPublisher) Publish(ctx context.Context, stream string, fields map[string]any) error {
	return p.Client.XAdd(ctx, &redis.XAddArgs{Stream: stream, MaxLen: p.MaxLen, Approx: true, Values: fields}).Err()
}

// Close closes the connection pool.
func (p *RedisPublisher) Close() error { return p.Client.Close() }
