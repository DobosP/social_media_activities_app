package media

import (
	"context"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ImportLicensedPlaceCover is the native trusted Commons-consumer seam. It never
// changes an existing cover and preserves the acquired license and attribution
// through metadata stripping and local processing. Acquisition remains opt-in.
func (s *Service) ImportLicensedPlaceCover(ctx context.Context, placeID int64, path, attribution, license, sourcePageURL, alt string) (id int64, err error) {
	source, parseErr := url.Parse(sourcePageURL)
	if parseErr != nil || source.Scheme != "https" || source.Hostname() != "commons.wikimedia.org" || source.User != nil || strings.TrimSpace(license) == "" || utf8.RuneCountInString(license) > 120 || utf8.RuneCountInString(attribution) > 255 || utf8.RuneCountInString(sourcePageURL) > 500 || utf8.RuneCountInString(alt) > 140 {
		return 0, ErrRejected
	}
	var exists bool
	if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_placecover WHERE place_id=$1)`, placeID).Scan(&exists); err != nil || exists {
		return 0, err
	}
	allowed, err := s.publicPlace(ctx, s.db, placeID)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, platform.ErrForbidden
	}
	// Commons' reviewed acquisition contract permits 8 MiB. Keep user uploads at
	// their separate 5 MiB ceiling; share the same bounded codec semaphore.
	processor := *s.processor
	processor.cfg.ImageMaxBytes = 8 << 20
	manifest, err := processor.ProcessImage(ctx, path)
	if err != nil {
		return 0, err
	}
	defer manifest.Cleanup()
	key, err := s.storeArtifact(ctx, "place-covers", manifest.Main)
	if err != nil {
		return 0, err
	}
	published := false
	defer func() {
		if !published {
			s.discard(ctx, key)
		}
	}()
	err = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		var locked int64
		if err := tx.QueryRow(ctx, `SELECT p.id FROM places_place p WHERE p.id=$1 AND `+catalog.PublicPlaceSQL+` FOR UPDATE`, placeID).Scan(&locked); err != nil {
			return err
		}
		var existing int64
		err := tx.QueryRow(ctx, `SELECT id FROM places_placecover WHERE place_id=$1`, placeID).Scan(&existing)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err = tx.QueryRow(ctx, `INSERT INTO places_placecover(place_id,source,uploaded_by_id,storage_key,content_type,byte_size,sha256,width,height,exif_stripped,attribution,license_name,source_page_url,alt_text,created_at,updated_at) VALUES($1,'wikimedia',NULL,$2,$3,$4,$5,$6,$7,true,$8,$9,$10,$11,now(),now()) RETURNING id`, placeID, key, manifest.Main.ContentType, manifest.Main.ByteSize, manifest.Main.SHA256, manifest.Main.Width, manifest.Main.Height, attribution, license, sourcePageURL, alt).Scan(&id); err != nil {
			return err
		}
		if err = saveManifest(ctx, tx, "place-cover", id, manifest); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "place.cover_resolved", fmt.Sprintf("places.place:%d", placeID), map[string]any{"source": "wikimedia", "license": license, "source_sha256": manifest.SourceSHA256})
	})
	if err == nil && id > 0 {
		published = true
	}
	return id, err
}

// PlaceVisuals issues public-cover URLs in one permission-filtered metadata query.
// Missing or withdrawn venues produce no image; scanner/uploaded-user bytes are
// never published merely because an object key exists.
func (s *Service) PlaceVisuals(ctx context.Context, q platform.Querier, places []int64) (map[int64]any, error) {
	result := map[int64]any{}
	if len(places) == 0 {
		return result, nil
	}
	if len(places) > 1000 {
		return nil, platform.ErrInvalid
	}
	rows, err := q.Query(ctx, `SELECT c.place_id,c.id,c.alt_text,c.attribution,c.license_name,c.source_page_url,c.source FROM places_placecover c JOIN places_place p ON p.id=c.place_id WHERE c.place_id=ANY($1) AND c.storage_key<>'' AND `+catalog.PublicPlaceSQL, places)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var place, id int64
		var alt, credit, license, page, source string
		if err = rows.Scan(&place, &id, &alt, &credit, &license, &page, &source); err != nil {
			return nil, err
		}
		href, err := s.url("place-cover", id, platform.Actor{}, "main", nil)
		if err != nil {
			return nil, err
		}
		result[place] = map[string]any{"kind": "place_cover_photo", "url": href, "alt": alt, "attribution": credit, "license_name": license, "source_page_url": page, "source": source}
	}
	return result, rows.Err()
}
func (s *Service) PlaceVisual(ctx context.Context, q platform.Querier, place int64) (any, error) {
	items, err := s.PlaceVisuals(ctx, q, []int64{place})
	return items[place], err
}
