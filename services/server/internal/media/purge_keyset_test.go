package media_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

// Held evidence is never marked. The old limit*4 candidate window re-read the
// same oldest held rows on every run and never reached a later purgeable row.
func TestPostgresPurgePagesPastHeldExpiredEvidence(t *testing.T) {
	m, s, db, _ := mediaStore(t)
	ctx := context.Background()
	owner := user(t, db, "purge-keyset-owner", "adult")
	act, _ := activity(t, s, db, owner)
	heldPost, e := s.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic held evidence"}, false)
	if e != nil {
		t.Fatal(e)
	}
	freePost, e := s.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic expired picture"}, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, `UPDATE social_post SET is_hidden=true,is_author_deleted=false WHERE id=$1`, heldPost); e != nil {
		t.Fatal(e)
	}
	seed := func(post int64, name string, hoursAgo int) int64 {
		t.Helper()
		var id int64
		if e := db.QueryRow(ctx, `INSERT INTO media_attachment(post_id,uploader_id,kind,storage_key,thumb_storage_key,content_type,byte_size,sha256,original_filename,width,height,exif_stripped,created_at,expires_at,purged_at,duration_seconds,poster_content_type,poster_storage_key,processing_attempts,processing_started_at,source_storage_key,status) VALUES($1,$2,'image',$3,'','image/avif',1,$4,'fixture.avif',1,1,true,now(),now()-($5::int*interval '1 hour'),NULL,0,'','',0,NULL,'','ready') RETURNING id`, post, owner.ID, "attachments/"+name+".avif", strings.Repeat("b", 64), hoursAgo).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	const limit = 2
	// 41 (20*limit+1) held rows own the oldest expiries: five full pages of the old
	// limit*4 window, so the purgeable row is reached only by paging past held
	// evidence that is skipped without spending a transaction each.
	var held []int64
	for i := 0; i < 20*limit+1; i++ {
		held = append(held, seed(heldPost, fmt.Sprintf("purge-held-%d", i), 100-i))
	}
	free := seed(freePost, "purge-free", 1)
	if n, e := m.PurgeExpiredAttachments(ctx, limit); e != nil || n != 1 {
		t.Fatal("purge starved behind held expired evidence", n, e)
	}
	var purged bool
	if e = db.QueryRow(ctx, `SELECT purged_at IS NOT NULL AND storage_key='' FROM media_attachment WHERE id=$1`, free).Scan(&purged); e != nil || !purged {
		t.Fatal("purgeable attachment retained", purged, e)
	}
	var touched int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM media_attachment WHERE id=ANY($1) AND (purged_at IS NOT NULL OR storage_key='')`, held).Scan(&touched); e != nil || touched != 0 {
		t.Fatal("held evidence purged", touched, e)
	}
}
