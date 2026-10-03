package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const snapshotSchema = 2

var snapshotCaps = map[string]int{"events": 10000, "places": 50000, "activities": 2000, "taxonomy": 50000}
var snapshotByteCaps = map[string]int64{"events": 32 << 20, "places": 64 << 20, "activities": 8 << 20, "taxonomy": 8 << 20}

func readSnapshotFile(root *os.Root, name string, limit int64) ([]byte, error) {
	// Fixed flat filenames plus OpenRoot prevent path traversal, including a
	// directory or symlink replacement during a reload.
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("snapshot file unavailable or exceeds budget")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("snapshot file unavailable")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("snapshot file is not regular")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, fmt.Errorf("snapshot file exceeds budget")
	}
	return raw, nil
}

func strictJSON(raw []byte, target any) error {
	// Raw records are later emitted as JSON. Reject duplicate keys throughout
	// the document so validation and every downstream JSON reader see the same
	// value, including inside approved nested fields.
	check := json.NewDecoder(bytes.NewReader(raw))
	check.UseNumber()
	if err := uniqueJSONValue(check, 0); err != nil {
		return fmt.Errorf("ambiguous snapshot JSON")
	}
	if _, err := check.Token(); err != io.EOF {
		return fmt.Errorf("invalid snapshot JSON envelope")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("invalid snapshot JSON schema")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid snapshot JSON envelope")
	}
	return nil
}

func uniqueJSONValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return fmt.Errorf("snapshot nesting exceeds budget")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate object key")
			}
			// encoding/json accepts case-insensitive struct field names. The
			// producer contract uses lower ASCII keys; enforcing that spelling
			// prevents aliases overwriting a value while unsafe raw bytes survive.
			if key == "" {
				return fmt.Errorf("noncanonical snapshot key")
			}
			for _, ch := range key {
				if ch != '_' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
					return fmt.Errorf("noncanonical snapshot key")
				}
			}
			seen[key] = true
			if err := uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid object")
		}
	case '[':
		for d.More() {
			if err := uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid array")
		}
	default:
		return fmt.Errorf("invalid delimiter")
	}
	return nil
}

func validateManifest(m manifestDoc) error {
	if m.SchemaVersion != snapshotSchema || len(m.Datasets) != len(snapshotCaps) {
		return fmt.Errorf("unsupported snapshot schema")
	}
	generated, err := time.Parse(time.RFC3339Nano, m.GeneratedAt)
	if err != nil || !strings.HasSuffix(m.GeneratedAt, "Z") || generated.After(time.Now().Add(5*time.Minute)) {
		return fmt.Errorf("invalid snapshot generation")
	}
	for name, cap := range snapshotCaps {
		d, ok := m.Datasets[name]
		digest, err := hex.DecodeString(d.SHA256)
		if !ok || d.File != name+".json" || d.Count < 0 || d.Count > cap || err != nil || len(digest) != 32 || d.SHA256 != strings.ToLower(d.SHA256) {
			return fmt.Errorf("invalid snapshot dataset metadata")
		}
	}
	var credits []struct {
		LicenseName string `json:"license_name"`
		Attribution string `json:"attribution"`
	}
	if err := strictJSON(m.Licenses, &credits); err != nil {
		return err
	}
	return nil
}

var recordFields = map[string]string{
	"events":     "id title description starts_at ends_at url source attribution license_name provenance_url attribution_credit activity activity_type source_category lifecycle_status source_confidence source_recurrence source_timezone source_price_min source_price_max source_currency source_is_free source_availability place_id path place_summary",
	"places":     "id name lat lon address city postcode country website phone opening_hours_text activity_types attribution license_name provenance_url path",
	"activities": "id title cohort starts_at status activity_type place_id",
}

var reviewedFieldSets = func() map[string]map[string]bool {
	sets := make(map[string]map[string]bool)
	fields := []string{"id name city lat lon", "attribution license_name provenance_url", "slug name parent", "slug name category parent family_friendly wellness"}
	for _, value := range recordFields {
		fields = append(fields, value)
	}
	for _, value := range fields {
		set := make(map[string]bool)
		for _, key := range strings.Fields(value) {
			set[key] = true
		}
		sets[value] = set
	}
	return sets
}()

func reviewedObject(raw []byte, fields string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("invalid snapshot object")
	}
	allowed := reviewedFieldSets[fields]
	for key := range obj {
		if !allowed[key] {
			return nil, fmt.Errorf("unreviewed snapshot field")
		}
		value := bytes.TrimSpace(obj[key])
		if len(value) == 0 {
			return nil, fmt.Errorf("invalid snapshot field")
		}
		if key == "place_summary" || key == "attribution_credit" {
			continue
		}
		if key == "activity_types" {
			var slugs []string
			if err := json.Unmarshal(value, &slugs); err != nil {
				return nil, fmt.Errorf("invalid activity type list")
			}
		} else if value[0] == '{' || value[0] == '[' {
			return nil, fmt.Errorf("non-scalar snapshot field")
		}
	}
	return obj, nil
}

func validateRecords(name string, records []json.RawMessage) error {
	seen := make(map[int64]bool, len(records))
	for _, raw := range records {
		obj, err := reviewedObject(raw, recordFields[name])
		if err != nil {
			return err
		}
		var id int64
		if json.Unmarshal(obj["id"], &id) != nil || id <= 0 || seen[id] {
			return fmt.Errorf("invalid or duplicate snapshot ID")
		}
		seen[id] = true
		if name == "activities" {
			var cohort string
			if json.Unmarshal(obj["cohort"], &cohort) != nil || cohort != "adult" {
				return fmt.Errorf("non-adult public activity rejected")
			}
		}
		if name == "events" {
			for key, fields := range map[string]string{"place_summary": "id name city lat lon", "attribution_credit": "attribution license_name provenance_url"} {
				if nested, ok := obj[key]; ok && string(nested) != "null" {
					if _, err := reviewedObject(nested, fields); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func validateTaxonomy(raw []byte, generated string, count int) error {
	var tax struct {
		SchemaVersion int               `json:"schema_version"`
		GeneratedAt   string            `json:"generated_at"`
		Categories    []json.RawMessage `json:"categories"`
		ActivityTypes []json.RawMessage `json:"activity_types"`
	}
	if strictJSON(raw, &tax) != nil || tax.SchemaVersion != snapshotSchema || tax.GeneratedAt != generated || len(tax.Categories)+len(tax.ActivityTypes) != count {
		return fmt.Errorf("incoherent snapshot taxonomy")
	}
	for _, group := range []struct {
		records []json.RawMessage
		fields  string
	}{{tax.Categories, "slug name parent"}, {tax.ActivityTypes, "slug name category parent family_friendly wellness"}} {
		seen := make(map[string]bool)
		for _, raw := range group.records {
			obj, err := reviewedObject(raw, group.fields)
			if err != nil {
				return err
			}
			var slug string
			if json.Unmarshal(obj["slug"], &slug) != nil || slug == "" || seen[slug] {
				return fmt.Errorf("invalid taxonomy identity")
			}
			seen[slug] = true
		}
	}
	return nil
}
