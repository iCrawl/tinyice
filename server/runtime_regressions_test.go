package server

import (
	"bytes"
	"os"
	"testing"
)

func TestServerDoesNotBootstrapLegacyTemplates(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}

	if bytes.Contains(source, []byte("ParseFS(templateFS")) {
		t.Fatal("server bootstrap should not parse legacy Go templates when the Preact shell is the runtime")
	}

	if _, err := os.Stat("templates"); err == nil {
		t.Fatal("legacy server/templates directory should be removed once the Preact shell is the only runtime UI")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat templates directory: %v", err)
	}
}
