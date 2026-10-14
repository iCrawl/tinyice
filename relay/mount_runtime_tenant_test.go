package relay

import "testing"

func TestRuntimeRegistryAttachesAndDetachesTenantMounts(t *testing.T) {
	r := NewRelay(false, nil)
	tm := NewTenantManager()
	tenant := tm.CreateTenant("tenant-a", "Tenant A", "free")
	other := tm.CreateTenant("tenant-b", "Tenant B", "free")

	rr := NewRuntimeRegistry(r)
	rr.SetTenantManager(tm)

	rr.AttachSource("/live", SourceIcecast, "tenant-a")
	if got := tenant.StreamCount(); got != 1 {
		t.Fatalf("expected tenant-a stream count 1, got %d", got)
	}

	rr.AttachSource("/live", SourceRelay, "tenant-b")
	if got := tenant.StreamCount(); got != 0 {
		t.Fatalf("expected tenant-a stream count 0 after transfer, got %d", got)
	}
	if got := other.StreamCount(); got != 1 {
		t.Fatalf("expected tenant-b stream count 1 after transfer, got %d", got)
	}

	rr.Remove("/live")
	if got := other.StreamCount(); got != 0 {
		t.Fatalf("expected tenant-b stream count 0 after remove, got %d", got)
	}
}
