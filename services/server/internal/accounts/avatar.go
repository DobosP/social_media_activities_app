package accounts

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

var avatarColors = map[string]string{"team_sport": "#ff7a45", "racquet_sport": "#ffa940", "sport": "#ff7a45", "outdoor": "#52c41a", "fitness": "#13c2c2", "tabletop": "#9254de", "reading": "#4096ff", "video_games": "#f759ab", "culture": "#ffc53d", "social": "#ff85c0"}

type avatarInputs struct {
	Seed             string
	Generation, Salt int
	Nodes            []avatars.Node
	Edges            []avatars.Edge
}

func avatarInput(ctx context.Context, q platform.Querier, user int64) (avatarInputs, error) {
	var in avatarInputs
	err := q.QueryRow(ctx, `SELECT u.username,COALESCE(s.generation,1),COALESCE(s.salt,0) FROM accounts_user u LEFT JOIN accounts_signatureavatar s ON s.user_id=u.id WHERE u.id=$1`, user).Scan(&in.Seed, &in.Generation, &in.Salt)
	if err != nil {
		return in, err
	}
	if in.Generation != 1 && in.Generation != 2 {
		in.Generation = 1
	}
	rows, err := q.Query(ctx, `SELECT COALESCE(c.slug,'') FROM recommendations_userinterest i JOIN taxonomy_activitytype t ON t.id=i.activity_type_id LEFT JOIN taxonomy_activitycategory c ON c.id=t.category_id WHERE i.user_id=$1 ORDER BY t.slug`, user)
	if err != nil {
		return in, err
	}
	defer rows.Close()
	for rows.Next() {
		var category string
		if err = rows.Scan(&category); err != nil {
			return in, err
		}
		color := avatarColors[category]
		if color == "" {
			color = "#8c8c8c"
		}
		in.Nodes = append(in.Nodes, avatars.Node{Color: color, Category: category})
	}
	if err = rows.Err(); err != nil {
		return in, err
	}
	for i := range in.Nodes {
		for j := i + 1; j < len(in.Nodes); j++ {
			if in.Nodes[i].Category != "" && in.Nodes[i].Category == in.Nodes[j].Category {
				in.Edges = append(in.Edges, avatars.Edge{i, j})
			}
		}
	}
	return in, nil
}

// Avatar is always a base render for other-user surfaces. Progression never
// enters this callback, regardless of caller or selected generation.
func Avatar(ctx context.Context, q platform.Querier, userID int64) (string, error) {
	return renderAvatar(ctx, q, userID, 80, 0)
}

// Avatars preserves the two-query list-surface contract. Callers pass only IDs
// already authorized by the shared profile/roster resolver.
func Avatars(ctx context.Context, q platform.Querier, userIDs []int64) (map[int64]string, error) {
	result := map[int64]string{}
	if len(userIDs) == 0 {
		return result, nil
	}
	inputs := map[int64]avatarInputs{}
	rows, err := q.Query(ctx, `SELECT u.id,u.username,COALESCE(s.generation,1),COALESCE(s.salt,0) FROM accounts_user u LEFT JOIN accounts_signatureavatar s ON s.user_id=u.id WHERE u.id=ANY($1)`, userIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var in avatarInputs
		if err = rows.Scan(&id, &in.Seed, &in.Generation, &in.Salt); err != nil {
			rows.Close()
			return nil, err
		}
		if in.Generation != 1 && in.Generation != 2 {
			in.Generation = 1
		}
		inputs[id] = in
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `SELECT i.user_id,COALESCE(c.slug,'') FROM recommendations_userinterest i JOIN taxonomy_activitytype t ON t.id=i.activity_type_id LEFT JOIN taxonomy_activitycategory c ON c.id=t.category_id WHERE i.user_id=ANY($1) ORDER BY t.slug`, userIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var category string
		if err = rows.Scan(&id, &category); err != nil {
			rows.Close()
			return nil, err
		}
		in, ok := inputs[id]
		if !ok {
			continue
		}
		color := avatarColors[category]
		if color == "" {
			color = "#8c8c8c"
		}
		in.Nodes = append(in.Nodes, avatars.Node{Color: color, Category: category})
		inputs[id] = in
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for id, in := range inputs {
		for i := range in.Nodes {
			for j := i + 1; j < len(in.Nodes); j++ {
				if in.Nodes[i].Category != "" && in.Nodes[i].Category == in.Nodes[j].Category {
					in.Edges = append(in.Edges, avatars.Edge{i, j})
				}
			}
		}
		result[id] = avatars.DataURI(avatars.RenderGeneration(in.Generation, avatars.SignatureSeed(in.Seed, in.Generation, in.Salt), in.Nodes, in.Edges, avatars.Options{PX: 80}))
	}
	return result, nil
}
func renderAvatar(ctx context.Context, q platform.Querier, user int64, px int, intensity float64) (string, error) {
	in, err := avatarInput(ctx, q, user)
	if err != nil {
		return "", err
	}
	return avatars.DataURI(avatars.RenderGeneration(in.Generation, avatars.SignatureSeed(in.Seed, in.Generation, in.Salt), in.Nodes, in.Edges, avatars.Options{PX: px, Intensity: intensity})), nil
}

func canonicalFingerprint(in avatarInputs, generation, salt int) string {
	svg := avatars.RenderGeneration(generation, avatars.SignatureSeed(in.Seed, generation, salt), in.Nodes, in.Edges, avatars.Options{PX: avatars.CanonicalPX, UIDOverride: avatars.FingerprintUID})
	return fmt.Sprintf("%x", sha256.Sum256([]byte(svg)))
}

func Progression(ctx context.Context, q platform.Querier, user int64) (map[string]int, error) {
	var count int
	err := q.QueryRow(ctx, `SELECT count(*) FROM social_membership WHERE user_id=$1 AND state='member' AND met_confirmed_at IS NOT NULL`, user).Scan(&count)
	level := 0
	for _, threshold := range []int{1, 3, 6, 12, 24} {
		if count >= threshold {
			level++
		}
	}
	return map[string]int{"count": count, "level": level, "max_level": 5}, err
}
