package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/parquet-go/parquet-go"
)

func pointer[T any](v T) *T { return &v }
func fixtureOvertureRow(id, name string, lon, lat float64) overtureRow {
	var row overtureRow
	row.ID = id
	row.Names = &struct {
		Primary *string `parquet:"primary,optional"`
	}{Primary: pointer(name)}
	row.Categories = &struct {
		Primary   *string  `parquet:"primary,optional"`
		Alternate []string `parquet:"alternate,list,optional"`
	}{Primary: pointer("library"), Alternate: []string{"community_center"}}
	row.BBox = &struct {
		XMin float64 `parquet:"xmin"`
		YMin float64 `parquet:"ymin"`
		XMax float64 `parquet:"xmax"`
		YMax float64 `parquet:"ymax"`
	}{XMin: lon - 0.001, XMax: lon + 0.001, YMin: lat - 0.001, YMax: lat + 0.001}
	row.Addresses = []overtureAddress{{Freeform: pointer("Fixture Address"), Locality: pointer("Cluj-Napoca"), Postcode: pointer("400000"), Country: pointer("RO")}}
	row.Websites = []string{"https://library.fixture.test"}
	return row
}
func parquetFixture(t *testing.T, rows []overtureRow) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := parquet.NewGenericWriter[overtureRow](&out)
	if _, err := writer.Write(rows); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestNativeOvertureParquetFiltersBoundingBoxAndNormalizesSource(t *testing.T) {
	raw := parquetFixture(t, []overtureRow{fixtureOvertureRow("cluj-one", " Library Fixture ", 23.6, 46.7), fixtureOvertureRow("outside", "Outside", 10, 10), fixtureOvertureRow("cluj-two", "Second", 23.61, 46.71), fixtureOvertureRow("no-name", " ", 23.6, 46.7)})
	path := filepath.Join(t.TempDir(), "fixture.parquet")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	found := []commands.RawPlace{}
	err := (overtureSource{DefaultPath: path}).Fetch(context.Background(), commands.PlaceOptions{Source: "overture", BBox: "23.5,46.6,23.8,46.9", Limit: 1}, func(place commands.RawPlace) error { found = append(found, place); return nil })
	if err != nil || len(found) != 1 {
		t.Fatalf("Overture read failed: %v", err)
	}
	place := found[0]
	if place.Source != "overture" || place.ExternalID != "cluj-one" || place.Name != "Library Fixture" || place.Address["street"] != "Fixture Address" || place.Address["city"] != "Cluj-Napoca" || place.Website != "https://library.fixture.test" || place.Tags["overture:category"] != "library" {
		t.Fatal("source normalization differs")
	}
	if len(place.Tags["overture:alternate"].([]string)) != 1 {
		t.Fatal("alternate categories dropped")
	}
}
func TestNativeOvertureParquetRejectsInvalidFilesAndCancellation(t *testing.T) {
	for _, raw := range [][]byte{[]byte("not parquet"), append([]byte("PAR1"), make([]byte, 16)...)} {
		_, err := readOverture(context.Background(), bytes.NewReader(raw), int64(len(raw)), [4]float64{-180, -90, 180, 90}, new(int64), new(int), 0, func(commands.RawPlace) error { return nil })
		if err == nil {
			t.Fatal("malformed Parquet accepted")
		}
	}
	raw := parquetFixture(t, []overtureRow{fixtureOvertureRow("cluj-one", "Fixture", 23.6, 46.7)})
	corrupt := append([]byte{}, raw...)
	binary.LittleEndian.PutUint32(corrupt[len(corrupt)-8:], maxParquetFooter+1)
	if _, err := readOverture(context.Background(), bytes.NewReader(corrupt), int64(len(corrupt)), [4]float64{-180, -90, 180, 90}, new(int64), new(int), 0, func(commands.RawPlace) error { return nil }); err == nil {
		t.Fatal("unbounded footer accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readOverture(ctx, bytes.NewReader(raw), int64(len(raw)), [4]float64{-180, -90, 180, 90}, new(int64), new(int), 0, func(commands.RawPlace) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	for _, bbox := range []string{"", "NaN,0,1,1", "180,0,-180,1", "0,-91,1,1"} {
		if _, err := parseBoundingBox(bbox); err == nil {
			t.Fatal("invalid bounding box accepted")
		}
	}
}
func TestNativeRemoteParquetConditionalRangeReads(t *testing.T) {
	raw := parquetFixture(t, []overtureRow{fixtureOvertureRow("cluj-one", "Fixture", 23.6, 46.7)})
	ranges := 0
	client := &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodHead {
			return &http.Response{StatusCode: 200, ContentLength: int64(len(raw)), Header: http.Header{"Etag": []string{`"fixture-immutable-etag"`}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if request.Header.Get("If-Match") != `"fixture-immutable-etag"` || request.Header.Get("Accept-Encoding") != "identity" {
			t.Fatal("remote file identity not pinned")
		}
		parts := strings.Split(strings.TrimPrefix(request.Header.Get("Range"), "bytes="), "-")
		if len(parts) != 2 {
			t.Fatal("range missing")
		}
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		if start < 0 || end >= len(raw) || start > end {
			t.Fatal("invalid range")
		}
		ranges++
		return &http.Response{StatusCode: 206, ContentLength: int64(end - start + 1), Header: http.Header{"Content-Range": []string{fmt.Sprintf("bytes %d-%d/%d", start, end, len(raw))}, "Etag": []string{`"fixture-immutable-etag"`}}, Body: io.NopCloser(bytes.NewReader(raw[start : end+1]))}, nil
	})}
	found := 0
	err := (overtureSource{HTTP: client}).Fetch(context.Background(), commands.PlaceOptions{Source: "overture", OverturePath: "https://public.fixture.test/fixture.parquet", BBox: "23,46,24,47"}, func(commands.RawPlace) error { found++; return nil })
	if err != nil || found != 1 || ranges < 1 {
		t.Fatalf("remote Parquet failed: %v", err)
	}
	client.Transport = responseTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodHead {
			return &http.Response{StatusCode: 200, ContentLength: 20, Header: http.Header{"Etag": []string{`"initial"`}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": []string{"bytes 0-3/20"}, "Etag": []string{`"changed"`}}, Body: io.NopCloser(strings.NewReader("PAR1"))}, nil
	})
	remote, err := newParquetRemote(context.Background(), "https://public.fixture.test/fixture.parquet", client)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.close()
	if _, err = remote.ReadAt(make([]byte, 4), 0); err == nil {
		t.Fatal("mutable remote file accepted")
	}
}
func TestOvertureS3AnonymousListingIsBoundedAndDoesNotUseCredentials(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") != "" || request.Header.Get("X-Amz-Security-Token") != "" || request.URL.Host != "overturemaps-us-west-2.s3.us-west-2.amazonaws.com" || request.URL.Query().Get("prefix") != "release/fixture/theme=places/type=place/" {
			t.Fatal("nonanonymous source listing")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>release/fixture/theme=places/type=place/one.parquet</Key></Contents><Contents><Key>release/fixture/theme=places/type=place/ignore.json</Key></Contents></ListBucketResult>`)), Header: http.Header{}}, nil
	})}
	paths, err := (overtureSource{HTTP: client}).paths(context.Background(), "s3://overturemaps-us-west-2/release/fixture/theme=places/type=place/*")
	if err != nil || calls != 1 || len(paths) != 1 || !strings.HasSuffix(paths[0], "one.parquet") {
		t.Fatalf("S3 listing failed: %v", err)
	}
}

func TestOvertureColumnIndexSkipsUnrelatedRowGroups(t *testing.T) {
	var encoded bytes.Buffer
	writer := parquet.NewGenericWriter[overtureRow](&encoded)
	for _, row := range []overtureRow{fixtureOvertureRow("outside", "Outside", 10, 10), fixtureOvertureRow("cluj", "Cluj", 23.6, 46.7)} {
		if _, err := writer.Write([]overtureRow{row}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	scanned, emitted := int64(0), 0
	_, err := readOverture(context.Background(), bytes.NewReader(encoded.Bytes()), int64(encoded.Len()), [4]float64{23, 46, 24, 47}, &scanned, &emitted, 0, func(commands.RawPlace) error { return nil })
	if err != nil || scanned != 1 || emitted != 1 {
		t.Fatalf("unrelated row group was scanned: %v", err)
	}
}
