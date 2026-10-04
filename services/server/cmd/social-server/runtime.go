package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
)

type denyScanner struct{}

func (denyScanner) Effective() bool { return false }

func (denyScanner) Scan(context.Context, media.ScanInput) (media.Verdict, error) {
	return media.Verdict{}, media.ErrScanner
}

type denyDocuments struct{}

func (denyDocuments) ScanDocument(context.Context, string, int64) (media.Verdict, error) {
	return media.Verdict{}, media.ErrScanner
}

func storageFromEnvironment(get environment, directory string, development bool, presence ...environmentPresence) (media.Store, error) {
	d := newDecoder(get, presence...)
	backend := get("MEDIA_STORAGE_BACKEND")
	if d.isSet("MEDIA_STORAGE_BACKEND") && backend == "" {
		return nil, errors.New("MEDIA_STORAGE_BACKEND is invalid")
	}
	switch backend {
	case "", "local", "apps.media.storage.LocalStorageBackend":
		if !development {
			return nil, errors.New("MEDIA_STORAGE_BACKEND local storage requires loopback development mode")
		}
		path, err := filepath.Abs(directory)
		if err != nil || preparePrivateDirectory(path) != nil {
			return nil, errors.New("private media storage unavailable")
		}
		store, err := media.NewLocalStore(path)
		if err != nil {
			return nil, errors.New("private media storage unavailable")
		}
		return store, nil
	case "s3", "apps.media.storage.S3StorageBackend":
		sse, style := get("MEDIA_S3_SSE"), d.value("MEDIA_S3_ADDRESSING_STYLE", "auto")
		switch sse {
		case "", "AES256", "aws:kms", "aws:kms:dsse":
		default:
			d.invalid("MEDIA_S3_SSE")
		}
		switch style {
		case "auto", "path", "virtual":
		default:
			d.invalid("MEDIA_S3_ADDRESSING_STYLE")
		}
		config := media.S3Config{Endpoint: get("MEDIA_S3_ENDPOINT_URL"), Bucket: get("MEDIA_S3_BUCKET"), Region: get("MEDIA_S3_REGION"), AccessKey: get("AWS_ACCESS_KEY_ID"), SecretKey: get("AWS_SECRET_ACCESS_KEY"), SessionToken: get("AWS_SESSION_TOKEN"), SSE: sse, AddressingStyle: style, EUResidencyVerified: d.boolean("MEDIA_EU_RESIDENCY_VERIFIED", false), PrivateBucketVerified: d.boolean("MEDIA_PRIVATE_BUCKET_VERIFIED", false)}
		if d.err != nil {
			return nil, d.err
		}
		if !config.EUResidencyVerified {
			return nil, errors.New("MEDIA_EU_RESIDENCY_VERIFIED is required")
		}
		if !config.PrivateBucketVerified {
			return nil, errors.New("MEDIA_PRIVATE_BUCKET_VERIFIED is required")
		}
		for _, field := range []struct{ name, value string }{{"MEDIA_S3_ENDPOINT_URL", config.Endpoint}, {"MEDIA_S3_BUCKET", config.Bucket}, {"MEDIA_S3_REGION", config.Region}, {"AWS_ACCESS_KEY_ID", config.AccessKey}, {"AWS_SECRET_ACCESS_KEY", config.SecretKey}} {
			if field.value == "" {
				return nil, errors.New(field.name + " is required")
			}
		}
		store, err := media.NewS3Store(config)
		if err != nil {
			return nil, errors.New("MEDIA_STORAGE_BACKEND S3 configuration is invalid")
		}
		return store, nil
	default:
		return nil, errors.New("MEDIA_STORAGE_BACKEND has no native provider")
	}
}

func hashList(inline, file, name string) ([]string, error) {
	list := []string{}
	if inline != "" {
		d := decoder{get: func(string) string { return inline }}
		list = d.list(name, nil)
		if d.err != nil {
			return nil, d.err
		}
		for i, hash := range list {
			list[i] = strings.ToLower(hash)
		}
	}
	if len(list) > 1000000 {
		return nil, errors.New(name + " exceeds bound")
	}
	if file == "" {
		return list, nil
	}
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return nil, errors.New(name + "_FILE is invalid or unavailable")
	}
	handle, err := os.Open(file)
	if err != nil {
		return nil, errors.New(name + "_FILE is unavailable")
	}
	defer handle.Close()
	actual, err := handle.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.New(name + "_FILE is invalid")
	}
	scanner := bufio.NewScanner(io.LimitReader(handle, (64<<20)+1))
	scanner.Buffer(make([]byte, 128), 1024)
	for scanner.Scan() {
		hash := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if hash != "" && !strings.HasPrefix(hash, "#") {
			list = append(list, hash)
		}
		if len(list) > 1000000 {
			return nil, errors.New(name + "_FILE exceeds bound")
		}
	}
	if scanner.Err() != nil {
		return nil, errors.New(name + "_FILE is invalid")
	}
	return list, nil
}
func scannersFromEnvironment(get environment, presence ...environmentPresence) (media.Scanner, media.DocumentScanner, error) {
	d := newDecoder(get, presence...)
	d.fixedBool("MEDIA_REQUIRE_SCANNER", true)
	documentRequired := d.boolean("MEDIA_REQUIRE_DOCUMENT_SCANNER", false)
	var scanner media.Scanner = denyScanner{}
	mode := get("MEDIA_IMAGE_SCANNER")
	if d.isSet("MEDIA_IMAGE_SCANNER") && mode == "" {
		return nil, nil, errors.New("MEDIA_IMAGE_SCANNER is invalid")
	}
	if d.isSet("MEDIA_DOCUMENT_SCANNER") && get("MEDIA_DOCUMENT_SCANNER") == "" {
		return nil, nil, errors.New("MEDIA_DOCUMENT_SCANNER is invalid")
	}
	switch mode {
	case "managed", "apps.media.scanning.ManagedScanner":
		endpoint := get("MEDIA_SCANNER_ENDPOINT")
		u, _ := url.Parse(endpoint)
		if !safeHTTPS(endpoint, false) || u.RawQuery != "" {
			return nil, nil, errors.New("MEDIA_SCANNER_ENDPOINT is invalid")
		}
		scanner = media.ManagedScanner{Endpoint: endpoint, Token: get("MEDIA_SCANNER_API_KEY"), Client: &http.Client{Timeout: time.Duration(d.integer("MEDIA_SCANNER_TIMEOUT", 10, 1, 10)) * time.Second}}
	case "", "blocklist", "apps.media.scanning.HashBlocklistScanner":
		hashes, err := hashList(get("MEDIA_CSAM_HASH_BLOCKLIST"), get("MEDIA_CSAM_HASH_BLOCKLIST_FILE"), "MEDIA_CSAM_HASH_BLOCKLIST")
		if err != nil {
			return nil, nil, err
		}
		perceptual, err := hashList(get("MEDIA_PERCEPTUAL_BLOCKLIST"), get("MEDIA_PERCEPTUAL_BLOCKLIST_FILE"), "MEDIA_PERCEPTUAL_BLOCKLIST")
		if err != nil {
			return nil, nil, err
		}
		distance := d.integer("MEDIA_PERCEPTUAL_MAX_DISTANCE", 8, 0, 8)
		if len(hashes)+len(perceptual) > 0 {
			scanner, err = media.NewBlocklist(hashes, perceptual, distance)
			if err != nil {
				return nil, nil, errors.New("MEDIA_CSAM_HASH_BLOCKLIST or MEDIA_PERCEPTUAL_BLOCKLIST is invalid")
			}
		}
	default:
		return nil, nil, errors.New("MEDIA_IMAGE_SCANNER has no native provider")
	}
	var documents media.DocumentScanner = denyDocuments{}
	switch get("MEDIA_DOCUMENT_SCANNER") {
	case "clamd", "apps.media.docscan.ClamdScanner":
		host := d.value("MEDIA_CLAMD_HOST", "127.0.0.1")
		if !validHost(host) {
			d.invalid("MEDIA_CLAMD_HOST")
		}
		port := d.integer("MEDIA_CLAMD_PORT", 3310, 1, 65535)
		documents = media.Clamd{Address: net.JoinHostPort(host, strconv.Itoa(port)), Timeout: time.Duration(d.integer("MEDIA_CLAMD_TIMEOUT", 20, 1, 20)) * time.Second}
	case "", "none", "apps.media.docscan.NoopDocumentScanner":
		if documentRequired {
			d.invalid("MEDIA_DOCUMENT_SCANNER")
		}
	default:
		return nil, nil, errors.New("MEDIA_DOCUMENT_SCANNER has no native provider")
	}
	if d.err != nil {
		return nil, nil, d.err
	}
	return scanner, documents, nil
}

func readJobOptions(path string, stdin io.Reader) (map[string]json.RawMessage, error) {
	if path == "" {
		return map[string]json.RawMessage{}, nil
	}
	reader := stdin
	if path != "-" {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return nil, errors.New("--job-options file is invalid or unavailable")
		}
		handle, err := os.Open(path)
		if err != nil {
			return nil, errors.New("--job-options file is unavailable")
		}
		defer handle.Close()
		actual, err := handle.Stat()
		if err != nil || !os.SameFile(info, actual) {
			return nil, errors.New("--job-options file is invalid")
		}
		reader = handle
	}
	if reader == nil {
		return nil, errors.New("--job-options input is unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return nil, errors.New("--job-options input exceeds bound or is unavailable")
	}
	options := map[string]json.RawMessage{}
	if decodeJSONObject(raw, &options) != nil || len(options) > 32 {
		return nil, errors.New("--job-options must be one bounded JSON object")
	}
	for name := range options {
		if name == "" || len(name) > 64 || strings.ContainsAny(name, "\x00\r\n") {
			return nil, errors.New("--job-options has an invalid option name")
		}
	}
	return options, nil
}
