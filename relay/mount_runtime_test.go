package relay

import "testing"

func TestRuntimeRegistryGetOrCreateMount(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)

	rt := reg.GetOrCreate("/live")
	if rt.Mount != "/live" {
		t.Fatalf("expected /live, got %q", rt.Mount)
	}
	if rt.Stream == nil {
		t.Fatal("expected runtime to resolve a backing stream")
	}
}

func TestRuntimeRegistryAttachSource(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)

	rt := reg.GetOrCreate("/live")
	reg.AttachSource("/live", SourceIcecast, "default")

	if rt.Source != SourceIcecast {
		t.Fatalf("expected source icecast, got %q", rt.Source)
	}
	if rt.TenantID != "default" {
		t.Fatalf("expected tenant default, got %q", rt.TenantID)
	}
}

func TestRuntimeRegistryRemoveMount(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)
	reg.GetOrCreate("/live")

	reg.Remove("/live")

	if _, ok := reg.Get("/live"); ok {
		t.Fatal("expected runtime to be removed")
	}
}
