package recommendations

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

type Record struct {
	ID, TypeID                    int64
	Data                          map[string]any
	TypeSlug, TypeName, PlaceName string
	Cosine, Metres                *float64
	Reason                        string
	Near, AccessMatch, TopicMatch bool
	score                         float64
	Tags                          map[string]any
}

func DistanceDecay(metres *float64) float64 {
	if metres == nil {
		return 1
	}
	return 1 / (1 + math.Max(*metres, 0)/3000)
}
func Score(cosine float64, metres *float64, access bool) float64 {
	score := math.Max(0, 1-cosine) * DistanceDecay(metres)
	if access {
		score += 0.05
	}
	return score
}
func (s *Service) Recommend(ctx context.Context, a platform.Actor, limit int, near catalog.Near, reasons bool) ([]Record, error) {
	if a.ID <= 0 || a.Cohort == "unassigned" || a.Cohort == "" {
		return []Record{}, nil
	}
	limit = max(1, min(limit, 50))
	vec, err := s.UserVector(ctx, a)
	if err != nil {
		return nil, err
	}
	warm := false
	for _, value := range vec {
		warm = warm || value != 0
	}
	proximity := near.Lon != nil && near.Lat != nil && near.Radius != nil && *near.Radius != 0
	args := []any{a.ID, a.Cohort, near.Lon, near.Lat, near.Radius}
	distance := `NULL::double precision`
	if warm && proximity {
		distance = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($3,$4),4326)::geography)`
	}
	cosine := "NULL::double precision"
	join := ""
	order := "a.starts_at,a.id"
	candidateLimit := limit
	if warm {
		args = append(args, vectorText(vec))
		join = ` JOIN recommendations_activityembedding emb ON emb.activity_id=a.id`
		cosine = `(emb.vector <=> $6::text::vector)`
		order = cosine + `,a.id`
		if proximity {
			candidateLimit *= 4
		}
	}
	where := social.ActivityVisibilitySQL() + ` AND a.status='open' AND a.starts_at>=now() AND NOT (EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.state='member') AND EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1)) AND ($3::double precision IS NULL OR $4::double precision IS NULL OR $5::double precision IS NULL OR $5=0 OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($3,$4),4326)::geography,$5))`
	query := `SELECT ` + social.ActivityProjectionSQL() + `,a.id,a.activity_type_id,t.slug,t.name,p.name,p.raw_tags,` + cosine + `,` + distance + ` FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id JOIN places_place p ON p.id=a.place_id` + join + ` WHERE ` + where + ` ORDER BY ` + order + ` LIMIT $` + strconv.Itoa(len(args)+1)
	rows, err := s.DB.Query(ctx, query, append(args, candidateLimit)...)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	ids := []int64{}
	for rows.Next() {
		var item Record
		var data, tags []byte
		if err := rows.Scan(&data, &item.ID, &item.TypeID, &item.TypeSlug, &item.TypeName, &item.PlaceName, &tags, &item.Cosine, &item.Metres); err != nil {
			rows.Close()
			return nil, err
		}
		if json.Unmarshal(data, &item.Data) != nil {
			return nil, platform.ErrInvalid
		}
		_ = json.Unmarshal(tags, &item.Tags)
		out = append(out, item)
		ids = append(ids, item.TypeID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if warm && proximity {
		pref, err := s.Catalog.Access(ctx, a)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].Near = out[i].Metres != nil && *out[i].Metres <= 2000
			out[i].AccessMatch = catalog.MatchesAccess(catalog.AccessibilityFacts(out[i].Tags), pref) == "match"
			if out[i].Cosine != nil {
				out[i].score = Score(*out[i].Cosine, out[i].Metres, out[i].AccessMatch)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
		if len(out) > limit {
			out = out[:limit]
		}
	}
	if reasons {
		topics, err := s.TopicSlugs(ctx, a)
		if err != nil {
			return nil, err
		}
		topicSet := map[string]bool{}
		for _, slug := range topics {
			topicSet["cat:"+slug] = true
		}
		tokens, err := typeTokens(ctx, s.DB, ids)
		if err != nil {
			return nil, err
		}
		interestRows, err := s.DB.Query(ctx, `SELECT t.slug,t.name FROM recommendations_userinterest i JOIN taxonomy_activitytype t ON t.id=i.activity_type_id WHERE i.user_id=$1`, a.ID)
		if err != nil {
			return nil, err
		}
		interestNames := map[string]string{}
		for interestRows.Next() {
			var slug, name string
			if err := interestRows.Scan(&slug, &name); err != nil {
				interestRows.Close()
				return nil, err
			}
			interestNames[slug] = name
		}
		err = interestRows.Err()
		interestRows.Close()
		if err != nil {
			return nil, err
		}
		for i := range out {
			item := &out[i]
			item.Reason = "soonest first"
			if item.Cosine != nil {
				percentage := max(0, min(100, int(math.RoundToEven((1-*item.Cosine)*100))))
				item.Reason = strconv.Itoa(percentage) + "% match"
				if name, ok := interestNames[item.TypeSlug]; ok {
					item.Reason = "matches your interest in " + name
				}
				if item.Near {
					item.Reason += " · near you"
				}
				if item.AccessMatch {
					item.Reason += " · matches your access needs"
				}
			}
			for _, token := range tokens[item.TypeID] {
				item.TopicMatch = item.TopicMatch || topicSet[token]
			}
			if item.TopicMatch {
				item.Reason += " · matches your chosen topics"
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].TopicMatch && !out[j].TopicMatch })
	}
	return out, nil
}
func (s *Service) StarterInterests(ctx context.Context, a platform.Actor, limit int) ([]map[string]any, error) {
	if a.ID < 1 || a.Cohort == "" || a.Cohort == "unassigned" {
		return []map[string]any{}, nil
	}
	rows, err := s.DB.Query(ctx, `SELECT t.id,t.slug,t.name FROM taxonomy_activitytype t WHERE t.is_active AND NOT EXISTS(SELECT 1 FROM recommendations_userinterest i WHERE i.activity_type_id=t.id AND i.user_id=$1) AND EXISTS(SELECT 1 FROM social_activity a WHERE a.activity_type_id=t.id AND `+social.ActivityVisibilitySQL()+` AND a.status='open' AND a.starts_at>=now()) ORDER BY t.name,t.slug LIMIT $3`, a.ID, a.Cohort, max(1, min(limit, 50)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var slug, name string
		if err := rows.Scan(&id, &slug, &name); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "slug": slug, "name": name})
	}
	return out, rows.Err()
}
