package media_test

import (
	"context"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

func TestRetirementExpiredMediaEvidencePreservationMatrix(t *testing.T) {
	m, soc, db, blobs := mediaStore(t)
	ctx := context.Background()
	image := sourceImage(t)
	for _, scenario := range []struct {
		name, reportModel, reportStatus                                    string
		hiddenPost, authorDeleted, hiddenActivity, remove, lifted, reclaim bool
	}{
		{name: "plain-expired-row-retained", reclaim: true},
		{name: "admin-hidden-post", hiddenPost: true},
		{name: "reported-post", reportModel: "post", reportStatus: "open"},
		{name: "reported-uploader", reportModel: "user", reportStatus: "open"},
		{name: "reported-activity", reportModel: "activity", reportStatus: "open"},
		{name: "actioned-report-still-held", reportModel: "post", reportStatus: "actioned"},
		{name: "dismissed-report-released", reportModel: "post", reportStatus: "dismissed", reclaim: true},
		{name: "hidden-activity", hiddenActivity: true},
		{name: "author-withdrawal", hiddenPost: true, authorDeleted: true, reclaim: true},
		{name: "unlifted-remove-and-author-withdrawal", hiddenPost: true, authorDeleted: true, remove: true},
		{name: "lifted-remove-and-author-withdrawal", hiddenPost: true, authorDeleted: true, remove: true, lifted: true, reclaim: true},
		{name: "reported-author-withdrawal", hiddenPost: true, authorDeleted: true, reportModel: "post", reportStatus: "open"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			owner := user(t, db, "retirement-media-"+scenario.name, "adult")
			act, _ := activity(t, soc, db, owner)
			post, err := soc.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic private evidence"}, false)
			if err != nil {
				t.Fatal(err)
			}
			att, err := m.AttachToPost(ctx, owner, post, image, "fixture.png", nil)
			if err != nil {
				t.Fatal(err)
			}
			var key string
			if err := db.QueryRow(ctx, `SELECT storage_key FROM media_attachment WHERE id=$1`, att.ID).Scan(&key); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE media_attachment SET expires_at=now()-interval '1 minute' WHERE id=$1`, att.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE social_post SET is_hidden=$2,is_author_deleted=$3 WHERE id=$1`, post, scenario.hiddenPost, scenario.authorDeleted); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE social_activity SET is_hidden=$2 WHERE id=$1`, act, scenario.hiddenActivity); err != nil {
				t.Fatal(err)
			}
			if scenario.reportModel != "" {
				app, target := "social", post
				if scenario.reportModel == "user" {
					app, target = "accounts", owner.ID
				}
				if scenario.reportModel == "activity" {
					target = act
				}
				if _, err := db.Exec(ctx, `INSERT INTO safety_report(target_type_id,target_id,reporter_id,reason,detail,status,resolution,created_at,handled_by_id,handled_at) SELECT id,$3,$4,'other','Synthetic report',$5,'',now(),NULL,NULL FROM django_content_type WHERE app_label=$1 AND model=$2`, app, scenario.reportModel, target, owner.ID, scenario.reportStatus); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.remove {
				if _, err := db.Exec(ctx, `INSERT INTO safety_moderationaction(target_type_id,target_id,moderator_id,action,reason,notes,created_at,expires_at,report_id,lifted_at) SELECT id,$1,$2,'remove','other','',now(),NULL,NULL,CASE WHEN $3 THEN now() ELSE NULL END FROM django_content_type WHERE app_label='social' AND model='post'`, post, owner.ID, scenario.lifted); err != nil {
					t.Fatal(err)
				}
			}
			purged, err := m.PurgeExpiredAttachments(ctx, 100)
			want := 0
			if scenario.reclaim {
				want = 1
			}
			if err != nil || purged != want {
				t.Fatalf("evidence purge=%d want=%d error=%v", purged, want, err)
			}
			if _, err := m.DrainBlobDeletions(ctx, 100); err != nil {
				t.Fatal(err)
			}
			var marked bool
			var storedKey string
			if err := db.QueryRow(ctx, `SELECT purged_at IS NOT NULL,storage_key FROM media_attachment WHERE id=$1`, att.ID).Scan(&marked, &storedKey); err != nil {
				t.Fatal("purge deleted provenance row", err)
			}
			if marked != scenario.reclaim {
				t.Fatal("purge marker differs from evidence policy")
			}
			_, physicalErr := blobs.Size(ctx, key)
			if scenario.reclaim {
				if storedKey != "" || physicalErr == nil {
					t.Fatal("reclaimed evidence still had servable bytes")
				}
			} else if storedKey != key || physicalErr != nil {
				t.Fatalf("held evidence changed row/key or lost physical bytes: %v", physicalErr)
			}
		})
	}
}
