package main

import (
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/parquet-go/parquet-go"
)

// Overture is a source-only adapter. Parquet never enters the serving process
// unless an operator explicitly selects ingest_places/source=overture.
const maxOvertureFiles = 1000
const maxOvertureRows = 5000000
const maxParquetSize = int64(2 << 30)
const maxParquetFooter = 8 << 20

type overtureSource struct {
	DefaultPath string
	HTTP        *http.Client
}
type overtureRow struct {
	ID    string `parquet:"id"`
	Names *struct {
		Primary *string `parquet:"primary,optional"`
	} `parquet:"names,optional"`
	Categories *struct {
		Primary   *string  `parquet:"primary,optional"`
		Alternate []string `parquet:"alternate,list,optional"`
	} `parquet:"categories,optional"`
	BBox *struct {
		XMin float64 `parquet:"xmin"`
		YMin float64 `parquet:"ymin"`
		XMax float64 `parquet:"xmax"`
		YMax float64 `parquet:"ymax"`
	} `parquet:"bbox,optional"`
	Addresses []overtureAddress `parquet:"addresses,list,optional"`
	Websites  []string          `parquet:"websites,list,optional"`
}
type overtureAddress struct {
	Street      *string `parquet:"street,optional"`
	Freeform    *string `parquet:"freeform,optional"`
	HouseNumber *string `parquet:"housenumber,optional"`
	Locality    *string `parquet:"locality,optional"`
	Postcode    *string `parquet:"postcode,optional"`
	Country     *string `parquet:"country,optional"`
}

func optionalString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func overtureRaw(row overtureRow) (commands.RawPlace, bool) {
	if row.BBox == nil || row.Names == nil || row.Names.Primary == nil {
		return commands.RawPlace{}, false
	}
	lon := (row.BBox.XMin + row.BBox.XMax) / 2
	lat := (row.BBox.YMin + row.BBox.YMax) / 2
	name := strings.TrimSpace(*row.Names.Primary)
	if name == "" || len([]rune(name)) > 255 || row.ID == "" || len(row.ID) > 200 || math.IsNaN(lon) || math.IsNaN(lat) || math.IsInf(lon, 0) || math.IsInf(lat, 0) || math.Abs(lon) > 180 || math.Abs(lat) > 90 {
		return commands.RawPlace{}, false
	}
	tags := map[string]any{"overture:category": nil, "overture:alternate": []string{}}
	if row.Categories != nil {
		tags["overture:category"] = row.Categories.Primary
		if row.Categories.Primary != nil {
			tags["overture:category"] = *row.Categories.Primary
		}
		tags["overture:alternate"] = row.Categories.Alternate
	}
	website := ""
	if len(row.Websites) > 0 {
		website = row.Websites[0]
		if !safePublicContactURL(website) || len(website) > 500 {
			website = ""
		} else {
			tags["overture:website"] = website
		}
	}
	address := map[string]string{}
	if len(row.Addresses) > 0 {
		a := row.Addresses[0]
		street := optionalString(a.Street)
		if street == "" {
			street = optionalString(a.Freeform)
		}
		address = map[string]string{"street": street, "housenumber": optionalString(a.HouseNumber), "city": optionalString(a.Locality), "postcode": optionalString(a.Postcode), "country": optionalString(a.Country)}
	}
	return commands.RawPlace{Source: "overture", ExternalID: row.ID, Name: name, Lon: lon, Lat: lat, Tags: tags, Website: website, Address: address}, true
}
func parseBoundingBox(raw string) ([4]float64, error) {
	var bounds [4]float64
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return bounds, errors.New("Overture requires an explicit bounding box")
	}
	for i, value := range parts {
		n, e := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return bounds, errors.New("Overture bounding box is invalid")
		}
		bounds[i] = n
	}
	if math.Abs(bounds[0]) > 180 || math.Abs(bounds[2]) > 180 || math.Abs(bounds[1]) > 90 || math.Abs(bounds[3]) > 90 || bounds[0] > bounds[2] || bounds[1] > bounds[3] {
		return bounds, errors.New("Overture bounding box is invalid")
	}
	return bounds, nil
}
func (s overtureSource) Fetch(ctx context.Context, o commands.PlaceOptions, emit func(commands.RawPlace) error) (err error) {
	bounds, err := parseBoundingBox(o.BBox)
	if err != nil {
		return err
	}
	path := o.OverturePath
	if path == "" {
		path = s.DefaultPath
	}
	if path == "" {
		return errors.New("OVERTURE_DATA_PATH is required")
	}
	paths, err := s.paths(ctx, path)
	if err != nil {
		return err
	}
	scanned, emitted := int64(0), 0
	for _, path := range paths {
		if err = ctx.Err(); err != nil {
			return err
		}
		reader, size, closeFile, e := s.open(ctx, path)
		if e != nil {
			return e
		}
		reached, e := readOverture(ctx, reader, size, bounds, &scanned, &emitted, o.Limit, emit)
		closeFile()
		if e != nil {
			return e
		}
		if reached {
			return nil
		}
	}
	return nil
}
func readOverture(ctx context.Context, at io.ReaderAt, size int64, bounds [4]float64, scanned *int64, emitted *int, limit int, emit func(commands.RawPlace) error) (reached bool, err error) {
	// Bad external Parquet metadata must produce a private, bounded diagnostic.
	defer func() {
		if recover() != nil {
			reached = false
			err = errors.New("Overture Parquet schema is invalid")
		}
	}()
	var magic [4]byte
	var footer [8]byte
	if size < 12 || size > maxParquetSize {
		return false, errors.New("Overture Parquet size exceeds bound")
	}
	if _, err = at.ReadAt(magic[:], 0); err != nil || string(magic[:]) != "PAR1" {
		return false, errors.New("Overture Parquet header is invalid")
	}
	if _, err = at.ReadAt(footer[:], size-8); err != nil || string(footer[4:]) != "PAR1" {
		return false, errors.New("Overture Parquet footer is invalid")
	}
	footerSize := int64(binary.LittleEndian.Uint32(footer[:4]))
	if footerSize < 1 || footerSize > maxParquetFooter || footerSize > size-12 {
		return false, errors.New("Overture Parquet footer exceeds bound")
	}
	file, err := parquet.OpenFile(at, size, parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true), parquet.ReadBufferSize(64<<10))
	if err != nil {
		return false, errors.New("Overture Parquet is invalid")
	}
	if file.NumRows() < 0 || file.NumRows() > maxOvertureRows || len(file.RowGroups()) > 10000 {
		return false, errors.New("Overture Parquet rows exceed bound")
	}
	for _, path := range [][]string{{"id"}, {"names", "primary"}, {"bbox", "xmin"}, {"bbox", "xmax"}, {"bbox", "ymin"}, {"bbox", "ymax"}} {
		if _, ok := file.Schema().Lookup(path...); !ok {
			return false, errors.New("Overture Parquet required column is missing")
		}
	}
	batch := make([]overtureRow, 256)
	for _, group := range file.RowGroups() {
		if !overtureGroupOverlaps(group, bounds) {
			continue
		}
		reader := parquet.NewGenericRowGroupReader[overtureRow](group)
		reached, readErr := readOvertureGroup(ctx, reader, batch, bounds, scanned, emitted, limit, emit)
		_ = reader.Close()
		if reached || readErr != nil {
			return reached, readErr
		}
	}
	return false, nil
}

func readOvertureGroup(ctx context.Context, reader *parquet.GenericReader[overtureRow], batch []overtureRow, bounds [4]float64, scanned *int64, emitted *int, limit int, emit func(commands.RawPlace) error) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, e := reader.Read(batch)
		for _, row := range batch[:n] {
			*scanned++
			if *scanned > maxOvertureRows {
				return false, errors.New("Overture scan exceeds row bound")
			}
			raw, ok := overtureRaw(row)
			if !ok || raw.Lon < bounds[0] || raw.Lon > bounds[2] || raw.Lat < bounds[1] || raw.Lat > bounds[3] {
				continue
			}
			if err := emit(raw); err != nil {
				return false, err
			}
			*emitted++
			if limit > 0 && *emitted >= limit {
				return true, nil
			}
		}
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return false, errors.New("Overture Parquet rows are invalid")
		}
		if n == 0 {
			return false, errors.New("Overture Parquet reader stalled")
		}
	}
	return false, nil
}

// Bounding-box column indexes let a global extract skip unrelated row groups;
// missing indexes fall back to exact row filtering without excluding records.
func overtureGroupOverlaps(group parquet.RowGroup, bounds [4]float64) bool {
	values := [4][2]float64{}
	for i, name := range []string{"xmin", "ymin", "xmax", "ymax"} {
		column, ok := group.Schema().Lookup("bbox", name)
		if !ok {
			return true
		}
		chunks := group.ColumnChunks()
		if column.ColumnIndex < 0 || column.ColumnIndex >= len(chunks) {
			return true
		}
		index, err := chunks[column.ColumnIndex].ColumnIndex()
		if err != nil || index.NumPages() < 1 || index.NumPages() > 100000 {
			return true
		}
		low, high := math.Inf(1), math.Inf(-1)
		for page := 0; page < index.NumPages(); page++ {
			if index.NullPage(page) {
				continue
			}
			minimum, maximum := index.MinValue(page), index.MaxValue(page)
			if minimum.IsNull() || maximum.IsNull() || minimum.Kind() != maximum.Kind() {
				return true
			}
			var a, b float64
			switch minimum.Kind() {
			case parquet.Double:
				a, b = minimum.Double(), maximum.Double()
			case parquet.Float:
				a, b = float64(minimum.Float()), float64(maximum.Float())
			default:
				return true
			}
			if math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) || a > b {
				return true
			}
			low, high = min(low, a), max(high, b)
		}
		if math.IsInf(low, 0) || math.IsInf(high, 0) {
			return true
		}
		values[i] = [2]float64{low, high}
	}
	minLon, maxLon := (values[0][0]+values[2][0])/2, (values[0][1]+values[2][1])/2
	minLat, maxLat := (values[1][0]+values[3][0])/2, (values[1][1]+values[3][1])/2
	return maxLon >= bounds[0] && minLon <= bounds[2] && maxLat >= bounds[1] && minLat <= bounds[3]
}
func (s overtureSource) paths(ctx context.Context, path string) ([]string, error) {
	if strings.HasPrefix(path, "s3://") {
		return s.s3Paths(ctx, path)
	}
	if strings.HasPrefix(path, "https://") {
		u, err := url.Parse(path)
		if err != nil || strings.ContainsAny(u.Path, "*[") {
			return nil, errors.New("remote Overture glob must use an anonymous S3 prefix")
		}
		return []string{path}, nil
	}
	if strings.Contains(path, "://") {
		return nil, errors.New("OVERTURE_DATA_PATH is invalid")
	}
	paths, err := filepath.Glob(path)
	if err != nil || len(paths) == 0 || len(paths) > maxOvertureFiles {
		return nil, errors.New("OVERTURE_DATA_PATH glob is invalid or exceeds bound")
	}
	return paths, nil
}
func (s overtureSource) open(ctx context.Context, path string) (io.ReaderAt, int64, func(), error) {
	if strings.HasPrefix(path, "https://") {
		reader, err := newParquetRemote(ctx, path, s.HTTP)
		if err != nil {
			return nil, 0, nil, err
		}
		return reader, reader.size, reader.close, nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxParquetSize {
		return nil, 0, nil, errors.New("OVERTURE_DATA_PATH file is invalid")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, errors.New("Overture file unavailable")
	}
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		f.Close()
		return nil, 0, nil, errors.New("Overture file changed")
	}
	return contextReaderAt{ctx: ctx, at: f}, actual.Size(), func() { _ = f.Close() }, nil
}

type contextReaderAt struct {
	ctx context.Context
	at  io.ReaderAt
}

func (c contextReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.at.ReadAt(p, offset)
}

type parquetRemote struct {
	mu                 sync.Mutex
	ctx                context.Context
	url                string
	size               int64
	etag, lastModified string
	client             *http.Client
	close              func()
	transferred        int64
}

func pinnedSourceClient(ctx context.Context, raw string, injected *http.Client) (*http.Client, func(), error) {
	u, err := url.Parse(raw)
	if err != nil || !safeHTTPS(raw, false) {
		return nil, nil, errors.New("Overture source URL is invalid")
	}
	var client http.Client
	closeClient := func() {}
	if injected != nil {
		client = *injected
	} else {
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
		if err != nil || len(addresses) == 0 {
			return nil, nil, errors.New("Overture source unavailable")
		}
		for _, address := range addresses {
			if !publicIngestionIP(address.IP) {
				return nil, nil, errors.New("Overture source address is invalid")
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
		client.Transport = transport
		closeClient = transport.CloseIdleConnections
	}
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client, closeClient, nil
}
func newParquetRemote(ctx context.Context, raw string, injected *http.Client) (*parquetRemote, error) {
	client, closeClient, err := pinnedSourceClient(ctx, raw, injected)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		closeClient()
		return nil, errors.New("Overture source URL is invalid")
	}
	response, err := client.Do(request)
	if err != nil {
		closeClient()
		return nil, errors.New("Overture source unavailable")
	}
	defer response.Body.Close()
	size := response.ContentLength
	if response.StatusCode != 200 || size < 12 || size > maxParquetSize {
		closeClient()
		return nil, errors.New("Overture source size is invalid")
	}
	etag, lastModified := response.Header.Get("ETag"), response.Header.Get("Last-Modified")
	if strings.HasPrefix(etag, "W/") {
		etag = ""
	}
	if etag == "" && lastModified == "" {
		closeClient()
		return nil, errors.New("Overture source must have immutable conditional-read metadata")
	}
	return &parquetRemote{ctx: ctx, url: raw, size: size, etag: etag, lastModified: lastModified, client: client, close: closeClient}, nil
}
func (r *parquetRemote) ReadAt(p []byte, offset int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	exceeds := r.transferred+int64(len(p)) > 256<<20
	if !exceeds {
		r.transferred += int64(len(p))
	}
	r.mu.Unlock()
	if offset < 0 || offset >= r.size || len(p) > 8<<20 || exceeds {
		return 0, errors.New("Overture range read exceeds bound")
	}
	end := min(r.size-1, offset+int64(len(p))-1)
	request, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return 0, errors.New("Overture range request is invalid")
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
	request.Header.Set("Accept-Encoding", "identity")
	if r.etag != "" {
		request.Header.Set("If-Match", r.etag)
	} else {
		request.Header.Set("If-Unmodified-Since", r.lastModified)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return 0, errors.New("Overture range unavailable")
	}
	defer response.Body.Close()
	wanted := fmt.Sprintf("bytes %d-%d/%d", offset, end, r.size)
	if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != wanted || r.etag != "" && response.Header.Get("ETag") != r.etag || r.etag == "" && response.Header.Get("Last-Modified") != r.lastModified {
		return 0, errors.New("Overture source changed or range invalid")
	}
	n, err := io.ReadFull(io.LimitReader(response.Body, end-offset+2), p[:int(end-offset+1)])
	if err != nil {
		return n, errors.New("Overture range body is invalid")
	}
	var extra [1]byte
	if nExtra, _ := response.Body.Read(extra[:]); nExtra != 0 {
		return n, errors.New("Overture range body exceeds bound")
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (s overtureSource) s3Paths(ctx context.Context, raw string) ([]string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Hostname(), ".:") {
		return nil, errors.New("OVERTURE_DATA_PATH S3 prefix is invalid")
	}
	// Overture's source is anonymous, public S3. This never loads AWS credentials.
	host := u.Hostname() + ".s3.us-west-2.amazonaws.com"
	key := strings.TrimPrefix(u.Path, "/")
	if !strings.ContainsAny(key, "*?[") {
		return []string{s3ObjectURL(host, key)}, nil
	}
	if !strings.HasSuffix(key, "*") || strings.Count(key, "*") != 1 || strings.ContainsAny(key, "?[") {
		return nil, errors.New("Overture S3 glob must be one prefix wildcard")
	}
	prefix := strings.TrimSuffix(key, "*")
	endpoint := "https://" + host + "/"
	client, closeClient, err := pinnedSourceClient(ctx, endpoint, s.HTTP)
	if err != nil {
		return nil, err
	}
	defer closeClient()
	cursor := ""
	paths := []string{}
	seen := map[string]bool{}
	for page := 0; page < maxOvertureFiles; page++ {
		query := url.Values{"list-type": {"2"}, "prefix": {prefix}, "max-keys": {"1000"}}
		if cursor != "" {
			query.Set("continuation-token", cursor)
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
		response, err := client.Do(request)
		if err != nil {
			return nil, errors.New("Overture S3 listing unavailable")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		response.Body.Close()
		if readErr != nil || len(data) > 4<<20 || response.StatusCode != 200 {
			return nil, errors.New("Overture S3 listing is invalid")
		}
		var listing struct {
			Contents              []struct{ Key string }
			IsTruncated           bool
			NextContinuationToken string
		}
		if xml.Unmarshal(data, &listing) != nil {
			return nil, errors.New("Overture S3 listing is invalid")
		}
		for _, entry := range listing.Contents {
			if !strings.HasPrefix(entry.Key, prefix) || strings.ContainsAny(entry.Key, "\x00\r\n\\") || strings.Contains(entry.Key, "..") {
				return nil, errors.New("Overture S3 key is invalid")
			}
			if !strings.HasSuffix(entry.Key, ".parquet") {
				continue
			}
			if seen[entry.Key] {
				return nil, errors.New("Overture S3 listing repeats a file")
			}
			seen[entry.Key] = true
			paths = append(paths, s3ObjectURL(host, entry.Key))
			if len(paths) > maxOvertureFiles {
				return nil, errors.New("Overture S3 listing exceeds bound")
			}
		}
		if !listing.IsTruncated {
			if len(paths) == 0 {
				return nil, errors.New("Overture S3 listing contains no Parquet files")
			}
			return paths, nil
		}
		next := listing.NextContinuationToken
		if next == "" || next == cursor || len(next) > 4096 {
			return nil, errors.New("Overture S3 listing cursor is invalid")
		}
		cursor = next
	}
	return nil, errors.New("Overture S3 listing exceeds page bound")
}

func s3ObjectURL(host, key string) string {
	return (&url.URL{Scheme: "https", Host: host, Path: "/" + key}).String()
}
