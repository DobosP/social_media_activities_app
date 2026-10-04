package web

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func (s *Server) paymentView(r *http.Request, a platform.Actor, name string) (pongo2.Context, string, error) {
	if s.Donations == nil {
		return nil, "", errors.New("donation service unavailable")
	}
	data := pongo2.Context{}
	if name == "my_donations" {
		mine, err := s.Donations.Mine(r.Context(), a)
		data["donations"] = mine
		return data, "web/my_donations.html", err
	}
	ledger, err := s.Donations.Ledger(r.Context(), "EUR")
	if err != nil {
		return nil, "", err
	}
	switch name {
	case "donate":
		choices, err := s.Donations.CampaignChoices(r.Context())
		if err != nil {
			return nil, "", err
		}
		data["form"] = donateForm(r, choices, "")
		data["cost_anchors"] = ledger["cost_anchors"]
	case "transparency":
		data["currency"] = "EUR"
		data["raised_cents"] = ledger["total_cents"]
		data["spend_rows"] = ledger["spend"]
		data["spend_total_cents"] = ledger["spent_cents"]
		data["in_kind_rows"] = ledger["in_kind"]
		data["civic_outcomes"] = ledger["civic_outcomes"]
	case "campaigns":
		data["campaigns"] = ledger["campaigns"]
		data["completed_campaigns"] = ledger["completed_campaigns"]
	case "partners":
		data["partners"] = ledger["partners"]
	default:
		return nil, "", platform.ErrNotFound
	}
	return data, "web/" + name + ".html", nil
}

func donateForm(r *http.Request, choices []map[string]any, message string) map[string]any {
	amount, campaign := "10", ""
	if r.Method == http.MethodPost {
		amount, campaign = r.PostForm.Get("amount"), r.PostForm.Get("campaign")
	} else if slug := r.URL.Query().Get("campaign"); slug != "" {
		for _, choice := range choices {
			if choice["slug"] == slug {
				campaign = fmt.Sprint(choice["id"])
				break
			}
		}
	}
	var html strings.Builder
	if message != "" {
		html.WriteString(`<ul class="errorlist"><li>` + escape(message) + `</li></ul>`)
	}
	html.WriteString(`<p><label for="id_amount">Amount (EUR):</label> <input type="number" name="amount" value="` + escape(amount) + `" min="1" step="0.01" required id="id_amount"></p>`)
	html.WriteString(`<p><label for="id_campaign">Direct your gift to:</label> <select name="campaign" id="id_campaign"><option value="">General fund (where it's needed most)</option>`)
	for _, choice := range choices {
		value := fmt.Sprint(choice["id"])
		selected := ""
		if value == campaign {
			selected = " selected"
		}
		html.WriteString(`<option value="` + escape(value) + `"` + selected + `>` + escape(fmt.Sprint(choice["title"])) + `</option>`)
	}
	html.WriteString(`</select></p>`)
	return map[string]any{"as_p": pongo2.AsSafeValue(html.String()), "errors": message}
}

// Decimal form amounts are converted with integer arithmetic. Rounding an
// invalid three-decimal input would silently change the donor's chosen amount.
var donationDecimal = regexp.MustCompile(`^\+?((?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))(?:[eE]([+-]?[0-9]{1,3}))?$`)

func donationCents(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 128 {
		return 0, platform.ErrInvalid
	}
	match := donationDecimal.FindStringSubmatch(raw)
	if match == nil {
		return 0, platform.ErrInvalid
	}
	parts := strings.Split(match[1], ".")
	exponent := 0
	if match[2] != "" {
		exponent, _ = strconv.Atoi(match[2])
	}
	scale := 0
	if len(parts) == 2 {
		scale = len(parts[1])
	}
	scale -= exponent
	if scale > 2 {
		return 0, platform.ErrInvalid
	}
	digits := strings.TrimLeft(strings.Join(parts, ""), "0")
	if digits == "" {
		digits = "0"
	}
	total := len(digits)
	if scale < 0 {
		total -= scale
	} else {
		total = max(total, scale)
	}
	if total > 8 || total-max(0, scale) > 6 {
		return 0, platform.ErrInvalid
	}
	cents, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, platform.ErrInvalid
	}
	for i := scale; i < 2; i++ {
		cents *= 10
	}
	if cents < 100 || cents > 99999999 {
		return 0, platform.ErrInvalid
	}
	return cents, nil
}

// donateAction is invoked after the common legacy form/CSRF admission.
func (s *Server) donateAction(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	if s.Donations == nil {
		platform.Error(w, 503, "Donations unavailable.")
		return
	}
	amount, err := donationCents(r.PostForm.Get("amount"))
	var campaign *int64
	if raw := r.PostForm.Get("campaign"); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || parsed <= 0 {
			err = platform.ErrInvalid
		} else {
			campaign = &parsed
		}
	}
	message := "Enter a valid amount of at least 1.00 EUR with at most two decimal places."
	if err == nil {
		var result map[string]any
		result, err = s.Donations.Start(r.Context(), a, donations.Request{Amount: amount, Currency: "EUR", Campaign: campaign})
		if err == nil {
			http.Redirect(w, r, result["checkout_url"].(string), http.StatusFound)
			return
		}
		if errors.Is(err, donations.ErrProvider) {
			message = "The payment provider is temporarily unavailable. Please try again."
		} else if errors.Is(err, platform.ErrInvalid) {
			message = "Select a valid active campaign."
		} else {
			platform.Fail(w, err)
			return
		}
	}
	data, template, viewErr := s.paymentView(r, a, "donate")
	if viewErr != nil {
		platform.Fail(w, viewErr)
		return
	}
	choices, viewErr := s.Donations.CampaignChoices(r.Context())
	if viewErr != nil {
		platform.Fail(w, viewErr)
		return
	}
	data["form"] = donateForm(r, choices, message)
	data["csrf"] = s.Auth.EnsureCSRF(w, r)
	if err := s.Renderer.Render(w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}
