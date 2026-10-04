package booking

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// UnmarshalJSON retains the original serializer's distinction between an omitted
// party size (one) and an explicitly invalid zero, and accepts local ISO dates in
// the app's configured Europe/Bucharest timezone.
func (input *Request) UnmarshalJSON(raw []byte) error {
	var body struct {
		Place     int64           `json:"place"`
		Activity  *int64          `json:"activity"`
		StartsAt  string          `json:"starts_at"`
		EndsAt    *string         `json:"ends_at"`
		PartySize json.RawMessage `json:"party_size"`
		Provider  string          `json:"provider"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		return platform.ErrInvalid
	}
	start, err := parseBookingDate(body.StartsAt)
	if err != nil {
		return err
	}
	var end *time.Time
	if body.EndsAt != nil {
		parsed, err := parseBookingDate(*body.EndsAt)
		if err != nil {
			return err
		}
		end = &parsed
	}
	party := 1
	if len(body.PartySize) > 0 {
		if bytes.Equal(body.PartySize, []byte("null")) || json.Unmarshal(body.PartySize, &party) != nil {
			return platform.ErrInvalid
		}
	}
	*input = Request{Place: body.Place, Activity: body.Activity, StartsAt: start, EndsAt: end, PartySize: party, Provider: body.Provider}
	return nil
}

func parseBookingDate(raw string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, nil
	}
	zone, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return time.Time{}, err
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04"} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(raw), zone); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, platform.ErrInvalid
}
