package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDomainRouteRegistrarsWireRepresentativePaths(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()

	s.registerAdminRoutes(mux)
	s.registerAuthRoutes(mux)
	s.registerAPIRoutes(mux)
	s.registerPublicRoutes(mux)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "admin events", method: http.MethodGet, path: "/admin/events", wantStatus: http.StatusUnauthorized},
		{name: "oidc providers", method: http.MethodGet, path: "/api/oidc/providers", wantStatus: http.StatusOK},
		{name: "streams api", method: http.MethodGet, path: "/api/streams", wantStatus: http.StatusUnauthorized},
		{name: "streams update api", method: http.MethodPut, path: "/api/streams", wantStatus: http.StatusUnauthorized},
		{name: "listeners api", method: http.MethodGet, path: "/api/listeners", wantStatus: http.StatusUnauthorized},
		{name: "public legacy stats", method: http.MethodGet, path: "/status-json.xsl", wantStatus: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d for %s %s, got %d", tc.wantStatus, tc.method, tc.path, rr.Code)
			}
		})
	}
}
