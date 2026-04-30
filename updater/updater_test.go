package updater

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
)

type noopSwapper struct{}

func (noopSwapper) HotSwap() error { return nil }

func TestGetLatestChecksumRequiresRowForCurrentBinary(t *testing.T) {
	checksums := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  tinyice-other-os\n"))
	}))
	defer checksums.Close()

	u := NewUpdater(&config.Config{ChecksumURL: checksums.URL}, noopSwapper{})

	_, err := u.getLatestChecksum()
	if err == nil {
		t.Fatal("expected error when checksums.txt has no row for this binary")
	}
	if !strings.Contains(err.Error(), "no checksum row for tinyice-"+runtime.GOOS+"-"+runtime.GOARCH) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetLatestChecksumAcceptsCurrentBinaryRow(t *testing.T) {
	want := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	name := "tinyice-" + runtime.GOOS + "-" + runtime.GOARCH
	checksums := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(want + "  " + name + "\n"))
	}))
	defer checksums.Close()

	u := NewUpdater(&config.Config{ChecksumURL: checksums.URL}, noopSwapper{})

	got, err := u.getLatestChecksum()
	if err != nil {
		t.Fatalf("getLatestChecksum: %v", err)
	}
	if got != strings.ToLower(want) {
		t.Fatalf("expected %q, got %q", strings.ToLower(want), got)
	}
}
