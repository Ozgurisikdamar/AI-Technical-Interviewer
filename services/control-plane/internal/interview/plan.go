package interview

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts/schema"
)

// Item is one plan item (question or unconditional follow-up) in flattened order.
type Item struct {
	ID            string
	ParentID      string // set for follow-ups
	Ordinal       int
	Kind          string // question | follow_up
	Text          string
	Modality      string
	TurnProfile   string
	RubricID      *string
	RubricVersion *int
}

// Plan is an immutable published plan.
type Plan struct {
	ID            string
	Version       int
	Language      string
	SeniorityBand string
	Synthetic     bool
	// Title is the plan's display title; the candidate sees it as the interview's name (ADR-024).
	Title string
	Items []Item // ordered by ordinal
	index map[string]int
}

// First returns the first item.
func (p *Plan) First() *Item { return &p.Items[0] }

// Item returns an item by id.
func (p *Plan) Item(id string) (*Item, bool) {
	i, ok := p.index[id]
	if !ok {
		return nil, false
	}
	return &p.Items[i], true
}

// Next returns the item after id in plan order, or nil.
func (p *Plan) Next(id string) *Item {
	i, ok := p.index[id]
	if !ok || i+1 >= len(p.Items) {
		return nil
	}
	return &p.Items[i+1]
}

// root returns the top-level question an item belongs to.
func (p *Plan) root(id string) string {
	if it, ok := p.Item(id); ok && it.ParentID != "" {
		return it.ParentID
	}
	return id
}

// FollowUpPending reports whether the next item is an unconditional follow-up of the same question.
func (p *Plan) FollowUpPending(finalized string) bool {
	n := p.Next(finalized)
	return n != nil && n.Kind == "follow_up" && n.ParentID == p.root(finalized)
}

// NextQuestion reports whether the next item is a new top-level question.
func (p *Plan) NextQuestion(finalized string) bool {
	n := p.Next(finalized)
	return n != nil && n.Kind == "question"
}

// Exhausted reports whether no item follows.
func (p *Plan) Exhausted(finalized string) bool { return p.Next(finalized) == nil }

type planKey struct {
	id      string
	version int
}

type planCache struct{ m sync.Map }

func newPlanCache() *planCache { return &planCache{} }

// load reads a plan once; plans are immutable so caching is safe.
func (c *planCache) load(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, version int) (*Plan, error) {
	if v, ok := c.m.Load(planKey{id, version}); ok {
		return v.(*Plan), nil
	}
	p := &Plan{ID: id, Version: version, index: map[string]int{}}
	err := q.QueryRow(ctx, `SELECT language, seniority_band, synthetic, coalesce(content->>'title', '') FROM ati.interview_plans WHERE plan_id = $1 AND version = $2`, id, version).
		Scan(&p.Language, &p.SeniorityBand, &p.Synthetic, &p.Title)
	if err == pgx.ErrNoRows {
		return nil, ErrPlanUnavailable
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT item_id, coalesce(parent_item_id,''), ordinal, kind, text, modality, turn_profile, rubric_id, rubric_version
		FROM ati.interview_questions WHERE plan_id = $1 AND plan_version = $2 ORDER BY ordinal`, id, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.ParentID, &it.Ordinal, &it.Kind, &it.Text, &it.Modality, &it.TurnProfile, &it.RubricID, &it.RubricVersion); err != nil {
			return nil, err
		}
		p.index[it.ID] = len(p.Items)
		p.Items = append(p.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(p.Items) == 0 {
		return nil, ErrPlanUnavailable
	}
	c.m.Store(planKey{id, version}, p)
	return p, nil
}

// Purpose is one notice purpose.
type Purpose struct {
	PurposeID         string   `json:"purpose_id"`
	Required          bool     `json:"required"`
	Basis             string   `json:"basis"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	DataCategories    []string `json:"data_categories"`
	RetentionCategory string   `json:"retention_category"`
}

// Notice is a versioned candidate notice (contracts/content/notices).
type Notice struct {
	NoticeID string `json:"notice_id"`
	Version  string `json:"version"`
	Status   string `json:"status"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Sections []struct {
		Heading string `json:"heading"`
		Text    string `json:"text"`
	} `json:"sections"`
	Purposes []Purpose       `json:"purposes"`
	Raw      json.RawMessage `json:"-"`
}

// Notices indexes the embedded notices.
type Notices struct{ byKey map[string]*Notice }

func noticeKey(id, version, lang string) string { return id + "@" + version + "/" + lang }

// LoadNotices loads every embedded notice and validates it against the notice contract.
func LoadNotices() (*Notices, error) {
	files, err := contracts.ListDir(contracts.NoticesDir)
	if err != nil {
		return nil, err
	}
	n := &Notices{byKey: map[string]*Notice{}}
	for _, f := range files {
		raw := contracts.MustRead(f)
		if err := schema.ValidateJSON(contracts.ConsentNoticePath, raw); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		var notice Notice
		if err := json.Unmarshal(raw, &notice); err != nil {
			return nil, err
		}
		notice.Raw = raw
		n.byKey[noticeKey(notice.NoticeID, notice.Version, notice.Language)] = &notice
	}
	return n, nil
}

// Get returns a notice.
func (n *Notices) Get(id, version, lang string) (*Notice, bool) {
	v, ok := n.byKey[noticeKey(id, version, lang)]
	return v, ok
}
