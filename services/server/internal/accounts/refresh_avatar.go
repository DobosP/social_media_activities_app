package accounts

import (
	"context"
	"errors"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// RefreshAvatarFingerprint must be called inside the interest-edit transaction.
// It never creates a style pick and never logs the internal fingerprint.
func RefreshAvatarFingerprint(ctx context.Context, tx pgx.Tx, a platform.Actor) error {
	var generation, salt int
	var previous string
	err := tx.QueryRow(ctx, `SELECT generation,salt,fingerprint FROM accounts_signatureavatar WHERE user_id=$1 FOR UPDATE`, a.ID).Scan(&generation, &salt, &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	in, err := avatarInput(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	order := []int{salt}
	for candidate := 0; candidate < 16; candidate++ {
		if candidate != salt {
			order = append(order, candidate)
		}
	}
	for _, candidate := range order {
		fingerprint := canonicalFingerprint(in, generation, candidate)
		if candidate == salt && fingerprint == previous {
			return nil
		}
		var clash bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_signatureavatar WHERE fingerprint=$1 AND user_id<>$2)`, fingerprint, a.ID).Scan(&clash); err != nil {
			return err
		}
		if clash {
			continue
		}
		if _, err = tx.Exec(ctx, `SAVEPOINT refresh_avatar`); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE accounts_signatureavatar SET salt=$2,fingerprint=$3,updated_at=now() WHERE user_id=$1`, a.ID, candidate, fingerprint)
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT refresh_avatar`)
			if errors.Is(storeError(err), authcore.ErrConflict) {
				continue
			}
			return err
		}
		_, err = tx.Exec(ctx, `RELEASE SAVEPOINT refresh_avatar`)
		return err
	}
	return nil
}
