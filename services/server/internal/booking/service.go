package booking

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Request struct {
	Place     int64      `json:"place"`
	Activity  *int64     `json:"activity"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	PartySize int        `json:"party_size"`
	Provider  string     `json:"provider"`
}
type Result struct {
	Reference string
	Confirmed bool
}
type Provider interface {
	Create(context.Context, string, string, Request) (Result, error)
	Cancel(context.Context, string) error
}

// A native registry override can advertise a deep-link-only capability while
// sharing the adapter seam. Existing realtime adapters need no extra method.
type capabilities interface{ SupportsRealtime() bool }

func realtime(provider Provider) bool {
	if provider == nil {
		return false
	}
	if cap, ok := provider.(capabilities); ok {
		return cap.SupportsRealtime()
	}
	return true
}

type Service struct {
	DB        *pgxpool.Pool
	Providers map[string]Provider
}

type Config struct {
	DemoBaseURL, DemoAPIKey string
	Client                  *http.Client
	Providers               map[string]Provider
}

func New(db *pgxpool.Pool) *Service {
	return NewConfigured(db, Config{})
}
func NewConfigured(db *pgxpool.Pool, c Config) *Service {
	providers := map[string]Provider{"demo_rest": &RESTProvider{BaseURL: c.DemoBaseURL, APIKey: c.DemoAPIKey, Client: c.Client}}
	for slug, provider := range c.Providers {
		providers[slug] = provider
	}
	return &Service{db, providers}
}
func (s *Service) Register(mux *http.ServeMux) {
	for _, prefix := range []string{"/api/booking", "/api/v1/booking"} {
		registerRoute(mux, "GET "+prefix+"/options/", s.options)
		registerRoute(mux, "GET "+prefix+"/providers/", s.providers)
		registerRoute(mux, "GET "+prefix+"/bookings/", s.list)
		registerRoute(mux, "POST "+prefix+"/bookings/", s.create)
		registerRoute(mux, "GET "+prefix+"/bookings/{id}/", s.detail)
		registerRoute(mux, "POST "+prefix+"/bookings/{id}/cancel/", s.cancel)
	}
}

const projection = `jsonb_build_object('id',id,'place',place_id,'activity',activity_id,'provider',provider,'external_ref',external_ref,'status',status,'starts_at',starts_at,'ends_at',ends_at,'party_size',party_size,'deep_link',deep_link,'created_at',created_at)`

func (s *Service) info(ctx context.Context, q platform.Querier, place int64) (provider, link, instructions, external string, err error) {
	err = q.QueryRow(ctx, `SELECT coalesce(b.provider,'deeplink'),coalesce(b.deep_link,''),coalesce(b.instructions,''),coalesce(nullif(b.provider_place_ref,''),p.id::text) FROM places_place p LEFT JOIN booking_placebookinginfo b ON b.place_id=p.id WHERE p.id=$1`, place).Scan(&provider, &link, &instructions, &external)
	return
}
func (s *Service) options(w http.ResponseWriter, r *http.Request) {
	_, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	place, err := strconv.ParseInt(r.URL.Query().Get("place"), 10, 64)
	if err != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	provider, link, instructions, _, err := s.info(r.Context(), s.DB, place)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if provider != "deeplink" && s.Providers[provider] == nil {
		platform.Error(w, 502, "Booking provider unavailable.")
		return
	}
	platform.JSON(w, 200, map[string]any{"provider": provider, "bookable_in_app": realtime(s.Providers[provider]), "deep_link": link, "instructions": instructions})
}
func (s *Service) providers(w http.ResponseWriter, r *http.Request) {
	if _, ok := platform.RequireActor(w, r); !ok {
		return
	}
	data := []any{}
	slugs := []string{"deeplink"}
	for slug := range s.Providers {
		if slug != "deeplink" {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		data = append(data, map[string]any{"slug": slug, "supports_realtime": realtime(s.Providers[slug])})
	}
	platform.JSON(w, 200, data)
}
func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	var input Request
	if platform.Decode(w, r, &input) != nil || input.Place <= 0 || input.StartsAt.IsZero() {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	input.Provider = strings.TrimSpace(input.Provider)
	if input.PartySize < 1 || input.PartySize > 2147483647 || utf8.RuneCountInString(input.Provider) > 32 {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	if err := platform.Participate(r.Context(), s.DB, a); err != nil {
		platform.Fail(w, err)
		return
	}
	var data json.RawMessage
	err := platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		// Reload account facts inside the mutation transaction. An already issued
		// session cannot book after staff revoke assurance or cohort eligibility.
		current := a
		if err := tx.QueryRow(r.Context(), `SELECT public_id::text,cohort,age_band,is_identity_verified,is_active FROM accounts_user WHERE id=$1 FOR SHARE`, a.ID).Scan(&current.PublicID, &current.Cohort, &current.AgeBand, &current.IdentityVerified, &current.IsActive); err != nil {
			return err
		}
		a = current
		if err := platform.Participate(r.Context(), tx, a); err != nil {
			return err
		}
		if input.Activity != nil {
			var member bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM social_membership m JOIN social_activity x ON x.id=m.activity_id WHERE m.user_id=$1 AND m.activity_id=$2 AND m.state='member' AND (x.cohort=$3 OR ($3='adult' AND x.cohort='child' AND m.role='guardian' AND EXISTS(SELECT 1 FROM accounts_guardianrelationship rel JOIN social_membership ward_member ON ward_member.user_id=rel.ward_id AND ward_member.activity_id=x.id AND ward_member.state='member' AND ward_member.role<>'guardian' JOIN accounts_user ward ON ward.id=rel.ward_id WHERE rel.guardian_id=$1 AND rel.status='active' AND ward.is_active AND ward.is_identity_verified AND ward.cohort='child' AND EXISTS(SELECT 1 FROM accounts_parentalconsent pc WHERE pc.minor_id=ward.id AND pc.status='active' AND (pc.expires_at IS NULL OR pc.expires_at>now())) AND coalesce((SELECT expires_at IS NULL OR expires_at>now() FROM accounts_ageassurance aa WHERE aa.user_id=ward.id ORDER BY aa.verified_at DESC,aa.id DESC LIMIT 1),true)))))`, a.ID, *input.Activity, a.Cohort).Scan(&member); err != nil {
				return err
			}
			if !member {
				return platform.ErrForbidden
			}
		}
		provider, link, _, external, err := s.info(r.Context(), tx, input.Place)
		if err != nil {
			return err
		}
		if input.Provider != "" {
			provider = input.Provider
		}
		adapter := s.Providers[provider]
		if provider != "deeplink" && adapter == nil {
			return errProvider
		}
		result := Result{}
		if realtime(adapter) {
			link = ""
			result, err = adapter.Create(r.Context(), external, a.PublicID, input)
			if err != nil || result.Reference == "" || len(result.Reference) > 128 {
				return errProvider
			}
		}
		status := "pending"
		if result.Confirmed {
			status = "confirmed"
		}
		return tx.QueryRow(r.Context(), `INSERT INTO booking_booking(user_id,place_id,activity_id,provider,external_ref,status,starts_at,ends_at,party_size,deep_link,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now(),now()) RETURNING `+projection, a.ID, input.Place, input.Activity, provider, result.Reference, status, input.StartsAt, input.EndsAt, input.PartySize, link).Scan(&data)
	})
	if err != nil {
		if err == errProvider {
			platform.Error(w, 502, "Booking provider unavailable.")
			return
		}
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 201, data)
}
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 50
	}
	limit = min(200, limit)
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}
	var count int64
	if err = s.DB.QueryRow(r.Context(), `SELECT count(*) FROM booking_booking WHERE user_id=$1`, a.ID).Scan(&count); err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT `+projection+` FROM booking_booking WHERE user_id=$1 ORDER BY starts_at DESC,id DESC LIMIT $2 OFFSET $3`, a.ID, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var row json.RawMessage
		if err = rows.Scan(&row); err != nil {
			platform.Fail(w, err)
			return
		}
		result = append(result, row)
	}
	if err = rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	var next, previous any
	if int64(offset) < count && count-int64(offset) > int64(limit) {
		next = pageURL(r, limit, offset+limit)
	}
	if offset > 0 {
		previous = pageURL(r, limit, max(0, offset-limit))
	}
	platform.JSON(w, 200, map[string]any{"count": count, "next": next, "previous": previous, "results": result})
}

func pageURL(r *http.Request, limit, offset int) string {
	u := *r.URL
	q := u.Query()
	q.Set("limit", strconv.Itoa(limit))
	q.Del("offset")
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	u.RawQuery = q.Encode()
	if u.Host == "" {
		u.Host = r.Host
	}
	if u.Scheme == "" {
		u.Scheme = "http"
		if r.TLS != nil {
			u.Scheme = "https"
		}
	}
	return u.String()
}
func (s *Service) detail(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	var row json.RawMessage
	err = s.DB.QueryRow(r.Context(), `SELECT `+projection+` FROM booking_booking WHERE id=$1 AND user_id=$2`, id, a.ID).Scan(&row)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, row)
}
func (s *Service) cancel(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	providerFailed := false
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		var provider, reference, status string
		if err := tx.QueryRow(r.Context(), `SELECT provider,external_ref,status FROM booking_booking WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, a.ID).Scan(&provider, &reference, &status); err != nil {
			return err
		}
		if status == "cancelled" {
			return nil
		}
		adapter := s.Providers[provider]
		if provider != "deeplink" && adapter == nil {
			return errProvider
		}
		if realtime(adapter) && reference != "" {
			if err := adapter.Cancel(r.Context(), reference); err != nil {
				providerFailed = true
			}
		}
		status = "cancelled"
		if providerFailed {
			status = "failed"
		}
		_, err := tx.Exec(r.Context(), `UPDATE booking_booking SET status=$3,updated_at=now() WHERE id=$1 AND user_id=$2`, id, a.ID, status)
		return err
	})
	if err != nil {
		if err == errProvider {
			platform.Error(w, 502, "Booking provider unavailable.")
		} else {
			platform.Fail(w, err)
		}
		return
	}
	if providerFailed {
		platform.Error(w, 502, "Booking provider unavailable.")
		return
	}
	s.detail(w, r)
}

func registerRoute(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, handler)
}
