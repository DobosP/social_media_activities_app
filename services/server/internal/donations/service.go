package donations

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Provider, CheckoutURL, StripeSecret, StripeWebhookSecret, WebhookSecret, SuccessURL, CancelURL string
	Client                                                                                         *http.Client
	Now                                                                                            func() time.Time
	CostAnchorsMax                                                                                 *int
	Adapter                                                                                        PaymentProvider
}

// PaymentProvider is the native extension seam for an externally hosted checkout.
// It receives no donor identity or payment details.
type PaymentProvider interface {
	Name() string
	CreateIntent(context.Context, int64, string, string) (checkoutURL, externalRef string, err error)
}

type Request struct {
	Amount    int64  `json:"amount_cents"`
	Currency  string `json:"currency"`
	Recurring bool   `json:"recurring"`
	Campaign  *int64 `json:"campaign"`
}

var ErrProvider = errors.New("payment provider unavailable")

type Service struct {
	DB     *pgxpool.Pool
	Config Config
	HTTP   *ops.Resilient
}

func New(db *pgxpool.Pool, c Config) *Service {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Provider == "" {
		c.Provider = "deeplink"
	}
	for path, name := range map[string]string{"apps.donations.providers.DeepLinkProvider": "deeplink", "apps.donations.providers.DevPaymentProvider": "dev", "apps.donations.providers.StripePaymentProvider": "stripe"} {
		if c.Provider == path {
			c.Provider = name
		}
	}
	if c.Adapter != nil {
		c.Provider = c.Adapter.Name()
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 {
		client.Timeout = 15 * time.Second
	}
	return &Service{DB: db, Config: c, HTTP: ops.NewResilient(&client)}
}
func (s *Service) Register(mux *http.ServeMux) {
	for _, base := range []string{"/api/donations", "/api/v1/donations"} {
		registerRoute(mux, "POST "+base+"/", s.start)
		registerRoute(mux, "GET "+base+"/mine/", s.mine)
		registerRoute(mux, "GET "+base+"/total/", s.total)
		registerRoute(mux, "POST "+base+"/webhook/", s.webhook)
	}
}

const projection = `jsonb_build_object('id',id,'amount_cents',amount_cents,'currency',currency,'recurring',recurring,'campaign',campaign_id,'provider',provider,'status',status,'created_at',created_at)`

func safeURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(raw, "\\\r\n")
}
func (s *Service) intent(ctx context.Context, reference string, amount int64, currency string) (string, string, error) {
	if s.Config.Adapter != nil {
		return s.Config.Adapter.CreateIntent(ctx, amount, currency, reference)
	}
	switch s.Config.Provider {
	case "dev":
		return "dev://checkout/" + reference, reference, nil
	case "deeplink":
		base := s.Config.CheckoutURL
		if base == "" {
			base = "https://example.org/donate"
		}
		if !safeURL(base) {
			return "", "", platform.ErrInvalid
		}
		u, _ := url.Parse(base)
		q := u.Query()
		q.Set("ref", reference)
		q.Set("amount", strconv.FormatInt(amount, 10))
		q.Set("currency", currency)
		u.RawQuery = q.Encode()
		return u.String(), reference, nil
	case "stripe":
		if s.Config.StripeSecret == "" || !safeURL(s.Config.SuccessURL) || !safeURL(s.Config.CancelURL) {
			return "", "", platform.ErrForbidden
		}
		data := url.Values{"mode": {"payment"}, "success_url": {s.Config.SuccessURL}, "cancel_url": {s.Config.CancelURL}, "client_reference_id": {reference}, "line_items[0][quantity]": {"1"}, "line_items[0][price_data][currency]": {strings.ToLower(currency)}, "line_items[0][price_data][unit_amount]": {strconv.FormatInt(amount, 10)}, "line_items[0][price_data][product_data][name]": {"Donation"}}
		r, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.stripe.com/v1/checkout/sessions", strings.NewReader(data.Encode()))
		if e != nil {
			return "", "", e
		}
		r.SetBasicAuth(s.Config.StripeSecret, "")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Idempotency-Key", reference)
		resp, e := s.HTTP.Do(ctx, r, ops.RequestOptions{MaxAttempts: 3, RetryStatuses: []int{500, 502, 503, 504}, RetryTimeouts: true, BreakerKey: "stripe", MaxBodyBytes: 1 << 20})
		if e != nil {
			return "", "", e
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", "", platform.ErrInvalid
		}
		var body struct {
			URL string `json:"url"`
			ID  string `json:"id"`
		}
		decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || !safeURL(body.URL) || body.ID == "" {
			return "", "", platform.ErrInvalid
		}
		return body.URL, body.ID, nil
	}
	return "", "", platform.ErrInvalid
}

// Start is shared by the JSON and legacy form controllers. Campaign admission,
// checkout intent and pending receipt commit as one domain operation.
func (s *Service) Start(ctx context.Context, actor platform.Actor, body Request) (map[string]any, error) {
	if body.Amount < 100 || body.Amount > 2147483647 || len(s.Config.Provider) == 0 || len(s.Config.Provider) > 64 {
		return nil, platform.ErrInvalid
	}
	if body.Currency == "" {
		body.Currency = "EUR"
	}
	body.Currency = strings.TrimSpace(body.Currency)
	if utf8.RuneCountInString(body.Currency) == 0 || utf8.RuneCountInString(body.Currency) > 3 {
		return nil, platform.ErrInvalid
	}
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return nil, e
	}
	ref := hex.EncodeToString(random[:])
	var donor any
	if actor.ID > 0 && actor.IsActive {
		donor = actor.ID
	}
	var row json.RawMessage
	var checkout string
	e := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if body.Campaign != nil {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT is_active FROM donations_campaign WHERE id=$1 FOR SHARE`, *body.Campaign).Scan(&active); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return platform.ErrInvalid
				}
				return err
			}
			if !active {
				return platform.ErrInvalid
			}
		}
		var external string
		var err error
		checkout, external, err = s.intent(ctx, ref, body.Amount, body.Currency)
		if err != nil || external == "" || len(external) > 128 || !(safeURL(checkout) || s.Config.Provider == "dev" && strings.HasPrefix(checkout, "dev://checkout/")) {
			return ErrProvider
		}
		return tx.QueryRow(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,recurring,campaign_id,provider,status,external_ref,created_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,now(),NULL) RETURNING `+projection, donor, body.Amount, body.Currency, body.Recurring, body.Campaign, s.Config.Provider, external).Scan(&row)
	})
	if e != nil {
		return nil, e
	}
	var result map[string]any
	_ = json.Unmarshal(row, &result)
	result["checkout_url"] = checkout
	return result, nil
}
func (s *Service) start(w http.ResponseWriter, r *http.Request) {
	var body Request
	if platform.Decode(w, r, &body) != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	a, _ := platform.ActorFrom(r)
	result, err := s.Start(r.Context(), a, body)
	if errors.Is(err, ErrProvider) {
		platform.Error(w, 400, "The payment provider is temporarily unavailable. Please try again.")
		return
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 201, result)
}
func (s *Service) mine(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	rows, e := s.DB.Query(r.Context(), `SELECT `+projection+` FROM donations_donation WHERE donor_id=$1 ORDER BY created_at DESC,id DESC`, a.ID)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var row json.RawMessage
		if e = rows.Scan(&row); e != nil {
			platform.Fail(w, e)
			return
		}
		result = append(result, row)
	}
	if e = rows.Err(); e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, result)
}
func (s *Service) total(w http.ResponseWriter, r *http.Request) {
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = "EUR"
	}
	var total int64
	e := s.DB.QueryRow(r.Context(), `SELECT coalesce(sum(amount_cents),0) FROM donations_donation WHERE status='completed' AND currency=$1`, currency).Scan(&total)
	if e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, map[string]any{"currency": currency, "total_cents": total})
}
func validStripe(raw []byte, header, secret string, now time.Time) bool {
	if secret == "" || len(header) > 2048 {
		return false
	}
	var stamp string
	var signatures []string
	for _, part := range strings.Split(header, ",") {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		if strings.TrimSpace(pair[0]) == "t" {
			stamp = strings.TrimSpace(pair[1])
		}
		if strings.TrimSpace(pair[0]) == "v1" {
			signatures = append(signatures, strings.TrimSpace(pair[1]))
		}
	}
	seconds, e := strconv.ParseInt(stamp, 10, 64)
	if e != nil || now.Sub(time.Unix(seconds, 0)) > 5*time.Minute || time.Unix(seconds, 0).Sub(now) > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(raw)
	wanted := hex.EncodeToString(mac.Sum(nil))
	for _, sig := range signatures {
		if len(sig) == len(wanted) && subtle.ConstantTimeCompare([]byte(sig), []byte(wanted)) == 1 {
			return true
		}
	}
	return false
}
func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if e != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	var ref string
	stripeMode := s.Config.Provider == "stripe" && s.Config.StripeWebhookSecret != ""
	if s.Config.Provider == "stripe" && s.Config.StripeWebhookSecret != "" {
		if !validStripe(raw, r.Header.Get("Stripe-Signature"), s.Config.StripeWebhookSecret, s.Config.Now()) {
			platform.Fail(w, platform.ErrForbidden)
			return
		}
		var event struct {
			Type string `json:"type"`
			Data struct {
				Object struct {
					ID     string `json:"id"`
					Status string `json:"payment_status"`
				} `json:"object"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &event) != nil {
			platform.Fail(w, platform.ErrInvalid)
			return
		}
		if event.Type != "checkout.session.completed" || event.Data.Object.ID == "" {
			platform.JSON(w, 200, map[string]string{"status": "ignored"})
			return
		}
		// A completed but unpaid checkout is an asynchronous payment, not a paid
		// donation. This native safety hardening prevents false public totals.
		if event.Data.Object.Status != "paid" {
			platform.JSON(w, 200, map[string]string{"status": "ignored"})
			return
		}
		ref = event.Data.Object.ID
	} else {
		given := r.Header.Get("X-Webhook-Secret")
		secret := s.Config.WebhookSecret
		if secret == "" || len(given) != len(secret) || subtle.ConstantTimeCompare([]byte(given), []byte(secret)) != 1 {
			platform.Fail(w, platform.ErrForbidden)
			return
		}
		var body struct {
			Ref string `json:"external_ref"`
		}
		if json.Unmarshal(raw, &body) != nil {
			platform.Fail(w, platform.ErrInvalid)
			return
		}
		ref = strings.TrimSpace(body.Ref)
	}
	if ref == "" || len(ref) > 128 {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	var donation json.RawMessage
	e = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		// Provider is included for a Stripe event: a different adapter cannot
		// accidentally complete a donation with a coincidentally equal reference.
		return tx.QueryRow(r.Context(), `UPDATE donations_donation SET status='completed',completed_at=now() WHERE id=(SELECT id FROM donations_donation WHERE external_ref=$1 AND status='pending' AND (NOT $2 OR provider='stripe') ORDER BY id LIMIT 1 FOR UPDATE) RETURNING `+projection, ref, stripeMode).Scan(&donation)
	})
	if e == pgx.ErrNoRows {
		platform.JSON(w, 200, map[string]string{"status": "ignored"})
		return
	}
	if e != nil {
		platform.Fail(w, e)
		return
	}
	platform.JSON(w, 200, donation)
}

func registerRoute(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, handler)
}
