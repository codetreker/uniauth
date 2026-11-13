package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMicrosoftOptionalSourcesPreserveVerifiedIdentity(t *testing.T) {
	cases := []struct {
		name                      string
		mutate                    func(*fixture)
		profile, photo, photoNull bool
	}{
		{"profile unavailable", func(f *fixture) { f.status["/profile"] = 503 }, false, true, false},
		{"profile null", func(f *fixture) { f.profile = "null" }, false, true, false},
		{"profile malformed", func(f *fixture) { f.profile = "{" }, false, true, false},
		{"profile array", func(f *fixture) { f.profile = "[]" }, false, true, false},
		{"profile mail type", func(f *fixture) { f.profile = `{"mail":false,"displayName":"Rejected Name"}` }, false, true, false},
		{"profile name type", func(f *fixture) { f.profile = `{"mail":"rejected@example.test","displayName":123}` }, false, true, false},
		{"photo unavailable", func(f *fixture) { f.status["/photo"] = 403 }, true, false, false},
		{"photo empty", func(f *fixture) { f.photo = nil }, true, false, false},
		{"photo invalid type", func(f *fixture) { f.photoType = "text/plain" }, true, false, false},
		{"both unavailable", func(f *fixture) { f.status["/profile"], f.status["/photo"] = 503, 503 }, false, false, false},
		{"photo confirmed absent", func(f *fixture) { f.status["/photo"] = 404 }, true, true, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			f := newFixture(t)
			f.claims["email"], f.claims["name"] = "token@example.test", "Token Name"
			f.profile = `{"mail":"graph@example.test","displayName":"Graph Name","unknown":{"false":false,"null":null}}`
			item.mutate(f)
			var logs bytes.Buffer
			identity, err := f.provider(t, "microsoft").Exchange(WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil))), exchangeRequest())
			if err != nil || identity.Provider != "microsoft" || identity.ID != "msft:object-123@"+microsoftTenant || identity.EmailVerified != nil || identity.Origin["id_token_claims"] == nil {
				t.Fatalf("optional source changed authenticated identity: %+v %v", identity, err)
			}
			wantEmail, wantName := "token@example.test", "Token Name"
			if item.profile {
				wantEmail, wantName = "graph@example.test", "Graph Name"
			}
			if identity.Email == nil || *identity.Email != wantEmail || identity.Name == nil || *identity.Name != wantName {
				t.Fatalf("source selection changed: %+v", identity)
			}
			if _, present := identity.Origin["profile"]; present != item.profile {
				t.Fatalf("rejected/missing profile source was published: %s", identity.Origin["profile"])
			}
			if item.profile && string(identity.Origin["profile"]) != f.profile {
				t.Fatalf("accepted raw profile was modified: %s", identity.Origin["profile"])
			}
			if _, present := identity.Origin["photo"]; present != item.photo || (identity.AvatarURL != nil) != (item.photo && !item.photoNull) {
				t.Fatalf("photo availability was fabricated: %+v", identity)
			}
			if item.photoNull && string(identity.Origin["photo"]) != "null" {
				t.Fatalf("404 lost explicit absence: %s", identity.Origin["photo"])
			}
			var unavailable []string
			if !item.profile {
				unavailable = append(unavailable, "profile")
			}
			if !item.photo {
				unavailable = append(unavailable, "photo")
			}
			assertEnrichmentWarning(t, &logs, unavailable)
			if strings.Contains(logs.String(), "token@example.test") || strings.Contains(logs.String(), "Rejected Name") || strings.Contains(logs.String(), "provider-secret-sensitive-description") {
				t.Fatalf("optional failures disclosed identity/body values: %s", &logs)
			}
		})
	}
}

func assertEnrichmentWarning(t *testing.T, logs *bytes.Buffer, unavailable []string) {
	t.Helper()
	var warnings []map[string]any
	for _, record := range diagnosticRecords(t, logs) {
		if record["level"] == "WARN" {
			warnings = append(warnings, record)
		}
	}
	if len(unavailable) == 0 {
		if len(warnings) != 0 {
			t.Fatalf("accepted sources emitted degradation: %v", warnings)
		}
		return
	}
	if len(warnings) != 1 || warnings[0]["outcome"] != "partial" || warnings[0]["stage"] != "profile_enrichment" {
		t.Fatalf("missing bounded degradation warning: %v", warnings)
	}
	values, ok := warnings[0]["unavailable_sources"].([]any)
	if !ok || len(values) != len(unavailable) {
		t.Fatalf("incorrect unavailable sources: %v", warnings)
	}
	for index, source := range unavailable {
		if values[index] != source {
			t.Fatalf("incorrect unavailable sources: %v", warnings)
		}
	}
}

func TestMicrosoftBothSourcesUnavailableKeepsNullableEnvelope(t *testing.T) {
	f := newFixture(t)
	f.status["/profile"], f.status["/photo"] = 503, 403
	f.claims["preferred_username"], f.claims["userPrincipalName"] = "not-email@example.test", "not-email@example.test"
	var logs bytes.Buffer
	identity, err := f.provider(t, "microsoft").Exchange(WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil))), exchangeRequest())
	if err != nil || len(identity.Origin) != 1 {
		t.Fatalf("complete token identity was rejected: %+v %v", identity, err)
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil || len(envelope) != 7 {
		t.Fatalf("seven-field envelope changed: %s %v", encoded, err)
	}
	for _, field := range []string{"email", "email_verified", "name", "avatar_url"} {
		value, present := envelope[field]
		if !present || value != nil {
			t.Fatalf("nullable field %s was derived or omitted: %s", field, encoded)
		}
	}
}

func TestMicrosoftOptionalDeadlineAndCallerCancellation(t *testing.T) {
	for _, stage := range []string{"profile", "photo"} {
		for _, callerCanceled := range []bool{false, true} {
			name := stage + "/optional-timeout"
			if callerCanceled {
				name = stage + "/caller-canceled"
			}
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				f.claims["email"], f.claims["name"] = "token@example.test", "Token Name"
				base := f.server.Config.Handler
				entered, stopped := make(chan struct{}), make(chan struct{})
				var photoRequests atomic.Int32
				replaceDiagnosticServer(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/photo" {
						photoRequests.Add(1)
					}
					if r.URL.Path != "/"+stage {
						base.ServeHTTP(w, r)
						return
					}
					if stage == "photo" {
						w.Header().Set("Content-Type", "image/jpeg")
						w.Write([]byte{0, 1})
						w.(http.Flusher).Flush()
					}
					close(entered)
					<-r.Context().Done()
					close(stopped)
				}), false)
				provider := f.provider(t, "microsoft").(*Microsoft)
				provider.enrichmentTimeout = 150 * time.Millisecond
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var logs bytes.Buffer
				type outcome struct {
					identity Identity
					err      error
				}
				result := make(chan outcome, 1)
				go func() {
					identity, err := provider.Exchange(WithLogger(ctx, slog.New(slog.NewJSONHandler(&logs, nil))), exchangeRequest())
					result <- outcome{identity, err}
				}()
				select {
				case <-entered:
				case finished := <-result:
					t.Fatalf("enrichment not reached: %v", finished.err)
				case <-time.After(2 * time.Second):
					t.Fatal("enrichment did not start")
				}
				if callerCanceled {
					cancel()
				}
				var finished outcome
				select {
				case finished = <-result:
				case <-time.After(2 * time.Second):
					t.Fatal("enrichment did not stop")
				}
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("upstream request survived enrichment")
				}
				if callerCanceled {
					if !errors.Is(finished.err, context.Canceled) || finished.identity.ID != "" {
						t.Fatalf("cancellation returned a usable identity: %+v %v", finished.identity, finished.err)
					}
					assertEnrichmentWarning(t, &logs, nil)
				} else {
					if finished.err != nil || finished.identity.ID != "msft:object-123@"+microsoftTenant || ctx.Err() != nil {
						t.Fatalf("optional deadline ended authentication: %+v %v", finished.identity, finished.err)
					}
					unavailable := []string{"photo"}
					if stage == "profile" {
						unavailable = []string{"profile", "photo"}
					}
					assertEnrichmentWarning(t, &logs, unavailable)
					failure := diagnosticRecord(t, diagnosticRecords(t, &logs), stage+"_retrieval", "completed")
					if failure["error_cause"] != "deadline_exceeded" {
						t.Fatalf("optional timeout cause disappeared: %v", failure)
					}
					if stage == "photo" && (failure["status"] != float64(200) || failure["body_error_type"] != "context_deadline") {
						t.Fatalf("optional body stall lost its response evidence: %v", failure)
					}
				}
				if stage == "profile" && photoRequests.Load() != 0 {
					t.Fatal("photo was attempted after the shared optional budget ended")
				}
			})
		}
	}
}

func TestMicrosoftRejectsInvalidCoreBeforeOptionalRequests(t *testing.T) {
	for _, mutate := range []func(*fixture){
		func(f *fixture) { delete(f.claims, "oid") },
		func(f *fixture) { f.claims["tid"] = nil },
		func(f *fixture) { f.claims["email"] = []string{"invalid"} },
		func(f *fixture) { f.claims["name"] = false },
	} {
		f := newFixture(t)
		mutate(f)
		identity, err := f.provider(t, "microsoft").Exchange(context.Background(), exchangeRequest())
		if err == nil || errors.Unwrap(err) == nil || identity.ID != "" || slices.Contains(f.requests, "/profile") || slices.Contains(f.requests, "/photo") {
			t.Fatalf("optional profile bypassed invalid core: %+v %v requests=%v", identity, err, f.requests)
		}
	}
}

func TestMicrosoftOversizedOptionalSourcesAreOmitted(t *testing.T) {
	f := newFixture(t)
	base := f.server.Config.Handler
	replaceDiagnosticServer(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/profile" || r.URL.Path == "/photo" {
			w.Header().Set("Content-Length", "8388609")
			w.WriteHeader(http.StatusOK)
			return
		}
		base.ServeHTTP(w, r)
	}), false)
	var logs bytes.Buffer
	identity, err := f.provider(t, "microsoft").Exchange(WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil))), exchangeRequest())
	if err != nil || len(identity.Origin) != 1 || identity.AvatarURL != nil {
		t.Fatalf("oversize source escaped optional boundary: %+v %v", identity, err)
	}
	for _, stage := range []string{"profile_retrieval", "photo_retrieval"} {
		record := diagnosticRecord(t, diagnosticRecords(t, &logs), stage, "completed")
		if record["error_type"] != "response_too_large" {
			t.Fatalf("source limit failure was hidden: %v", record)
		}
	}
}

func TestMicrosoftCanceledCallerBeforeExchangeRemainsFatal(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	identity, err := f.provider(t, "microsoft").Exchange(ctx, exchangeRequest())
	if !errors.Is(err, context.Canceled) || identity.ID != "" {
		t.Fatalf("canceled authentication degraded into success: %+v %v", identity, err)
	}
}
