package media_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestPostgresOperatorCase5ExistingCoverReturnsIdentityBeforeCodec(t *testing.T) {
	m, _, db, blobs := integratedMedia(t)
	ctx := context.Background()
	place := testdb.Place(t, db, "Existing business cover", "osm")
	page := "https://commons.wikimedia.org/wiki/File:Original.png"
	id, err := m.ImportLicensedPlaceCover(ctx, place, sourceImage(t), "Original Artist", "CC BY 4.0", page, "Original alt")
	if err != nil || id == 0 {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE places_placecover SET source='business' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := db.QueryRow(ctx, `SELECT storage_key FROM places_placecover WHERE id=$1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	puts := len(blobs.keys)
	// A path that does not exist proves this return does not reach the codec.
	returned, err := m.ImportLicensedPlaceCover(ctx, place, filepath.Join(t.TempDir(), "never-read.png"), "Other Artist", "CC0", page, "Other alt")
	if err != nil || returned != id || len(blobs.keys) != puts {
		t.Fatal("existing cover identity was not returned before processing/storage", returned, err)
	}
	var heldKey, source, credit, license, sourcePage, alt string
	if err := db.QueryRow(ctx, `SELECT storage_key,source,attribution,license_name,source_page_url,alt_text FROM places_placecover WHERE id=$1 AND place_id=$2`, id, place).Scan(&heldKey, &source, &credit, &license, &sourcePage, &alt); err != nil || heldKey != key || source != "business" || credit != "Original Artist" || license != "CC BY 4.0" || sourcePage != page || alt != "Original alt" {
		t.Fatal("existing cover metadata/key changed", err)
	}
	if size, err := blobs.Size(ctx, key); err != nil || size == 0 {
		t.Fatal("existing physical cover was reclaimed", err)
	}
}

type operatorCase5RaceBlobs struct {
	*media.LocalStore
	install func(context.Context) error
	staged  []string
}

func (s *operatorCase5RaceBlobs) Put(ctx context.Context, key, path, mime string) error {
	if err := s.LocalStore.Put(ctx, key, path, mime); err != nil {
		return err
	}
	s.staged = append(s.staged, key)
	if install := s.install; install != nil {
		s.install = nil
		return install(ctx)
	}
	return nil
}

func TestPostgresOperatorCase5ExistingCoverRaceReturnsIDAndDiscardsStaging(t *testing.T) {
	_, soc, db, blobs := integratedMedia(t)
	ctx := context.Background()
	place := testdb.Place(t, db, "Race business cover", "osm")
	processor, err := media.NewProcessor(media.DefaultConfig(t.TempDir()), cleanScanner{}, cleanDocuments{})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := processor.ProcessImage(ctx, sourceImage(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Cleanup()
	existingKey := "place-covers/" + strings.Repeat("e", 32) + ".avif"
	if err := blobs.LocalStore.Put(ctx, existingKey, manifest.Main.Path, manifest.Main.ContentType); err != nil {
		t.Fatal(err)
	}
	var existingID int64
	store := &operatorCase5RaceBlobs{LocalStore: blobs.LocalStore}
	store.install = func(ctx context.Context) error {
		return db.QueryRow(ctx, `INSERT INTO places_placecover(place_id,source,uploaded_by_id,storage_key,content_type,byte_size,sha256,width,height,exif_stripped,attribution,license_name,source_page_url,alt_text,created_at,updated_at) VALUES($1,'business',NULL,$2,$3,$4,$5,$6,$7,true,'Original Artist','CC BY 4.0','https://commons.wikimedia.org/wiki/File:Original.png','Original alt',now(),now()) RETURNING id`, place, existingKey, manifest.Main.ContentType, manifest.Main.ByteSize, manifest.Main.SHA256, manifest.Main.Width, manifest.Main.Height).Scan(&existingID)
	}
	m := media.NewService(db, processor, store, media.TokenCodec{Key: []byte(strings.Repeat("s", 32))}, soc)
	returned, err := m.ImportLicensedPlaceCover(ctx, place, sourceImage(t), "New Artist", "CC0", "https://commons.wikimedia.org/wiki/File:New.png", "New alt")
	if err != nil || existingID == 0 || returned != existingID || len(store.staged) != 1 {
		t.Fatal("locked existing-cover race lost returned identity", returned, existingID, err)
	}
	var key, source, credit, license, alt string
	if err := db.QueryRow(ctx, `SELECT storage_key,source,attribution,license_name,alt_text FROM places_placecover WHERE id=$1 AND place_id=$2`, existingID, place).Scan(&key, &source, &credit, &license, &alt); err != nil || key != existingKey || source != "business" || credit != "Original Artist" || license != "CC BY 4.0" || alt != "Original alt" {
		t.Fatal("race overwrote original cover metadata or physical identity", err)
	}
	if _, err := m.DrainBlobDeletions(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if size, err := blobs.Size(ctx, existingKey); err != nil || size != manifest.Main.ByteSize {
		t.Fatal("race reclaimed authoritative original bytes", err)
	}
	for _, key := range store.staged {
		if _, err := blobs.Size(ctx, key); err == nil {
			t.Fatal("raced return retained unreferenced newly staged bytes")
		}
	}
	var manifests int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM media_go_manifest WHERE kind='place-cover' AND row_id=$1`, existingID).Scan(&manifests); err != nil || manifests != 0 {
		t.Fatal("race committed a new manifest onto existing cover", err)
	}
}
