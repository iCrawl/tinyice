package server

import (
	"bytes"
	"os"
	"testing"
)

func TestOggOpusLabelingRemainsConsistent(t *testing.T) {
	autoDJSource, err := os.ReadFile("frontend/src/pages/admin/AutoDJ.tsx")
	if err != nil {
		t.Fatalf("read AutoDJ page: %v", err)
	}

	transcodersSource, err := os.ReadFile("frontend/src/pages/admin/Transcoders.tsx")
	if err != nil {
		t.Fatalf("read Transcoders page: %v", err)
	}

	want := []byte(`<option value="opus">Ogg/Opus</option>`)
	if !bytes.Contains(autoDJSource, want) {
		t.Fatalf("AutoDJ page missing %q", want)
	}
	if !bytes.Contains(transcodersSource, want) {
		t.Fatalf("Transcoders page missing %q", want)
	}
	if bytes.Contains(autoDJSource, []byte(`<option value="ogg">OGG</option>`)) {
		t.Fatal("AutoDJ page should not expose ogg as a separate selectable format")
	}
	if bytes.Contains(transcodersSource, []byte(`<option value="ogg">OGG</option>`)) {
		t.Fatal("Transcoders page should not expose ogg as a separate selectable format")
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
