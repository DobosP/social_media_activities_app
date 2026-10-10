package web

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var guiPublicNormalizerBinary = flag.String("gui-public-normalizer-binary", "", "absolute private tooling binary built against the released core-v1.6 public golden API")
var guiPublicNormalizerSHA256 = flag.String("gui-public-normalizer-sha256", "", "actual SHA256 from the managed tooling build receipt")
var guiPublicNormalizerArchive = flag.String("gui-public-normalizer-archive", "", "unchanged released web-kit-go-core-v1.6.tgz used by the managed tooling build")

const guiPublicNormalizePolicy = "core-v1.6/golden.Normalize/strict"

type guiPublicNormalizer struct {
	path, binarySHA256, archivePath, archiveSHA256 string
	binding                                      map[string]any
}

func guiPublicBoundFile(name string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, fmt.Errorf("normalizer binding requires a canonical absolute path")
	}
	for parent := filepath.Dir(name); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("normalizer binding ancestry must be regular directories")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return nil, fmt.Errorf("normalizer binding must be a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || info.Mode() != opened.Mode() || info.Size() != opened.Size() {
		return nil, fmt.Errorf("normalizer binding changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, fmt.Errorf("normalizer binding could not be read exactly")
	}
	return raw, nil
}

func guiPublicNewNormalizer(root string) (*guiPublicNormalizer, error) {
	binary, digest, archive := *guiPublicNormalizerBinary, *guiPublicNormalizerSHA256, *guiPublicNormalizerArchive
	if binary == "" && digest == "" && archive == "" {
		return nil, nil // Omission is explicitly recorded, never a normalization pass.
	}
	decoded, err := hex.DecodeString(digest)
	if binary == "" || archive == "" || len(digest) != 64 || err != nil || len(decoded) != 32 || *guiPublicGoldenReference == "" {
		return nil, fmt.Errorf("normalization requires the original corpus, released archive and actual binary digest together")
	}
	var release struct {
		CoreTag string `json:"core_tag"`
		Source  string `json:"source_commit"`
		Policy  string `json:"policy"`
		Archive struct {
			File   string `json:"file"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"go_archive"`
		Files []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"released_regular_files"`
	}
	releaseRaw, err := os.ReadFile(filepath.Join(root, "tools/gui-public-golden/release-bindings.json"))
	if err != nil || json.Unmarshal(releaseRaw, &release) != nil || release.CoreTag != "core-v1.6" || release.Source != "b0ad658086a2745d1b3aea02659e6b644c89c680" || release.Policy != guiPublicNormalizePolicy || len(release.Files) != 41 || release.Archive.File != "web-kit-go-core-v1.6.tgz" || release.Archive.Bytes != 55202 || release.Archive.SHA256 != "d722a817074a01d8a983c3e8492335ee07d37fb0d58c9a938cd189df635e168d" {
		return nil, fmt.Errorf("released public normalizer source binding differs")
	}
	archiveRaw, err := guiPublicBoundFile(archive, 1<<20)
	if err != nil || filepath.Base(archive) != release.Archive.File || len(archiveRaw) != release.Archive.Bytes || guiPublicHash(archiveRaw) != release.Archive.SHA256 {
		return nil, fmt.Errorf("actual released Go archive differs")
	}
	binaryRaw, err := guiPublicBoundFile(binary, 64<<20)
	if err != nil || guiPublicHash(binaryRaw) != digest {
		return nil, fmt.Errorf("actual tooling binary differs from its build receipt digest")
	}
	var sdkSource string
	for _, file := range release.Files {
		if file.Path == "web-kit/golden/normalize.go" {
			sdkSource = file.SHA256
		}
	}
	if sdkSource != "a8539fe66388bb35aa94718843ef5241f72c7543242a5c4cc8a6f7c3b6bd57fa" {
		return nil, fmt.Errorf("released golden.Normalize source differs")
	}
	helper, err := os.ReadFile(filepath.Join(root, "tools/gui-public-golden/normalize.go"))
	if err != nil {
		return nil, err
	}
	return &guiPublicNormalizer{binary, digest, archive, release.Archive.SHA256, map[string]any{
		"policy": guiPublicNormalizePolicy, "core_source": release.Source, "go_archive_sha256": release.Archive.SHA256,
		"golden_normalize_source_sha256": sdkSource, "helper_source_sha256": guiPublicHash(helper), "binary_sha256": digest,
		"provenance_scope": "owner-managed build receipt plus exact supplied archive/binary/source bindings; not public receiver or app adoption qualification",
	}}, nil
}

type guiPublicLimitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *guiPublicLimitedBuffer) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.Len() {
		return 0, fmt.Errorf("normalizer process output exceeded its bound")
	}
	return b.Buffer.Write(raw)
}

func (b *guiPublicNormalizer) normalize(raw []byte) ([]byte, []string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, b.path)
	command.Stdin = bytes.NewReader(raw)
	output := &guiPublicLimitedBuffer{limit: 8 << 20}
	errors := &guiPublicLimitedBuffer{limit: 4 << 10}
	command.Stdout, command.Stderr = output, errors
	exit := command.Run()
	var result struct {
		Schema     int      `json:"schema"`
		Policy     string   `json:"policy"`
		Normalized []byte   `json:"normalized"`
		Hard       []string `json:"hard"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, nil, fmt.Errorf("released normalizer returned no complete diagnostic frame")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || result.Schema != 1 || result.Policy != guiPublicNormalizePolicy || len(result.Normalized) == 0 || len(result.Normalized) > 4<<20 {
		return nil, result.Hard, fmt.Errorf("released normalizer diagnostic frame differs")
	}
	if exit != nil || len(result.Hard) != 0 || errors.Len() != 0 {
		return result.Normalized, result.Hard, fmt.Errorf("released normalizer refused; hard_findings=%d, stderr_bytes=%d", len(result.Hard), errors.Len())
	}
	return result.Normalized, result.Hard, nil
}

func (b *guiPublicNormalizer) compare(original, current []byte) (map[string]any, error) {
	want, wantHard, wantErr := b.normalize(original)
	got, gotHard, gotErr := b.normalize(current)
	equal := bytes.Equal(want, got)
	report := map[string]any{"original_normalized_sha256": guiPublicHash(want), "current_normalized_sha256": guiPublicHash(got), "original_hard": wantHard, "current_hard": gotHard, "normalized_bytes_equal": equal, "original_success": wantErr == nil, "current_success": gotErr == nil, "diagnostic_pass": wantErr == nil && gotErr == nil && equal, "scope": "repeatability of two original-native synthetic renders only; not templ/Django/group acceptance"}
	if wantErr != nil || gotErr != nil || !equal {
		return report, fmt.Errorf("original-native released-normalizer diagnostic differs or refused")
	}
	return report, nil
}

func (b *guiPublicNormalizer) checkUnchanged() error {
	for name, digest := range map[string]string{b.path: b.binarySHA256, b.archivePath: b.archiveSHA256} {
		raw, err := guiPublicBoundFile(name, 64<<20)
		if err != nil || guiPublicHash(raw) != digest {
			return fmt.Errorf("normalizer binary/archive changed during rendering")
		}
	}
	return nil
}
