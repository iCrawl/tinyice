package relay

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGeoIPDownloadCreatesMissingParentDirectory(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte("mmdb fixture")); err != nil {
		t.Fatalf("write gzip fixture: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}

	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(bytes.NewReader(gz.Bytes())),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
	t.Cleanup(func() {
		http.DefaultClient = originalClient
	})

	dir := filepath.Join(t.TempDir(), "var", "lib", "tinyice", "geoip")
	dest := filepath.Join(dir, dbipFilename)
	g := &GeoLookup{dir: dir}
	if err := g.downloadTo("https://example.invalid/dbip.mmdb.gz", dest); err != nil {
		t.Fatalf("downloadTo: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded db: %v", err)
	}
	if string(got) != "mmdb fixture" {
		t.Fatalf("downloaded db = %q, want fixture", got)
	}
}
