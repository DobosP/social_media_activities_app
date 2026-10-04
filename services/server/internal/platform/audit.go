package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
)

func digest(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }

func RecordAudit(ctx context.Context, tx pgx.Tx, actor Actor, event, target string, data any) error {
	// Serializes the empty-chain case as well as the tail-row case. The lock is
	// transaction-scoped so domain mutation and audit append commit together.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951209)`); err != nil {
		return err
	}
	var previous string
	err := tx.QueryRow(ctx, `SELECT hash FROM safety_auditlog ORDER BY id DESC LIMIT 1 FOR UPDATE`).Scan(&previous)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	created := time.Now().UTC().Truncate(time.Microsecond)
	createdISO := created.Format("2006-01-02T15:04:05.000000+00:00")
	if created.Nanosecond() == 0 {
		createdISO = created.Format("2006-01-02T15:04:05+00:00")
	}
	var actorID any
	if actor.ID > 0 {
		actorID = actor.ID
	}
	if data == nil {
		data = map[string]any{}
	}
	canonical, err := CanonicalJSON(map[string]any{"actor": actorID, "event": event, "target": target, "data": data, "created": createdISO})
	if err != nil {
		return err
	}
	dataJSON, err := StoredAuditJSON(data)
	if err != nil {
		return err
	}
	chain := digest([]byte(previous + digest(canonical)))
	_, err = tx.Exec(ctx, `INSERT INTO safety_auditlog(actor_id,actor_ref,event,target_ref,data,created_at,prev_hash,hash) VALUES($1::bigint,$1::integer,$2,$3,$4,$5,$6,$7)`, actorID, event, target, dataJSON, created, previous, chain)
	return err
}

// Notify is the single in-app notification choke point for every Go domain.
func Notify(ctx context.Context, tx pgx.Tx, recipient int64, kind, title, body, url string) (bool, error) {
	if kind != "moderation" && kind != "system" {
		var muted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notifications_notificationpreference WHERE user_id=$1 AND $2=ANY(muted_kinds))`, recipient, kind).Scan(&muted); err != nil {
			return false, err
		}
		if muted {
			return false, nil
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,read_at,created_at) VALUES($1,$2,$3,$4,$5,NULL,now())`, recipient, kind, title, body, url)
	return err == nil, err
}

// Hash is the stable SHA256 digest used for public-key fingerprints.
func Hash(raw []byte) string { return digest(raw) }
