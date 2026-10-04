package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/backup"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
)

func validateOperatorFlags(o cliOptions) error {
	count := 0
	for _, yes := range []bool{o.CSPDigest, o.BackupProbe, o.BackupUpload != "", o.BackupDownload != ""} {
		if yes {
			count++
		}
	}
	if count > 1 || count > 0 && (o.Job != "" || o.Due || o.Migrate || o.MigrateOnly || o.JobOptions != "") {
		return errors.New("standalone operator flags cannot be combined with database commands")
	}
	if o.OutputFormat != "text" && o.OutputFormat != "json" {
		return errors.New("--format must be text or json")
	}
	if !o.CSPDigest && (o.OperatorInput != "-" || o.OutputFormat != "text") {
		return errors.New("--input and --format require --csp-digest")
	}
	if o.BackupDownload != "" && o.BackupFile == "" || o.BackupDownload == "" && o.BackupFile != "" || o.BackupUpload == "" && o.BackupKey != "" {
		return errors.New("backup source/destination flags are incompatible")
	}
	if o.BackupMaxBytes < 1 || o.BackupMaxBytes > backup.AbsoluteMaxBytes || o.BackupTimeout < time.Second || o.BackupTimeout > time.Hour {
		return errors.New("backup bounds are invalid")
	}
	return nil
}

func runOperator(ctx context.Context, o cliOptions, get environment, stdin io.Reader, stdout io.Writer, presence ...environmentPresence) (bool, error) {
	if o.CSPDigest {
		return true, runCSPDigest(ctx, o, stdin, stdout)
	}
	if !o.BackupProbe && o.BackupUpload == "" && o.BackupDownload == "" {
		return false, nil
	}
	config, err := backupConfiguration(get, presence...)
	if err != nil {
		return true, err
	}
	config.MaxBytes, config.Timeout = o.BackupMaxBytes, o.BackupTimeout
	service, err := backup.New(config)
	if err != nil {
		return true, err
	}
	defer service.Close()
	var result backup.Receipt
	switch {
	case o.BackupUpload != "":
		key := o.BackupKey
		if key == "" {
			key = backup.Key(time.Now())
		}
		result, err = service.Upload(ctx, o.BackupUpload, key)
	case o.BackupDownload != "":
		result, err = service.Download(ctx, o.BackupDownload, o.BackupFile)
	case o.BackupProbe:
		result, err = service.Probe(ctx)
	}
	if err != nil {
		return true, err
	}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		return true, errors.New("backup receipt output unavailable")
	}
	return true, nil
}

func backupConfiguration(get environment, presence ...environmentPresence) (backup.Config, error) {
	d := newDecoder(get, presence...)
	storage := media.S3Config{Endpoint: get("MEDIA_S3_ENDPOINT_URL"), Bucket: get("MEDIA_S3_BUCKET"), Region: get("MEDIA_S3_REGION"), AccessKey: get("AWS_ACCESS_KEY_ID"), SecretKey: get("AWS_SECRET_ACCESS_KEY"), SessionToken: get("AWS_SESSION_TOKEN"), SSE: get("MEDIA_S3_SSE"), AddressingStyle: d.value("MEDIA_S3_ADDRESSING_STYLE", "auto"), EUResidencyVerified: d.boolean("MEDIA_EU_RESIDENCY_VERIFIED", false), PrivateBucketVerified: d.boolean("MEDIA_PRIVATE_BUCKET_VERIFIED", false)}
	if d.err != nil {
		return backup.Config{}, d.err
	}
	for _, check := range []struct {
		name  string
		valid bool
	}{{"MEDIA_EU_RESIDENCY_VERIFIED", storage.EUResidencyVerified}, {"MEDIA_PRIVATE_BUCKET_VERIFIED", storage.PrivateBucketVerified}, {"MEDIA_S3_SSE", storage.SSE != ""}, {"MEDIA_S3_ENDPOINT_URL", storage.Endpoint != ""}, {"MEDIA_S3_BUCKET", storage.Bucket != ""}, {"MEDIA_S3_REGION", storage.Region != ""}, {"AWS_ACCESS_KEY_ID", storage.AccessKey != ""}, {"AWS_SECRET_ACCESS_KEY", storage.SecretKey != ""}} {
		if !check.valid {
			return backup.Config{}, errors.New(check.name + " is required for native backup")
		}
	}
	return backup.Config{Storage: storage}, nil
}

func runCSPDigest(ctx context.Context, o cliOptions, stdin io.Reader, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	reader := stdin
	if o.OperatorInput != "-" {
		f, err := os.OpenFile(o.OperatorInput, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return errors.New("CSP digest input file unavailable")
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return errors.New("CSP digest input must be a bounded regular file")
		}
		reader = f
	}
	if reader == nil {
		return errors.New("CSP digest input unavailable")
	}
	// Native stdin and regular files are closable, so cancellation/timeout can
	// interrupt a stalled operator pipe rather than leaving the process hung.
	if closer, ok := reader.(io.Closer); ok {
		stop := context.AfterFunc(ctx, func() { _ = closer.Close() })
		defer stop()
	}
	summary, err := ops.ReadCSPDigest(operatorContextReader{ctx, reader})
	if ctx.Err() != nil {
		return errors.New("CSP digest input deadline or cancellation reached")
	}
	if err != nil {
		return err
	}
	if o.OutputFormat == "json" {
		if err = json.NewEncoder(stdout).Encode(summary); err != nil {
			return errors.New("CSP digest output unavailable")
		}
		return nil
	}
	if _, err = fmt.Fprintf(stdout, "CSP reports: total=%d malformed=%d groups=%d\n", summary.Total, summary.Malformed, len(summary.Groups)); err != nil {
		return errors.New("CSP digest output unavailable")
	}
	for _, group := range summary.Groups {
		if _, err = fmt.Fprintf(stdout, "%5d %s blocked=%s doc=%s\n", group.Count, group.Directive, group.Blocked, group.Document); err != nil {
			return errors.New("CSP digest output unavailable")
		}
	}
	return nil
}

type operatorContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r operatorContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
