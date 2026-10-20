package jobs

// Local proof-of-concept acceptance (ADR-0057, cross-repo contract v1). A POC
// page carries items whose REAL publish_source decision is a deny plus a closed
// `local_poc_admission` recording which of that deny's reasons the owner waived
// for a loopback-only development build. The admission is never a legal basis:
// ValidatePackItem remains strict and still refuses every such item, and every
// path here requires RoeduClient.LocalPOCActive (set only by the development,
// loopback, ROEDU_LOCAL_POC=1 configuration gate) plus the page's own
// `local_poc: true`.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// LocalPOCMinConfidence is the scheduled POC sync's import floor: Events V3 rows
// are 1.0 (structured) or 0.9 (template); production keeps 1.
const LocalPOCMinConfidence = 0.9

// ErrLocalPOCRefused is returned when a POC pack reaches a consumer whose mode
// gate is off. It never degrades to a production read.
var ErrLocalPOCRefused = errors.New("RO-EDU local POC pack refused outside development")

// localPOCWaivable is contract §2's closed set, identical in every repo.
var localPOCWaivable = map[string]bool{
	"capability_denied":          true,
	"confidentiality_not_public": true,
	"copyright_unknown":          true,
	"missing_legal_basis":        true,
	"personal_data_not_cleared":  true,
	"privacy_personal_data":      true,
	"privacy_revision_missing":   true,
	"privacy_unknown":            true,
}

const localPOCAdmissionKind = "owner_local_development_override"
const pageEnvelopeFields = "pack_id app layer schema_version snapshot_id release_id snapshot_generated_at snapshot_mode snapshot_complete items pagination withheld errors"

var privacyClassPattern = regexp.MustCompile(`^[a-z][a-z_]{0,63}$`)

// LocalPOCActive is the client half of the gate: the configuration gate set
// LocalPOC, and the producer endpoint is loopback (re-checked here so a client
// built elsewhere can never accept a POC page from a remote host).
func (c *RoeduClient) LocalPOCActive() bool {
	if c == nil || !c.LocalPOC {
		return false
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return LoopbackHost(parsed.Hostname())
}

// LoopbackHost accepts localhost and loopback IP literals only.
func LoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
}

func (c *RoeduClient) envelopeFields(page map[string]any) string {
	if _, present := page["local_poc"]; present && c.LocalPOCActive() {
		return pageEnvelopeFields + " local_poc"
	}
	return pageEnvelopeFields
}

func validatePageItem(item map[string]any, localPOC bool) bool {
	if localPOC {
		return ValidateLocalPOCPackItem(item)
	}
	return ValidatePackItem(item)
}

// ValidateLocalPOCPackItem accepts a clear-allow item exactly as production does,
// or an item carrying a verified admission over its real deny. For the latter
// the admission replaces only the four clearance checks (gdpr_relevant=false,
// privacy_classification=no_personal_data, effect=allow, reasons=
// [explicitly_cleared]); the unchanged production validator then checks every
// other shape, integrity and SHA field on a copy with those four set to the
// production constants. A private access context additionally permits the
// literal tdm_exception label only after this admission verifies. Neither the
// item nor its access label is rewritten, including on the validation copy.
func ValidateLocalPOCPackItem(item map[string]any) bool {
	raw, present := item["local_poc_admission"]
	if !present {
		return ValidatePackItem(item)
	}
	admission, ok := raw.(map[string]any)
	attestation, attOK := item["policy_attestation"].(map[string]any)
	if !ok || !attOK || !VerifyLocalPOCAdmission(admission, attestation) {
		return false
	}
	if _, ok := item["gdpr_relevant"].(bool); !ok {
		return false
	}
	privacy, ok := item["privacy_classification"].(string)
	if !ok || !privacyClassPattern.MatchString(privacy) {
		return false
	}
	if attestation["decision_id"] != item["policy_decision_id"] || admission["underlying_decision_id"] != item["policy_decision_id"] {
		return false
	}
	shape := make(map[string]any, len(item))
	for key, value := range item {
		shape[key] = value
	}
	delete(shape, "local_poc_admission")
	shape["gdpr_relevant"] = false
	shape["privacy_classification"] = "no_personal_data"
	cleared := make(map[string]any, len(attestation))
	for key, value := range attestation {
		cleared[key] = value
	}
	cleared["effect"] = "allow"
	cleared["reasons"] = []any{"explicitly_cleared"}
	shape["policy_attestation"] = cleared
	return validatePackItem(shape, verifiedLocalPOCPackAccess)
}

// VerifyLocalPOCAdmission applies contract §2's verifier rule to one item's
// publish_source attestation and admission object.
func VerifyLocalPOCAdmission(admission, attestation map[string]any) bool {
	if !exactKeys(admission, "schema_version kind not_a_legal_basis underlying_decision_id waived_reasons poc_root admission_digest") {
		return false
	}
	if attestation["action"] != "publish_source" || attestation["effect"] != "deny" {
		return false
	}
	waived, ok := sortedWaivable(admission["waived_reasons"])
	if !ok {
		return false
	}
	reasons, ok := attestation["reasons"].([]any)
	if !ok || len(reasons) == 0 {
		return false
	}
	denied := map[string]bool{}
	for _, reason := range reasons {
		value, ok := reason.(string)
		if !ok {
			return false
		}
		denied[value] = true
	}
	if len(denied) != len(waived) {
		return false
	}
	for _, reason := range waived {
		if !denied[reason] {
			return false
		}
	}
	decision, ok := admission["underlying_decision_id"].(string)
	if !ok || decision == "" || attestation["decision_id"] != decision {
		return false
	}
	digest, ok := admission["admission_digest"].(string)
	if !ok || !hashPattern.MatchString(digest) {
		return false
	}
	computed, ok := LocalPOCAdmissionDigest(admission)
	return ok && computed == digest
}

func sortedWaivable(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		reason, ok := item.(string)
		if !ok || !localPOCWaivable[reason] || i > 0 && reason <= out[i-1] {
			return nil, false
		}
		out = append(out, reason)
	}
	return out, true
}

// LocalPOCAdmissionDigest returns sha256 over the contract canonical JSON of the
// admission without `admission_digest`, after checking every fixed value. The
// canonical bytes equal Python json.dumps(sort_keys=True, separators=(",",
// ":"), ensure_ascii=False): UTF-8, no HTML escaping, U+2028/2029 literal.
func LocalPOCAdmissionDigest(admission map[string]any) (string, bool) {
	if !integerOne(admission["schema_version"]) || admission["kind"] != localPOCAdmissionKind || admission["not_a_legal_basis"] != true {
		return "", false
	}
	decision, ok := admission["underlying_decision_id"].(string)
	if !ok || decision == "" || !utf8.ValidString(decision) {
		return "", false
	}
	root, ok := admission["poc_root"].(string)
	if !ok || !strings.HasPrefix(root, "/") || len(root) > 4096 || !utf8.ValidString(root) {
		return "", false
	}
	waived, ok := sortedWaivable(admission["waived_reasons"])
	if !ok {
		return "", false
	}
	fields := map[string]string{
		"kind":                   canonicalString(localPOCAdmissionKind),
		"not_a_legal_basis":      "true",
		"poc_root":               canonicalString(root),
		"schema_version":         "1",
		"underlying_decision_id": canonicalString(decision),
	}
	reasons := make([]string, len(waived))
	for i, reason := range waived {
		reasons[i] = canonicalString(reason)
	}
	fields["waived_reasons"] = "[" + strings.Join(reasons, ",") + "]"
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(canonicalString(key))
		b.WriteByte(':')
		b.WriteString(fields[key])
	}
	b.WriteByte('}')
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), true
}

func integerOne(value any) bool {
	switch v := value.(type) {
	case json.Number:
		return v.String() == "1"
	case float64:
		return v == 1
	case int:
		return v == 1
	}
	return false
}

// canonicalString escapes exactly as Python's json encoder with ensure_ascii=False.
func canonicalString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteString(strconv.FormatInt(int64(r)>>4, 16))
				b.WriteString(strconv.FormatInt(int64(r)&0xf, 16))
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// LocalPOCTags records, on a POC venue's raw tags, the release-level marker and
// the item's honest evidence: its real privacy values and its admission. The
// `roedu` prefix keeps them when a venue merges into an existing place. A
// production pack leaves the tags untouched.
func LocalPOCTags(tags map[string]any, pack PackRead, item map[string]any) {
	if !pack.LocalPOC {
		return
	}
	tags["roedu_local_poc"] = true
	tags["roedu:access_type"] = item["access_type"]
	tags["roedu:gdpr_relevant"] = item["gdpr_relevant"]
	tags["roedu:privacy_classification"] = item["privacy_classification"]
	if admission, ok := item["local_poc_admission"]; ok {
		tags["roedu:local_poc_admission"] = admission
	}
}

func roeduSyncOptions(pack PackRead) RoeduApplyOptions {
	if pack.LocalPOC {
		return RoeduApplyOptions{MinConfidence: LocalPOCMinConfidence}
	}
	return RoeduApplyOptions{MinConfidence: 1}
}

// ensureLocalPOCColumn adds the event flag and evidence columns a POC sync
// writes. They exist only in a database a POC sync has written to (the
// production schema is unchanged); readers use catalog.EventLocalPOCSQL, which
// tolerates their absence. It must run before the transaction's first
// events_event write: the baseline's DEFERRABLE INITIALLY DEFERRED foreign keys
// leave pending trigger events that make a later ALTER TABLE fail (SQLSTATE 55006).
func ensureLocalPOCColumn(ctx context.Context, tx pgx.Tx) error {
	var present int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_attribute WHERE attrelid='events_event'::regclass AND attname IN('source_local_poc','source_local_poc_evidence') AND NOT attisdropped`).Scan(&present); err != nil || present == 2 {
		return err
	}
	_, err := tx.Exec(ctx, `ALTER TABLE events_event ADD COLUMN IF NOT EXISTS source_local_poc boolean NOT NULL DEFAULT false, ADD COLUMN IF NOT EXISTS source_local_poc_evidence jsonb`)
	return err
}

// LocalPOCEventEvidence is the honest evidence a POC event row keeps (CONTRACT
// §2: the admission at every layer): the item's REAL access and privacy values,
// its real publish_source attestation and decision id, and its admission (absent
// for a clear-allow item of a POC release).
func LocalPOCEventEvidence(item map[string]any) map[string]any {
	evidence := map[string]any{"access_type": item["access_type"], "gdpr_relevant": item["gdpr_relevant"], "privacy_classification": item["privacy_classification"], "policy_decision_id": item["policy_decision_id"], "policy_attestation": item["policy_attestation"]}
	if admission, ok := item["local_poc_admission"]; ok {
		evidence["local_poc_admission"] = admission
	}
	return evidence
}

// markLocalPOCEvent flags one event row this POC sync wrote and stores its
// evidence.
func markLocalPOCEvent(ctx context.Context, tx pgx.Tx, external string, item map[string]any) error {
	evidence, err := json.Marshal(LocalPOCEventEvidence(item))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE events_event SET source_local_poc=true,source_local_poc_evidence=$2::jsonb WHERE source='roedu' AND external_id=$1`, external, string(evidence))
	return err
}
