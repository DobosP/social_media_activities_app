package safety

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"time"
)

type Checkpoint struct {
	LastID    int64      `json:"last_id"`
	LastHash  string     `json:"last_hash"`
	CreatedAt *time.Time `json:"created_at"`
}

func sha(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func pythonTimestamp(value time.Time) string {
	value = value.UTC()
	if value.Nanosecond() == 0 {
		return value.Format("2006-01-02T15:04:05+00:00")
	}
	return value.Format("2006-01-02T15:04:05.000000+00:00")
}

// VerifyAuditChain streams immutable actor_ref and data without loading history
// into memory. A trusted checkpoint validates only the append-only extension.
func (s *Service) VerifyAuditChain(ctx context.Context, checkpoint *Checkpoint) (bool, *Checkpoint, error) {
	last := Checkpoint{}
	if checkpoint != nil {
		last = *checkpoint
		if last.LastID == 0 && last.LastHash != "" {
			return false, &last, nil
		}
		if last.LastID > 0 {
			var exists bool
			err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_auditlog WHERE id=$1 AND hash=$2)`, last.LastID, last.LastHash).Scan(&exists)
			if err != nil {
				return false, &last, err
			}
			if !exists {
				return false, &last, nil
			}
		}
	}
	rows, err := s.DB.Query(ctx, `SELECT id,actor_ref,event,target_ref,data,created_at,prev_hash,hash FROM safety_auditlog WHERE id>$1 ORDER BY id`, last.LastID)
	if err != nil {
		return false, &last, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var actor *int64
		var event, target, previous, hash string
		var raw []byte
		var created time.Time
		if err = rows.Scan(&id, &actor, &event, &target, &raw, &created, &previous, &hash); err != nil {
			return false, &last, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var data any
		if err = decoder.Decode(&data); err != nil {
			return false, &last, err
		}
		payload := map[string]any{"actor": actor, "event": event, "target": target, "data": data, "created": pythonTimestamp(created)}
		canonical, err := platform.CanonicalJSON(payload)
		if err != nil {
			return false, &last, err
		}
		expected := sha([]byte(last.LastHash + sha(canonical)))
		if previous != last.LastHash || (hash != expected && !legacyNumericHashMatches(payload, last.LastHash, hash)) {
			return false, &last, nil
		}
		last = Checkpoint{LastID: id, LastHash: hash, CreatedAt: &created}
	}
	if err = rows.Err(); err != nil {
		return false, &last, err
	}
	return true, &last, nil
}
