package consoleweb

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedConsoleRouting(t *testing.T) {
	h := Handler()
	for _, tc := range []struct {
		method, path, accept string
		status               int
		html                 bool
	}{
		{"GET", "/console/", "", 200, true},
		{"HEAD", "/console/", "", 200, false},
		{"GET", "/console/apps/new", "text/html", 200, true},
		{"GET", "/console/apps/new", "application/json", 404, false},
		{"GET", "/console/assets/missing.js", "text/html", 404, false},
		{"GET", "/console/assets", "text/html", 404, false},
		{"GET", "/console/missing.css", "text/html", 404, false},
		{"GET", "/console/api/unknown", "text/html", 404, false},
		{"GET", "/console/mockup.html", "text/html", 404, false},
		{"GET", "/console/api", "text/html", 404, false},
		{"GET", "/other", "text/html", 404, false},
		{"POST", "/console/", "text/html", 405, false},
	} {
		t.Run(tc.method+tc.path+tc.accept, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("Accept", tc.accept)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, request)
			if rec.Code != tc.status {
				t.Fatalf("status %d, expected %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.html && !strings.Contains(rec.Body.String(), "<html") {
				t.Fatalf("missing HTML document: %s", rec.Body.String())
			}
			if tc.status == 200 && rec.Header().Get("Cache-Control") != "no-cache" {
				t.Fatalf("entry cache: %s", rec.Header().Get("Cache-Control"))
			}
			if tc.method == "HEAD" && rec.Body.Len() != 0 {
				t.Fatal("HEAD returned a body")
			}
		})
	}
}

func TestEmbeddedAssetsAreImmutableAndProductionOnly(t *testing.T) {
	files, err := fs.ReadDir(bundle, "dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("missing built assets")
	}
	for _, file := range files {
		request := httptest.NewRequest("GET", "/console/assets/"+file.Name(), nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, request)
		if rec.Code != 200 || rec.Body.Len() == 0 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("asset %s: %d", file.Name(), rec.Code)
		}
	}
	if _, err := fs.Stat(bundle, "dist/mockup.html"); err == nil {
		t.Fatal("mockup embedded in production")
	}
}
