package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

const connectionRowsLimit = 200 // Both source service queries have LIMIT 200.

// Two projected users have UUID36 and display_name120/username150 at most.
// Even fully escaped Unicode plus fixed keys, bigint19, status12 and timestamp
// fits below 8 KiB. This bounds decoding, without truncating any source value.
const connectionRowBytesLimit = 8 << 10

var errConnectionRow = errors.New("invalid connection row")

func connectionsPageContext(ctx context.Context, db platform.Querier, accepted, incoming, outgoing []json.RawMessage) (pongo2.Context, error) {
	projected, err := connectionsContext(accepted, incoming, outgoing)
	if err != nil {
		return nil, err
	}
	users, err := connectionAvatars(ctx, db, projected["connections"].([]map[string]any))
	if err != nil {
		return nil, err
	}
	projected["connections"] = users
	return projected, nil
}

// Resolve only the public IDs already admitted by Service.Connections. Empty
// lists need no query; every nonempty list uses this one join and the existing
// two-query base-avatar helper. Internal IDs never enter the template context.
func connectionAvatars(ctx context.Context, db platform.Querier, users []map[string]any) ([]map[string]any, error) {
	if ctx == nil || ctx.Err() != nil || len(users) > connectionRowsLimit {
		return nil, errors.New("connections avatars: unavailable input")
	}
	if len(users) == 0 {
		return []map[string]any{}, nil
	}
	if db == nil {
		return nil, errors.New("connections avatars: unavailable database")
	}
	publicIDs := make([]string, 0, len(users))
	selected := make(map[string]bool, len(users))
	for _, user := range users {
		publicID, ok := user["public_id"].(string)
		if !ok || publicID == "" || len(user) != 2 || selected[publicID] {
			return nil, errors.New("connections avatars: invalid selected identity")
		}
		if _, ok := user["display_name"].(string); !ok {
			return nil, errors.New("connections avatars: invalid selected display")
		}
		selected[publicID] = true
		publicIDs = append(publicIDs, publicID)
	}
	rows, err := db.Query(ctx, `SELECT id,public_id::text FROM accounts_user WHERE public_id::text=ANY($1)`, publicIDs)
	if err != nil {
		return nil, errors.New("connections avatars: identity lookup failed")
	}
	byPublicID := make(map[string]int64, len(users))
	seenIDs := make(map[int64]bool, len(users))
	for rows.Next() {
		var id int64
		var publicID string
		if rows.Scan(&id, &publicID) != nil || id <= 0 || !selected[publicID] || byPublicID[publicID] != 0 || seenIDs[id] {
			rows.Close()
			return nil, errors.New("connections avatars: identity join differs")
		}
		byPublicID[publicID] = id
		seenIDs[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(byPublicID) != len(users) {
		return nil, errors.New("connections avatars: incomplete identity lookup")
	}
	ids := make([]int64, len(users))
	for index, publicID := range publicIDs {
		ids[index] = byPublicID[publicID]
	}
	// accounts_user.id and accounts_signatureavatar.user_id are unique in the
	// actual schema. The existing helper returns a map; validate its complete
	// result, without pretending duplicate map keys remain observable here.
	avatars, err := accounts.Avatars(ctx, db, ids)
	if err != nil || len(avatars) != len(ids) {
		return nil, errors.New("connections avatars: incomplete avatar result")
	}
	for id, uri := range avatars {
		if !seenIDs[id] || !connectionAvatarURI(uri) {
			return nil, errors.New("connections avatars: avatar identity or URI differs")
		}
	}
	result := make([]map[string]any, len(users))
	for index, user := range users {
		result[index] = map[string]any{"public_id": user["public_id"], "display_name": user["display_name"], "avatar_uri": avatars[ids[index]]}
	}
	return result, nil
}

func connectionAvatarURI(uri string) bool {
	const prefix = "data:image/svg+xml;base64,"
	if !strings.HasPrefix(uri, prefix) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	return err == nil && len(raw) > 0 && utf8.Valid(raw) && bytes.HasPrefix(raw, []byte("<svg")) && base64.StdEncoding.EncodeToString(raw) == strings.TrimPrefix(uri, prefix)
}

// All three arrays are decoded before any context is returned. Domain queries
// remain responsible for membership/cohort/block policy; this is representation
// only, with no fallback, omitted row, or caller-supplied conversion policy.
func connectionsContext(accepted, incoming, outgoing []json.RawMessage) (pongo2.Context, error) {
	result := pongo2.Context{}
	for _, selected := range []struct {
		name    string
		rows    []json.RawMessage
		pending bool
	}{{"connections", accepted, false}, {"incoming", incoming, true}, {"outgoing", outgoing, true}} {
		if len(selected.rows) > connectionRowsLimit {
			return nil, fmt.Errorf("connections context: too many %s rows", selected.name)
		}
		rows := make([]map[string]any, 0, len(selected.rows))
		for index, raw := range selected.rows {
			var value map[string]any
			var err error
			if selected.pending {
				value, err = connectionPendingRow(raw)
			} else {
				value, err = connectionUserRow(raw)
			}
			if err != nil {
				return nil, fmt.Errorf("connections context: invalid %s row %d", selected.name, index)
			}
			rows = append(rows, value)
		}
		result[selected.name] = rows
	}
	return result, nil
}

func connectionUserRow(raw []byte) (map[string]any, error) {
	fields, err := connectionFields(raw, "public_id", "display_name")
	if err != nil {
		return nil, err
	}
	publicID, err := connectionString(fields["public_id"], 36)
	if err != nil || publicID == "" {
		return nil, errConnectionRow
	}
	name, err := connectionString(fields["display_name"], 150)
	if err != nil {
		return nil, err
	}
	return map[string]any{"public_id": publicID, "display_name": name}, nil
}

func connectionPendingRow(raw []byte) (map[string]any, error) {
	fields, err := connectionFields(raw, "id", "status", "requester", "addressee", "created_at")
	if err != nil {
		return nil, err
	}
	number := bytes.TrimSpace(fields["id"])
	if len(number) == 0 || len(number) > 19 || number[0] < '1' || number[0] > '9' {
		return nil, errConnectionRow
	}
	for _, digit := range number {
		if digit < '0' || digit > '9' {
			return nil, errConnectionRow
		}
	}
	id, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || id <= 0 {
		return nil, errConnectionRow
	}
	status, err := connectionString(fields["status"], 12)
	if err != nil || status != "pending" {
		return nil, errConnectionRow
	}
	created, err := connectionString(fields["created_at"], 64)
	if err != nil || created == "" {
		return nil, errConnectionRow
	}
	requester, err := connectionUserRow(fields["requester"])
	if err != nil {
		return nil, err
	}
	addressee, err := connectionUserRow(fields["addressee"])
	if err != nil {
		return nil, err
	}
	// The immutable template uses c.pk; the service projection calls it id.
	// Keep both as the same exact int64, never a float or platform-sized int.
	return map[string]any{"id": id, "pk": id, "status": status, "requester": requester, "addressee": addressee, "created_at": created}, nil
}

func connectionFields(raw []byte, names ...string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > connectionRowBytesLimit || !utf8.Valid(raw) || !json.Valid(raw) || !connectionUnicodeEscapes(raw) {
		return nil, errConnectionRow
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errConnectionRow
	}
	fields := make(map[string]json.RawMessage, len(names))
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || !allowed[name] || fields[name] != nil {
			return nil, errConnectionRow
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, errConnectionRow
		}
		fields[name] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(fields) != len(names) || decoder.Decode(new(any)) != io.EOF {
		return nil, errConnectionRow
	}
	return fields, nil
}

func connectionString(raw []byte, maximum int) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' {
		return "", errConnectionRow
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || utf8.RuneCountInString(value) > maximum {
		return "", errConnectionRow
	}
	return value, nil
}

// encoding/json accepts unpaired UTF-16 escapes by replacing them with U+FFFD.
// Check the original valid JSON spelling first so the view never changes data
// silently. Valid surrogate pairs and literal/escaped U+FFFD remain unchanged.
func connectionUnicodeEscapes(raw []byte) bool {
	inside := false
	for index := 0; index < len(raw); index++ {
		if raw[index] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[index] != '\\' {
			continue
		}
		if index+1 >= len(raw) {
			return false
		}
		if raw[index+1] != 'u' {
			index++
			continue
		}
		if index+6 > len(raw) {
			return false
		}
		unit, err := strconv.ParseUint(string(raw[index+2:index+6]), 16, 16)
		if err != nil || unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit >= 0xd800 && unit <= 0xdbff {
			if index+12 > len(raw) || raw[index+6] != '\\' || raw[index+7] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[index+8:index+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			index += 6
		}
		index += 5
	}
	return !inside
}
