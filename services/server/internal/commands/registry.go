// Package commands implements native manual operator commands. It never imports
// or launches Django. Every command validates an explicit bounded options object.
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"sort"

	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
)

type Config struct {
	BootstrapAdministrator   func(context.Context, string, string) (platform.Actor, error)
	Recommendations          *recommendations.Service
	Storage                  media.Store
	DecodeProfileHash        func(context.Context, []byte) (string, error)
	SeedRoot                 string
	DemoEnabled              bool
	Sources                  map[string]PlaceSource
	OverpassURL, DefaultCity string
	FetchOverpass            func(context.Context, string, string) ([]byte, error)
	GoogleEnrich             func(context.Context, EnrichPlace) (EnrichResult, error)
	WikidataEnrich           func(context.Context, []EnrichPlace) (map[int64]EnrichResult, error)
	SeedAccount              func(context.Context, string, string, string, bool) (platform.Actor, bool, error)
	SeedConsent              func(context.Context, platform.Actor, platform.Actor) error
	SeedComplete             func(context.Context, platform.Actor, int64) error
	SeedApproveVenue         func(context.Context, platform.Actor, int64) error
	Messaging                *messaging.Service
	Media                    *media.Service
	Scratch                  string
	SeedUploadCover          func(context.Context, platform.Actor, int64, string, string) error
}
type Service struct {
	Runner *jobs.Runner
	Config Config
}

var NativeNames = []string{"ingest_places", "ingest_events", "enrich_places", "dedup_places", "aggregate_unnamed_places", "backfill_embeddings", "backfill_avatar_phash", "seed_booking_links", "resolve_place_covers", "sync_roedu_events", "load_roedu_seed", "seed_demo_users", "seed_demo_data", "seed_browse_demo", "generate_demo_events", "seed_mobile_card_demo", "createsuperuser", "create_superuser"}

func Install(r *jobs.Runner, c Config) error {
	if r == nil {
		return platform.ErrInvalid
	}
	s := &Service{r, c}
	handlers := map[string]jobs.Handler{"ingest_places": s.ingestPlaces, "ingest_events": s.ingestEvents, "enrich_places": s.enrichPlaces, "dedup_places": s.dedupPlaces, "aggregate_unnamed_places": s.aggregate, "backfill_embeddings": s.backfillEmbeddings, "backfill_avatar_phash": s.backfillAvatars, "seed_booking_links": s.seedBooking, "resolve_place_covers": s.resolveCovers, "sync_roedu_events": s.syncRoeduEvents, "load_roedu_seed": s.loadSeed, "seed_demo_users": s.seedDemoUsers, "seed_demo_data": s.seedWorld, "seed_browse_demo": s.seedBrowse, "generate_demo_events": s.generateDemoEvents, "seed_mobile_card_demo": s.seedMobile, "createsuperuser": s.bootstrapAdministrator, "create_superuser": s.bootstrapAdministrator}
	for _, name := range NativeNames {
		if err := r.Register(name, handlers[name]); err != nil {
			return err
		}
	}
	return nil
}
func options(input map[string]json.RawMessage, out any) error {
	if len(input) > 32 {
		return platform.ErrInvalid
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 64<<10 {
		return platform.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return platform.ErrInvalid
	}
	return nil
}
func (s *Service) backfillEmbeddings(ctx context.Context, in map[string]json.RawMessage) (any, error) {
	if len(in) > 0 {
		return nil, platform.ErrInvalid
	}
	if s.Config.Recommendations == nil {
		return nil, errors.New("native recommendations dependency unavailable")
	}
	rows, err := s.Runner.DB.Query(ctx, `SELECT a.id FROM social_activity a LEFT JOIN recommendations_activityembedding emb ON emb.activity_id=a.id WHERE emb.activity_id IS NULL ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	count := 0
	for _, id := range ids {
		if err = s.Config.Recommendations.RecomputeEmbedding(ctx, id); err != nil {
			return map[string]int{"embedded": count}, err
		}
		count++
	}
	return map[string]int{"embedded": count}, nil
}
func (s *Service) backfillAvatars(ctx context.Context, in map[string]json.RawMessage) (any, error) {
	if len(in) > 0 {
		return nil, platform.ErrInvalid
	}
	if s.Config.Storage == nil {
		return nil, errors.New("native media storage dependency unavailable")
	}
	rows, err := s.Runner.DB.Query(ctx, `SELECT id,storage_key FROM media_photo WHERE kind='profile' AND phash='' AND storage_key<>'' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type photo struct {
		id  int64
		key string
	}
	photos := []photo{}
	for rows.Next() {
		var p photo
		if err = rows.Scan(&p.id, &p.key); err != nil {
			rows.Close()
			return nil, err
		}
		photos = append(photos, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	done, skipped := 0, 0
	for _, p := range photos {
		size, e := s.Config.Storage.Size(ctx, p.key)
		if e != nil || size < 1 || size >= 8<<20 {
			skipped++
			continue
		}
		raw, e := s.Config.Storage.OpenRange(ctx, p.key, 0, size-1)
		if e != nil {
			skipped++
			continue
		}
		hash := ""
		if s.Config.DecodeProfileHash != nil {
			hash, e = s.Config.DecodeProfileHash(ctx, raw)
		} else {
			var decoded image.Image
			decoded, _, e = image.Decode(bytes.NewReader(raw))
			if e == nil {
				hash = media.DHash(decoded)
			}
		}
		if e != nil || hash == "" {
			skipped++
			continue
		}
		tag, e := s.Runner.DB.Exec(ctx, `UPDATE media_photo SET phash=$2 WHERE id=$1 AND phash='' AND storage_key=$3`, p.id, hash, p.key)
		if e != nil {
			return map[string]int{"backfilled": done, "skipped": skipped}, e
		}
		done += int(tag.RowsAffected())
	}
	return map[string]int{"backfilled": done, "skipped": skipped}, nil
}
func Names() []string { names := append([]string{}, NativeNames...); sort.Strings(names); return names }
func missingDependency(name string) error {
	return fmt.Errorf("native operator dependency unavailable: %s", name)
}
