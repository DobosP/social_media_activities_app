package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var digestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Blocklist must be immutable after construction. An empty list is ineffective
// and fails closed. A dHash is only an additional heuristic, not a CSAM service.
type Blocklist struct {
	hashes     map[string]struct{}
	perceptual []string
	distance   int
}

func NewBlocklist(hashes, perceptual []string, distance int) (*Blocklist, error) {
	if len(hashes)+len(perceptual) == 0 || distance < 0 || distance > 8 {
		return nil, ErrScanner
	}
	b := &Blocklist{hashes: make(map[string]struct{}), distance: distance}
	for _, h := range hashes {
		if !digestRE.MatchString(h) {
			return nil, ErrScanner
		}
		b.hashes[h] = struct{}{}
	}
	for _, h := range perceptual {
		if _, e := Hamming(h, h); e != nil {
			return nil, ErrScanner
		}
		b.perceptual = append(b.perceptual, h)
	}
	return b, nil
}
func (b *Blocklist) Scan(ctx context.Context, in ScanInput) (Verdict, error) {
	if err := ctx.Err(); err != nil {
		return Verdict{}, err
	}
	if b == nil || len(b.hashes)+len(b.perceptual) == 0 || !digestRE.MatchString(in.SHA256) {
		return Verdict{}, ErrScanner
	}
	if _, bad := b.hashes[in.SHA256]; bad {
		return Verdict{}, nil
	}
	if in.PerceptualHash != "" {
		for _, h := range b.perceptual {
			distance, e := Hamming(in.PerceptualHash, h)
			if e != nil {
				return Verdict{}, ErrScanner
			}
			if distance <= b.distance {
				return Verdict{}, nil
			}
		}
	}
	return Verdict{Clean: true}, nil
}

type ManagedScanner struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func (s ManagedScanner) Scan(ctx context.Context, in ScanInput) (Verdict, error) {
	u, e := url.Parse(s.Endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !digestRE.MatchString(in.SHA256) {
		return Verdict{}, ErrScanner
	}
	payload, _ := json.Marshal(struct {
		SHA string `json:"sha256"`
	}{in.SHA256})
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if e != nil {
		return Verdict{}, ErrScanner
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if s.Token != "" {
		request.Header.Set("Authorization", "Bearer "+s.Token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if s.Client != nil {
		*client = *s.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout == 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	resp, e := client.Do(request)
	if e != nil {
		return Verdict{}, ErrScanner
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Verdict{}, ErrScanner
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if e != nil || len(b) > 4096 {
		return Verdict{}, ErrScanner
	}
	return parseManagedVerdict(b)
}
func parseManagedVerdict(b []byte) (Verdict, error) {
	// Exact, duplicate-free provider schema: at least one explicit boolean match
	// or flagged field. Missing/null/string/number/empty objects cannot mean clean.
	d := json.NewDecoder(bytes.NewReader(b))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return Verdict{}, ErrScanner
	}
	seen := map[string]bool{}
	matched := false
	found := false
	for d.More() {
		t, e = d.Token()
		if e != nil {
			return Verdict{}, ErrScanner
		}
		key, ok := t.(string)
		if !ok || seen[key] || (key != "match" && key != "flagged") {
			return Verdict{}, ErrScanner
		}
		seen[key] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return Verdict{}, ErrScanner
		}
		v := string(bytes.TrimSpace(raw))
		if v != "true" && v != "false" {
			return Verdict{}, ErrScanner
		}
		matched = matched || v == "true"
		found = true
	}
	if _, e = d.Token(); e != nil || !found {
		return Verdict{}, ErrScanner
	}
	if d.Decode(new(any)) != io.EOF {
		return Verdict{}, ErrScanner
	}
	return Verdict{Clean: !matched}, nil
}

// Clamd streams bounded PDF bytes through INSTREAM; only the exact protocol
// success is accepted. No connection/protocol error can authorize a document.
type Clamd struct {
	Address string
	Timeout time.Duration
}

func (c Clamd) ScanDocument(ctx context.Context, path string, size int64) (Verdict, error) {
	if size <= 0 || size > 7<<20 || c.Address == "" {
		return Verdict{}, ErrScanner
	}
	timeout := c.Timeout
	if timeout <= 0 || timeout > 20*time.Second {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", c.Address)
	if e != nil {
		return Verdict{}, ErrScanner
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	// Cancellation actively closes a connected socket, including blocked writes.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	f, e := os.Open(path)
	if e != nil {
		return Verdict{}, ErrScanner
	}
	defer f.Close()
	if _, e = conn.Write([]byte("zINSTREAM\x00")); e != nil {
		return Verdict{}, ErrScanner
	}
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		n, readErr := f.Read(buffer)
		if n > 0 {
			total += int64(n)
			if total > size {
				return Verdict{}, ErrScanner
			}
			var head [4]byte
			binary.BigEndian.PutUint32(head[:], uint32(n))
			if _, e = io.Copy(conn, bytes.NewReader(head[:])); e != nil {
				return Verdict{}, ErrScanner
			}
			if _, e = io.Copy(conn, bytes.NewReader(buffer[:n])); e != nil {
				return Verdict{}, ErrScanner
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Verdict{}, ErrScanner
		}
	}
	if total != size {
		return Verdict{}, ErrScanner
	}
	if _, e = conn.Write([]byte{0, 0, 0, 0}); e != nil {
		return Verdict{}, ErrScanner
	}
	reply, e := bufio.NewReader(io.LimitReader(conn, 4097)).ReadString(0)
	if e != nil || len(reply) > 4096 {
		return Verdict{}, ErrScanner
	}
	if strings.TrimSpace(strings.TrimSuffix(reply, "\x00")) == "stream: OK" {
		return Verdict{Clean: true}, nil
	}
	return Verdict{Clean: false}, nil
}
