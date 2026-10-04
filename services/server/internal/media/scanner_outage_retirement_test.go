package media

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

type retirementProviderOutage struct{}

func (retirementProviderOutage) Effective() bool { return true }
func (retirementProviderOutage) Scan(context.Context, ScanInput) (Verdict, error) {
	return Verdict{}, ErrScanner
}

// Source bytes are deliberately opaque: every tested scanner stops before a
// codec may read them. This tests the durable pending/evidence contract rather
// than relying on a decoder failure to simulate scanner unavailability.
func TestRetirementPendingVideoScannerOutagePreservesBytesAndAttemptBudget(t *testing.T) {
	dsnFlag := flag.Lookup("media-test-dsn")
	if dsnFlag == nil || dsnFlag.Value.String() == "" {
		t.Skip("explicit disposable media-test-dsn not supplied")
	}
	db := testdb.New(t, dsnFlag.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	owner := testdb.Actor(t, db, "retirement-outage-owner", "adult")
	place := testdb.Place(t, db, "Synthetic pending-video venue", "osm")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic pending-video activity", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	post, err := soc.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "Synthetic pending video"}, false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(filepath.Join(t.TempDir(), "private-blobs"))
	if err != nil {
		t.Fatal(err)
	}
	bytes := []byte("opaque synthetic pending video source")
	input := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(input, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	key := "attachments/retirement-pending-source.mp4"
	if err := store.Put(ctx, key, input, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	var attachment int64
	if err := db.QueryRow(ctx, `INSERT INTO media_attachment(post_id,uploader_id,kind,storage_key,thumb_storage_key,content_type,byte_size,sha256,original_filename,width,height,exif_stripped,created_at,expires_at,purged_at,duration_seconds,poster_content_type,poster_storage_key,processing_attempts,processing_started_at,source_storage_key,status) VALUES($1,$2,'video','','','video/mp4',$3,$4,'fixture.mp4',0,0,false,now(),NULL,NULL,0,'','',0,NULL,$5,'pending') RETURNING id`, post, owner.ID, len(bytes), strings.Repeat("a", 64), key).Scan(&attachment); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name     string
		scanner  Scanner
		attempts int
		drains   int
	}{
		{"nil-scanner", nil, 0, 1},
		{"empty-blocklist", &Blocklist{}, 0, 1},
		{"typed-nil-blocklist", (*Blocklist)(nil), 0, 1},
		{"provider-fails-after-claim", retirementProviderOutage{}, 2, 5},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, `UPDATE media_attachment SET processing_attempts=$2,status='pending',processing_started_at=NULL WHERE id=$1`, attachment, scenario.attempts); err != nil {
				t.Fatal(err)
			}
			processor, err := NewProcessor(DefaultConfig(t.TempDir()), scenario.scanner, nil)
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(db, processor, store, TokenCodec{Key: []byte(strings.Repeat("s", 48))}, soc)
			for range scenario.drains {
				processed, err := service.ProcessPendingVideos(ctx, 2)
				if processed != 0 || err != nil && !errors.Is(err, ErrScanner) {
					t.Fatal("scanner outage processed or destructively failed pending media", err)
				}
				var state, sourceKey string
				var attempts, queued int
				if err := db.QueryRow(ctx, `SELECT status,source_storage_key,processing_attempts,(SELECT count(*) FROM media_go_blobdeletion WHERE storage_key=$2) FROM media_attachment WHERE id=$1`, attachment, key).Scan(&state, &sourceKey, &attempts, &queued); err != nil || state != "pending" || sourceKey != key || attempts != scenario.attempts || queued != 0 {
					t.Fatalf("outage changed pending evidence/budget: state=%s attempts=%d queued=%d error=%v", state, attempts, queued, err)
				}
				if size, err := store.Size(ctx, key); err != nil || size != int64(len(bytes)) {
					t.Fatal("scanner outage removed source bytes")
				}
			}
		})
	}
	t.Run("deferral-audit-failure-fences-uncertain-exhausted-lease", func(t *testing.T) {
		if _, err := db.Exec(ctx, `UPDATE media_attachment SET processing_attempts=2,status='pending',processing_started_at=NULL WHERE id=$1`, attachment); err != nil {
			t.Fatal(err)
		}
		// NOT VALID preserves prior successful deferral audits while rejecting
		// every new append, so the restoration transaction deterministically fails.
		if _, err := db.Exec(ctx, `ALTER TABLE safety_auditlog ADD CONSTRAINT retirement_refuse_video_deferral CHECK(event<>'media.video_deferred') NOT VALID`); err != nil {
			t.Fatal(err)
		}
		processor, err := NewProcessor(DefaultConfig(t.TempDir()), retirementProviderOutage{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		service := NewService(db, processor, store, TokenCodec{Key: []byte(strings.Repeat("s", 48))}, soc)
		assertHeld := func() {
			var state, sourceKey string
			var attempts, queued int
			if err := db.QueryRow(ctx, `SELECT status,source_storage_key,processing_attempts,(SELECT count(*) FROM media_go_blobdeletion WHERE storage_key=$2) FROM media_attachment WHERE id=$1`, attachment, key).Scan(&state, &sourceKey, &attempts, &queued); err != nil || state != "processing" || sourceKey != key || attempts != 3 || queued != 0 {
				t.Fatalf("uncertain scanner lease changed evidence: state=%s attempts=%d queued=%d error=%v", state, attempts, queued, err)
			}
			if size, err := store.Size(ctx, key); err != nil || size != int64(len(bytes)) {
				t.Fatal("failed scanner deferral removed retained source bytes")
			}
		}
		if processed, err := service.ProcessPendingVideos(ctx, 2); processed != 0 || err == nil {
			t.Fatal("failed atomic deferral was reported as successful drain")
		}
		assertHeld()
		if _, err := db.Exec(ctx, `UPDATE media_attachment SET processing_started_at=now()-interval '1 hour' WHERE id=$1`, attachment); err != nil {
			t.Fatal(err)
		}
		if processed, err := service.ProcessPendingVideos(ctx, 2); processed != 0 || !errors.Is(err, ErrProcessing) {
			t.Fatal("exhausted unconfirmed lease was destructively finalized", err)
		}
		assertHeld()
	})
}
