package donations

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func readObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	return integerObject(result).(map[string]any), nil
}
func integerObject(value any) any {
	switch value := value.(type) {
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return n
		}
		return value.String()
	case map[string]any:
		for key, item := range value {
			value[key] = integerObject(item)
		}
	case []any:
		for i, item := range value {
			value[i] = integerObject(item)
		}
	}
	return value
}

// Ledger exposes only the reviewed public aggregates and staff-authored prose.
// No query includes a donor identity or activity/membership count.
func (s *Service) Ledger(ctx context.Context, currency string) (map[string]any, error) {
	anchorLimit := 6
	if s.Config.CostAnchorsMax != nil {
		anchorLimit = max(0, *s.Config.CostAnchorsMax)
	}
	result := map[string]any{"currency": currency}
	var received, spent int64
	if err := s.DB.QueryRow(ctx, `SELECT coalesce((SELECT sum(amount_cents) FROM donations_donation WHERE status='completed' AND currency=$1),0),coalesce((SELECT sum(amount_cents) FROM donations_spendentry WHERE currency=$1),0)`, currency).Scan(&received, &spent); err != nil {
		return nil, err
	}
	result["total_cents"] = received
	result["spent_cents"] = spent
	statements := []struct {
		key, query string
		args       []any
	}{
		{"campaigns", `SELECT jsonb_build_object('title',c.title,'slug',c.slug,'description',c.description,'raised_cents',coalesce(d.raised,0),'goal_cents',c.goal_cents,'currency',c.currency,'percent',CASE WHEN c.goal_cents>0 THEN least(100,coalesce(d.raised,0)*100/c.goal_cents) ELSE 0 END,'partner_name',CASE WHEN p.is_verified AND p.is_active THEN p.name ELSE '' END,'partner_blurb',CASE WHEN p.is_verified AND p.is_active THEN p.blurb ELSE '' END,'partner_website',CASE WHEN p.is_verified AND p.is_active THEN p.website ELSE '' END) FROM donations_campaign c LEFT JOIN (SELECT campaign_id,sum(amount_cents) raised FROM donations_donation WHERE status='completed' GROUP BY campaign_id)d ON d.campaign_id=c.id LEFT JOIN places_partner p ON p.id=c.partner_id WHERE c.is_active AND c.closed_at IS NULL ORDER BY c.title`, nil},
		{"completed_campaigns", `SELECT jsonb_build_object('title',c.title,'slug',c.slug,'outcome',c.outcome,'closed_at',c.closed_at,'raised_cents',coalesce((SELECT sum(amount_cents) FROM donations_donation WHERE campaign_id=c.id AND status='completed'),0),'currency',c.currency,'spend_entries',coalesce((SELECT jsonb_agg(jsonb_build_object('category',x.category,'amount_cents',x.amount_cents,'currency',x.currency,'period',x.period) ORDER BY x.amount_cents DESC) FROM donations_spendentry x WHERE x.campaign_id=c.id),'[]'::jsonb)) FROM donations_campaign c WHERE c.closed_at IS NOT NULL AND btrim(c.outcome)<>'' ORDER BY c.closed_at DESC`, nil},
		{"spend", `SELECT jsonb_build_object('category',category,'total_cents',sum(amount_cents)) FROM donations_spendentry WHERE currency=$1 GROUP BY category ORDER BY sum(amount_cents) DESC`, []any{currency}},
		{"in_kind", `SELECT jsonb_build_object('category',category,'unit_text',unit_text,'total_quantity',sum(quantity),'total_cents',sum(value_cents),'n',count(*)) FROM donations_inkindcontribution WHERE currency=$1 GROUP BY category,unit_text ORDER BY category,unit_text`, []any{currency}},
		{"civic_outcomes", `SELECT jsonb_build_object('headline',o.headline,'detail',o.detail,'period',o.period,'partner_name',CASE WHEN p.is_verified AND p.is_active THEN p.name ELSE NULL END) FROM donations_civicoutcome o LEFT JOIN places_partner p ON p.id=o.partner_id WHERE o.is_active ORDER BY o.created_at DESC,o.id DESC`, nil},
		{"cost_anchors", `SELECT jsonb_build_object('label',label,'amount_cents',amount_cents,'currency',currency,'spend_category',spend_category) FROM donations_costanchor WHERE is_active AND currency=$1 ORDER BY amount_cents DESC,id LIMIT $2`, []any{currency, anchorLimit}},
		{"partners", `SELECT jsonb_build_object('name',p.name,'kind',p.kind,'get_kind_display',CASE p.kind WHEN 'library' THEN 'Library' WHEN 'school' THEN 'School' WHEN 'ngo' THEN 'NGO / nonprofit' WHEN 'civic' THEN 'Civic body' WHEN 'healthcare' THEN 'Healthcare' WHEN 'cultural' THEN 'Cultural' WHEN 'business' THEN 'Local business' WHEN 'other' THEN 'Other' ELSE p.kind END,'blurb',p.blurb,'website',p.website,'place',CASE WHEN v.id IS NOT NULL THEN jsonb_build_object('id',v.id,'pk',v.id,'name',v.name) ELSE NULL END) FROM places_partner p LEFT JOIN places_place v ON v.id=p.place_id WHERE p.is_active AND p.is_verified ORDER BY p.name,p.id`, nil},
	}
	for _, statement := range statements {
		rows, err := s.DB.Query(ctx, statement.query, statement.args...)
		if err != nil {
			return nil, err
		}
		items := []map[string]any{}
		for rows.Next() {
			var row json.RawMessage
			if err = rows.Scan(&row); err != nil {
				rows.Close()
				return nil, err
			}
			item, err := readObject(row)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if statement.key == "completed_campaigns" && strings.TrimSpace(item["outcome"].(string)) == "" {
				continue
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		result[statement.key] = items
	}
	return result, nil
}

// Mine renders the owner's plain receipts. The public API projection deliberately
// omits external references, while this authenticated, self-only surface retains
// the original legacy receipt fields and campaign title.
func (s *Service) Mine(ctx context.Context, actor platform.Actor) ([]map[string]any, error) {
	if actor.ID <= 0 || !actor.IsActive {
		return nil, platform.ErrForbidden
	}
	rows, err := s.DB.Query(ctx, `SELECT jsonb_build_object('id',d.id,'amount_cents',d.amount_cents,'currency',d.currency,'recurring',d.recurring,'provider',d.provider,'status',d.status,'get_status_display',CASE d.status WHEN 'pending' THEN 'Pending' WHEN 'completed' THEN 'Completed' WHEN 'failed' THEN 'Failed' WHEN 'refunded' THEN 'Refunded' ELSE d.status END,'created_at',d.created_at,'external_ref',d.external_ref,'campaign',CASE WHEN c.id IS NOT NULL THEN jsonb_build_object('id',c.id,'title',c.title) ELSE NULL END) FROM donations_donation d LEFT JOIN donations_campaign c ON c.id=d.campaign_id WHERE d.donor_id=$1 ORDER BY d.created_at DESC,d.id DESC`, actor.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		item, err := readObject(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// CampaignChoices mirrors the active campaign choices of the original form.
// Start repeats that active check under a row lock, so stale forms cannot admit
// an earmark after staff deactivate it.
func (s *Service) CampaignChoices(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.DB.Query(ctx, `SELECT jsonb_build_object('id',id,'title',title,'slug',slug) FROM donations_campaign WHERE is_active ORDER BY title,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		item, err := readObject(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
