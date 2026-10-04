package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// CommonsClient's optional HTTP transport is an offline test seam. The normal
// transport checks all DNS results and pins a checked address before connecting.
type CommonsClient struct {
	APIURL, UserAgent, TempDir string
	HTTP                       *http.Client
}
type CoverImporter func(context.Context, int64, string, string, string, string, string) (int64, error)

func CommonsFileTitle(tags map[string]any) string {
	value, _ := tags["wikimedia_commons"].(string)
	if strings.HasPrefix(value, "File:") && text(value, 512, false) {
		return value
	}
	image, _ := tags["image"].(string)
	u, err := url.Parse(image)
	if err == nil && u.User == nil && u.Scheme == "https" && (u.Hostname() == "commons.wikimedia.org" || u.Hostname() == "upload.wikimedia.org") {
		if i := strings.Index(u.Path, "File:"); i >= 0 {
			value = u.Path[i:]
			if text(value, 512, false) {
				return value
			}
		}
	}
	return ""
}

func (c *CommonsClient) fetch(ctx context.Context, raw string, maximum int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "https" || u.Hostname() == "" || u.Fragment != "" {
		return nil, errors.New("unsafe Commons URL")
	}
	timeout := 30 * time.Second
	if maximum > 2<<20 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := c.HTTP
	if client == nil {
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("Commons DNS unavailable")
		}
		for _, a := range addresses {
			if !publicIP(a.IP) {
				return nil, errors.New("unsafe Commons address")
			}
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		}
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport, Timeout: timeout}
	}
	// The injected client may have permissive redirects. A shallow copy preserves
	// its test transport while keeping every redirect disabled.
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, errors.New("invalid Commons request")
	}
	agent := c.UserAgent
	if agent == "" {
		agent = "social-activities-app"
	}
	req.Header.Set("User-Agent", agent)
	response, err := copyClient.Do(req)
	if err != nil {
		return nil, errors.New("Commons unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("Commons unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || int64(len(body)) > maximum {
		return nil, errors.New("Commons response exceeds limit")
	}
	return body, nil
}
func (c *CommonsClient) api(ctx context.Context, params url.Values) (map[string]any, error) {
	endpoint := c.APIURL
	if endpoint == "" {
		endpoint = "https://commons.wikimedia.org/w/api.php"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("invalid Commons endpoint")
	}
	params.Set("format", "json")
	u.RawQuery = params.Encode()
	raw, err := c.fetch(ctx, u.String(), 2<<20)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid Commons metadata")
	}
	return result, nil
}
func objectValue(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}
func arrayValue(m map[string]any, key string) []any { v, _ := m[key].([]any); return v }
func (c *CommonsClient) imageTitle(ctx context.Context, qid string) (string, error) {
	if !regexp.MustCompile(`^Q[0-9]+$`).MatchString(qid) {
		return "", nil
	}
	data, err := c.api(ctx, url.Values{"action": {"wbgetclaims"}, "entity": {qid}, "property": {"P18"}})
	if err != nil {
		return "", err
	}
	for _, claim := range arrayValue(objectValue(data, "claims"), "P18") {
		obj, _ := claim.(map[string]any)
		value := stringValue(objectValue(objectValue(obj, "mainsnak"), "datavalue"), "value")
		if value != "" && text("File:"+value, 512, false) {
			return "File:" + value, nil
		}
	}
	return "", nil
}

var metadataHTML = regexp.MustCompile(`<[^>]+>`)

func commonsMeta(info map[string]any, key string) string {
	value := stringValue(objectValue(objectValue(info, "extmetadata"), key), "value")
	value = strings.TrimSpace(metadataHTML.ReplaceAllString(value, ""))
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
}
func FreeCommonsLicense(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if strings.Contains(lower, "-nc") || strings.Contains(lower, "-nd") || strings.Contains(lower, "noncommercial") || strings.Contains(lower, "no derivatives") {
		return false
	}
	return lower == "pd" || lower == "public domain" || strings.HasPrefix(lower, "cc0") || strings.HasPrefix(lower, "cc by ") || strings.HasPrefix(lower, "cc by-sa ") || strings.HasPrefix(lower, "cc-by-") || strings.HasPrefix(lower, "cc-by-sa-") || strings.HasPrefix(lower, "gfdl") || lower == "free art license"
}

type LicensedImage struct {
	Bytes                               []byte
	Attribution, License, SourcePageURL string
}

func (c *CommonsClient) Image(ctx context.Context, title string) (*LicensedImage, error) {
	if !strings.HasPrefix(title, "File:") || !text(title, 512, false) {
		return nil, errors.New("invalid Commons file")
	}
	data, err := c.api(ctx, url.Values{"action": {"query"}, "titles": {title}, "prop": {"imageinfo"}, "iiprop": {"url|mime|size|extmetadata"}, "iiurlwidth": {"800"}})
	if err != nil {
		return nil, err
	}
	pages := objectValue(objectValue(data, "query"), "pages")
	keys := []string{}
	for key := range pages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var info map[string]any
	for _, key := range keys {
		page, _ := pages[key].(map[string]any)
		if images := arrayValue(page, "imageinfo"); len(images) > 0 {
			info, _ = images[0].(map[string]any)
			break
		}
	}
	if info == nil {
		return nil, nil
	}
	imageURL := stringValue(info, "thumburl")
	if imageURL == "" {
		imageURL = stringValue(info, "url")
	}
	mime := stringValue(info, "thumbmime")
	if mime == "" {
		mime = stringValue(info, "mime")
	}
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
		return nil, nil
	}
	imageURI, parseErr := url.Parse(imageURL)
	source := stringValue(info, "descriptionurl")
	sourceURI, sourceErr := url.Parse(source)
	if parseErr != nil || imageURI.Scheme != "https" || imageURI.User != nil || imageURI.Hostname() != "upload.wikimedia.org" || sourceErr != nil || sourceURI.Scheme != "https" || sourceURI.User != nil || sourceURI.Hostname() != "commons.wikimedia.org" || len(source) > 500 {
		return nil, nil
	}
	license := clampText(commonsMeta(info, "LicenseShortName"), 120)
	if !FreeCommonsLicense(license) {
		return nil, nil
	}
	artist := commonsMeta(info, "Artist")
	pieces := []string{}
	if artist != "" {
		pieces = append(pieces, artist)
	}
	pieces = append(pieces, license, "via Wikimedia Commons")
	raw, err := c.fetch(ctx, imageURL, 8<<20)
	if err != nil {
		return nil, err
	}
	return &LicensedImage{Bytes: raw, Attribution: clampText(strings.Join(pieces, ", "), 255), License: license, SourcePageURL: source}, nil
}

type CoverResolveOptions struct {
	City            string
	Limit           int
	DryRun, Recheck bool
}

func (r *Runner) ResolveCovers(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
	return r.ResolveCoversWithOptions(ctx, CoverResolveOptions{City: r.Config.RoeduCity, Limit: 100})
}
func (r *Runner) ResolveCoversWithOptions(ctx context.Context, options CoverResolveOptions) (any, error) {
	if options.Limit < 0 || options.Limit > 10000 {
		return nil, platform.ErrInvalid
	}
	if options.Limit == 0 {
		options.Limit = 200
	}
	client := r.Config.Commons
	if client == nil {
		client = &CommonsClient{}
	}
	rows, err := r.DB.Query(ctx, `SELECT p.id,p.name,p.raw_tags FROM places_place p WHERE `+catalog.PolicyFromContext(ctx).PlaceSQL()+` AND ($1::text='' OR lower(p.address_city)=lower($1)) AND NOT EXISTS(SELECT 1 FROM places_placecover c WHERE c.place_id=p.id AND c.storage_key<>'') ORDER BY p.id`, options.City)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id   int64
		name string
		tags map[string]any
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		var raw []byte
		if err = rows.Scan(&item.id, &item.name, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if json.Unmarshal(raw, &item.tags) != nil {
			continue
		}
		candidates = append(candidates, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	resolved, failed, seen, skippedChecked, noCandidate := 0, 0, 0, 0, 0
	for _, item := range candidates {
		if seen >= options.Limit {
			break
		}
		if value := item.tags["cover_checked"]; value != nil && !options.Recheck {
			checked := false
			switch v := value.(type) {
			case bool:
				checked = v
			case string:
				checked = v != ""
			case float64:
				checked = v != 0
			}
			if checked {
				skippedChecked++
				continue
			}
		}
		title := CommonsFileTitle(item.tags)
		_, hasWikidata := item.tags["wikidata"].(string)
		if title == "" && !hasWikidata {
			noCandidate++
			continue
		}
		seen++
		if options.DryRun {
			continue
		}
		if title == "" {
			title, err = client.imageTitle(ctx, stringValue(item.tags, "wikidata"))
		}
		var image *LicensedImage
		if err == nil && title != "" {
			image, err = client.Image(ctx, title)
		}
		if err == nil && image != nil {
			if r.Config.ImportCover == nil || client.TempDir == "" {
				return nil, errors.New("licensed cover processor unavailable")
			}
			if err = os.MkdirAll(client.TempDir, 0700); err != nil {
				return nil, errors.New("licensed cover scratch unavailable")
			}
			file, createErr := os.CreateTemp(client.TempDir, "commons-cover-*")
			if createErr != nil {
				return nil, errors.New("licensed cover scratch unavailable")
			}
			path := file.Name()
			_, writeErr := file.Write(image.Bytes)
			closeErr := file.Close()
			image.Bytes = nil
			if writeErr != nil || closeErr != nil {
				err = errors.New("licensed cover scratch write failed")
			} else {
				_, err = r.Config.ImportCover(ctx, item.id, path, image.Attribution, image.License, image.SourcePageURL, clampText(item.name, 140))
				if err == nil {
					resolved++
				}
			}
			_ = os.Remove(path)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			failed++
			err = nil
		}
		if _, err = r.DB.Exec(ctx, `UPDATE places_place SET raw_tags=raw_tags||'{"cover_checked":true}'::jsonb WHERE id=$1`, item.id); err != nil {
			return nil, err
		}
	}
	return map[string]int{"seen": seen, "resolved": resolved, "failed": failed, "skipped_checked": skippedChecked, "no_candidate": noCandidate}, nil
}
