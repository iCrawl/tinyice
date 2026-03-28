package server

import (
	"bytes"
	"os"
	"testing"
)

func TestOggOpusLabelingRemainsConsistent(t *testing.T) {
	adminHTML, err := os.ReadFile("templates/admin.html")
	if err != nil {
		t.Fatalf("read admin template: %v", err)
	}

	for _, want := range [][]byte{
		[]byte(`<option value="opus">Ogg/Opus</option>`),
		[]byte(`placeholder="MP3 to Ogg/Opus"`),
	} {
		if !bytes.Contains(adminHTML, want) {
			t.Fatalf("admin template missing %q", want)
		}
	}
	if bytes.Contains(adminHTML, []byte(`<option value="ogg">OGG</option>`)) {
		t.Fatal("admin template should not expose ogg as a separate selectable format")
	}

	openAPI, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}
	if !bytes.Contains(openAPI, []byte(`enum: [mp3, opus]`)) {
		t.Fatal("OpenAPI should advertise mp3 and opus as the accepted format values")
	}
	if bytes.Contains(openAPI, []byte(`enum: [mp3, opus, ogg]`)) {
		t.Fatal("OpenAPI should not advertise ogg as a separate format value")
	}
	if !bytes.Contains(openAPI, []byte(`configured for that mount (MP3 or Ogg/Opus).`)) {
		t.Fatal("OpenAPI stream description should describe the output as MP3 or Ogg/Opus")
	}
}
