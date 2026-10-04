package commands

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
)

func (s *Service) seedMobile(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	if err := s.demoGate(); err != nil {
		return nil, err
	}
	if len(input) > 0 {
		return nil, platform.ErrInvalid
	}
	if s.Runner.Config.Social == nil || (s.Config.Media == nil && s.Config.SeedUploadCover == nil) || s.Config.Scratch == "" {
		return nil, missingDependency("native development cover media")
	}
	owner, _, err := s.demoAccount(ctx, "mobile_cards_owner", "Mobile Cards Owner", "adult", false)
	if err != nil {
		return nil, err
	}
	if _, _, err = s.demoAccount(ctx, "mobile_cards_viewer", "Mobile Cards Viewer", "adult", false); err != nil {
		return nil, err
	}
	var cards []map[string]any
	if err = demoPart("seed_mobile_card_demo", "DEMO_CARDS", &cards); err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, card := range cards {
		typ, _, err := s.demoType(ctx, demoText(card["slug"]))
		if err == pgx.ErrNoRows {
			typ, err = s.ensureMobileType(ctx, demoText(card["slug"]), demoText(card["type_name"]))
			if err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		place, err := s.demoPlace(ctx, owner, demoText(card["place"]), card["lon"].(float64), card["lat"].(float64), false, "seed_mobile_card_demo")
		if err != nil {
			return nil, err
		}
		start := demoHour(s.Runner.Config.Now().AddDate(0, 0, demoInt(card["days"], 0)), demoInt(card["hour"], 10))
		var activity int64
		err = s.Runner.DB.QueryRow(ctx, `SELECT id FROM social_activity WHERE owner_id=$1 AND title=$2 ORDER BY id LIMIT 1`, owner.ID, demoText(card["title"])).Scan(&activity)
		capacity := 12
		if err == pgx.ErrNoRows {
			activity, err = s.Runner.Config.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: demoText(card["title"]), Description: demoText(card["description"]), StartsAt: start, Capacity: &capacity, BeginnersWelcome: true})
		} else if err == nil {
			var currentPlace int64
			if err = s.Runner.DB.QueryRow(ctx, `SELECT place_id FROM social_activity WHERE id=$1`, activity).Scan(&currentPlace); err != nil {
				return nil, err
			}
			if currentPlace != place {
				if err = s.Runner.Config.Social.MoveActivity(ctx, owner, activity, place); err != nil {
					return nil, err
				}
			}
			err = s.Runner.Config.Social.UpdateActivity(ctx, owner, activity, demoFields(map[string]any{"description": card["description"], "starts_at": start, "capacity": 12, "beginners_welcome": true}))
		}
		if err != nil {
			return nil, err
		}
		if err = s.Runner.Config.Social.SetActivityListing(ctx, owner, activity, true); err != nil {
			return nil, err
		}
		if card["colors"] != nil {
			path, err := s.demoCover(card["colors"])
			if err != nil {
				return nil, err
			}
			if s.Config.SeedUploadCover != nil {
				err = s.Config.SeedUploadCover(ctx, owner, activity, path, demoText(card["alt"]))
			} else {
				_, err = s.Config.Media.UploadActivityCover(ctx, owner, activity, path, demoText(card["alt"]))
			}
			_ = os.Remove(path)
			if err != nil {
				return nil, err
			}
		}
		ids = append(ids, activity)
	}
	return map[string]any{"activities": ids, "owner": owner.PublicID}, nil
}
func (s *Service) ensureMobileType(ctx context.Context, slug, name string) (int64, error) {
	var id int64
	err := platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('development-mobile-taxonomy',0))`); err != nil {
			return err
		}
		var category int64
		err := tx.QueryRow(ctx, `SELECT id FROM taxonomy_activitycategory WHERE slug='mobile-card-demo'`).Scan(&category)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,description,parent_id,created_at,updated_at) VALUES('mobile-card-demo','Mobile card demo','',NULL,now(),now()) RETURNING id`).Scan(&category)
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,category_id,parent_id,aliases,is_active,wellness,family_friendly,created_at,updated_at) VALUES($1,$2,$3,NULL,'[]',true,false,false,now(),now()) ON CONFLICT(slug) DO UPDATE SET is_active=taxonomy_activitytype.is_active RETURNING id`, slug, name, category).Scan(&id)
		return err
	})
	return id, err
}
func (s *Service) demoCover(value any) (string, error) {
	var colors [2][3]uint8
	raw, _ := json.Marshal(value)
	if json.Unmarshal(raw, &colors) != nil {
		return "", platform.ErrInvalid
	}
	im := image.NewRGBA(image.Rect(0, 0, 1200, 760))
	first, last := colors[0], colors[1]
	for y := 0; y < 760; y++ {
		t := float64(y) / 759
		shade := color.RGBA{uint8(math.RoundToEven(float64(first[0])*(1-t) + float64(last[0])*t)), uint8(math.RoundToEven(float64(first[1])*(1-t) + float64(last[1])*t)), uint8(math.RoundToEven(float64(first[2])*(1-t) + float64(last[2])*t)), 255}
		for x := 0; x < 1200; x++ {
			im.SetRGBA(x, y, shade)
		}
	}
	ellipse := func(cx, cy, rx, ry int, shade color.RGBA) {
		for y := max(0, cy-ry); y < min(760, cy+ry); y++ {
			for x := max(0, cx-rx); x < min(1200, cx+rx); x++ {
				dx, dy := float64(x-cx)/float64(rx), float64(y-cy)/float64(ry)
				if dx*dx+dy*dy <= 1 {
					im.SetRGBA(x, y, shade)
				}
			}
		}
	}
	ellipse(90, 350, 270, 270, color.RGBA{uint8(min(255, int(first[0])+35)), uint8(min(255, int(first[1])+35)), uint8(min(255, int(first[2])+35)), 255})
	ellipse(1040, 150, 280, 270, color.RGBA{uint8(max(0, int(last[0])-35)), uint8(max(0, int(last[1])-35)), uint8(max(0, int(last[2])-35)), 255})
	for y := 560; y <= 700; y++ {
		for x := 80; x <= 1120; x++ {
			cx, cy := x, y
			if x < 108 {
				cx = 108
			} else if x > 1092 {
				cx = 1092
			}
			if y < 588 {
				cy = 588
			} else if y > 672 {
				cy = 672
			}
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= 28*28 {
				im.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	if err := os.MkdirAll(s.Config.Scratch, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(s.Config.Scratch, "demo-cover-*.png")
	if err != nil {
		return "", err
	}
	path := file.Name()
	err = png.Encode(file, im)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		if err == nil {
			err = closeErr
		}
		return "", err
	}
	return path, nil
}
