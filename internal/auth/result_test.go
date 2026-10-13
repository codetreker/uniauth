package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codetreker/uniauth/internal/apps"
	"github.com/codetreker/uniauth/internal/providers"
)

func TestInProcessResultHandover(t *testing.T) {
	var logs bytes.Buffer
	f := newFixture(t, func(config *Config) {
		config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	})
	email, name, verified := "member@example.test", "Member", false
	identity := providers.Identity{Provider: "google", ID: "google:stable-member", Email: &email, Name: &name, EmailVerified: &verified, Origin: map[string]json.RawMessage{"profile": json.RawMessage(`{"unknown":{"nested":true},"email":null,"verified":false}`)}}
	f.google.exchange = func(context.Context, providers.ExchangeRequest) (providers.Identity, error) { return identity, nil }
	code := f.complete(f.start("platform-flow"))
	entry, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if string(entry.Identity) != string(expected) || entry.Provider != "google" || len(entry.OperationID) != 22 {
		t.Fatalf("handover lost identity or correlation: %+v", entry)
	}
	if !strings.Contains(logs.String(), `"phase":"result_consume"`) || !strings.Contains(logs.String(), `"operation_id":"`+entry.OperationID+`"`) {
		t.Fatalf("direct handover lost sanitized correlation: %s", &logs)
	}
	for _, private := range []string{code, f.app.Secret, email, "google:stable-member", `"nested":true`} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("direct handover logged private identity or credential: %s", &logs)
		}
	}
	assertCode(t, f.retrieve(code, &f.app), 410, "result_unavailable")
	if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("duplicate direct claim = %v", err)
	}

	httpCode := f.complete(f.start("http-first"))
	if response := f.retrieve(httpCode, &f.app); response.Code != http.StatusOK {
		t.Fatalf("HTTP handover = %d: %s", response.Code, response.Body)
	}
	if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, httpCode); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("direct claim after HTTP = %v", err)
	}
}

func TestHTTPAndInProcessShareAtomicClaims(t *testing.T) {
	f := newFixture(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for round := range 8 {
		code := f.complete(f.start("concurrent-platform-flow"))
		start, completed := make(chan struct{}), make(chan error, 16)
		var winners atomic.Int32
		for claimant := range 16 {
			go func() {
				<-start
				if claimant%2 == 0 {
					entry, err := f.h.ConsumeResult(ctx, f.app.App.ID, f.app.Secret, code)
					if err == nil {
						winners.Add(1)
						if len(entry.Identity) == 0 || entry.Provider != "google" {
							completed <- errors.New("direct claimant received incomplete identity")
							return
						}
					} else if !errors.Is(err, ErrUnavailable) {
						completed <- err
						return
					}
				} else {
					response := f.retrieve(code, &f.app)
					switch response.Code {
					case http.StatusOK:
						winners.Add(1)
					case http.StatusGone:
					default:
						completed <- errors.New("HTTP claimant received unexpected response: " + response.Body.String())
						return
					}
				}
				completed <- nil
			}()
		}
		close(start)
		for range 16 {
			select {
			case err := <-completed:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if winners.Load() != 1 {
			t.Fatalf("round %d handed over %d copies", round, winners.Load())
		}
	}
}

func TestInProcessResultRejectedClaimsDoNotConsume(t *testing.T) {
	f := newFixture(t, nil)
	code := f.complete(f.start("retained-result"))
	cases := []struct {
		name, appID, secret, code string
		want                      error
	}{
		{"wrong secret", f.app.App.ID, "wrong-secret", code, apps.ErrUnauthorized},
		{"credentials before code validation", f.app.App.ID, "wrong-secret", "", apps.ErrUnauthorized},
		{"unknown App", "missing", f.app.Secret, code, apps.ErrUnauthorized},
		{"wrong App", f.other.App.ID, f.other.Secret, code, ErrUnavailable},
		{"missing App ID", "", f.app.Secret, code, apps.ErrUnauthorized},
		{"oversized App ID", strings.Repeat("a", 129), f.app.Secret, code, apps.ErrUnauthorized},
		{"missing secret", f.app.App.ID, "", code, apps.ErrUnauthorized},
		{"oversized secret", f.app.App.ID, strings.Repeat("s", 513), code, apps.ErrUnauthorized},
		{"missing code", f.app.App.ID, f.app.Secret, "", ErrInvalidRequest},
		{"oversized code", f.app.App.ID, f.app.Secret, strings.Repeat("c", 513), ErrInvalidRequest},
		{"invalid UTF-8 code", f.app.App.ID, f.app.Secret, string([]byte{0xff}), ErrInvalidRequest},
		{"unknown code", f.app.App.ID, f.app.Secret, "missing", ErrUnavailable},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := f.h.ConsumeResult(context.Background(), test.appID, test.secret, test.code); !errors.Is(err, test.want) {
				t.Fatalf("claim = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); err != nil {
		t.Fatalf("rejected claims consumed original result: %v", err)
	}
}

func TestInProcessResultUsesLiveAppCredentials(t *testing.T) {
	for _, change := range []string{"disabled", "revoked"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, nil)
			code := f.complete(f.start("live-configuration"))
			ctx := context.Background()
			var secret = f.app.Secret
			if change == "disabled" {
				input := apps.AppInput{Name: f.app.App.Name, Enabled: false, RedirectURIs: f.app.App.RedirectURIs, Providers: f.app.App.Providers}
				if _, err := f.store.Update(ctx, f.ownerID, f.app.App.ID, input); err != nil {
					t.Fatal(err)
				}
			} else if err := f.store.RevokeSecret(ctx, f.ownerID, f.app.App.ID, f.app.SecretID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.ConsumeResult(ctx, f.app.App.ID, secret, code); !errors.Is(err, apps.ErrUnauthorized) {
				t.Fatalf("%s credential accepted: %v", change, err)
			}
			if change == "disabled" {
				input := apps.AppInput{Name: f.app.App.Name, Enabled: true, RedirectURIs: f.app.App.RedirectURIs, Providers: f.app.App.Providers}
				if _, err := f.store.Update(ctx, f.ownerID, f.app.App.ID, input); err != nil {
					t.Fatal(err)
				}
			} else {
				issued, err := f.store.CreateSecret(ctx, f.ownerID, f.app.App.ID, apps.SecretInput{Name: "Replacement"})
				if err != nil {
					t.Fatal(err)
				}
				secret = issued.Secret
			}
			if _, err := f.h.ConsumeResult(ctx, f.app.App.ID, secret, code); err != nil {
				t.Fatalf("rejected live configuration consumed result: %v", err)
			}
		})
	}
}

func TestInProcessResultExactExpiry(t *testing.T) {
	for _, offset := range []time.Duration{ResultTTL - time.Nanosecond, ResultTTL} {
		t.Run(offset.String(), func(t *testing.T) {
			f := newFixture(t, nil)
			code := f.complete(f.start("expiry-boundary"))
			f.clock.Add(int64(offset))
			_, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code)
			if offset < ResultTTL && err != nil {
				t.Fatalf("unexpired result rejected: %v", err)
			}
			if offset == ResultTTL && !errors.Is(err, ErrUnavailable) {
				t.Fatalf("expired result accepted: %v", err)
			}
			assertCode(t, f.retrieve(code, &f.app), 410, "result_unavailable")
		})
	}
}

func TestInProcessResultCancellationAndClosure(t *testing.T) {
	t.Run("accepted HTTP result survives disconnect", func(t *testing.T) {
		f := newFixture(t, nil)
		code := f.complete(f.start("disconnect-before-response"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := f.h.config.Now
		f.h.config.Now = func() time.Time {
			cancel()
			return clock()
		}
		request := httptest.NewRequest(http.MethodPost, "/auth/result", strings.NewReader(`{"code":"`+code+`"}`)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		request.SetBasicAuth(f.app.App.ID, f.app.Secret)
		response := httptest.NewRecorder()
		f.mux.ServeHTTP(response, request)
		f.h.config.Now = clock
		if response.Code != http.StatusOK || !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("accepted HTTP claim changed after disconnect: %d %v", response.Code, ctx.Err())
		}
		if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("disconnected HTTP result was restored: %v", err)
		}
	})
	t.Run("canceled before authentication", func(t *testing.T) {
		f := newFixture(t, nil)
		code := f.complete(f.start("cancel-before-authentication"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := f.h.ConsumeResult(ctx, f.app.App.ID, f.app.Secret, code); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled retrieval = %v", err)
		}
		ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		if _, err := f.h.ConsumeResult(ctx, f.app.App.ID, f.app.Secret, code); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired deadline retrieval = %v", err)
		}
		if _, err := f.h.ConsumeResult(nil, f.app.App.ID, f.app.Secret, code); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("nil context retrieval = %v", err)
		}
		if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); err != nil {
			t.Fatalf("canceled attempts consumed result: %v", err)
		}
	})
	t.Run("canceled while waiting for claim", func(t *testing.T) {
		f := newFixture(t, nil)
		code := f.complete(f.start("cancel-before-claim"))
		beforeClaim := make(chan struct{})
		clock := f.h.config.Now
		f.h.config.Now = func() time.Time {
			close(beforeClaim)
			return clock()
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		completed := make(chan error, 1)
		f.h.cache.mu.Lock()
		go func() {
			_, err := f.h.ConsumeResult(ctx, f.app.App.ID, f.app.Secret, code)
			completed <- err
		}()
		select {
		case <-beforeClaim:
		case <-time.After(time.Second):
			f.h.cache.mu.Unlock()
			t.Fatal("retrieval did not authenticate")
		}
		cancel()
		f.h.cache.mu.Unlock()
		select {
		case err := <-completed:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled claim = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled claim did not settle")
		}
		f.h.config.Now = clock
		if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); err != nil {
			t.Fatalf("canceled claim removed result: %v", err)
		}
	})
	t.Run("closed gateway", func(t *testing.T) {
		f := newFixture(t, nil)
		code := f.complete(f.start("close-before-consumption"))
		if err := f.h.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed retrieval = %v", err)
		}
	})
	t.Run("closed during claim admission", func(t *testing.T) {
		f := newFixture(t, nil)
		code := f.complete(f.start("close-during-consumption"))
		clock := f.h.config.Now
		f.h.config.Now = func() time.Time {
			if err := f.h.Close(); err != nil {
				t.Fatal(err)
			}
			return clock()
		}
		if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); !errors.Is(err, ErrClosed) {
			t.Fatalf("closure admitted a result: %v", err)
		}
		f.h.config.Now = clock
	})
	t.Run("unavailable storage", func(t *testing.T) {
		f := newFixture(t, nil)
		code := f.complete(f.start("storage-before-consumption"))
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := f.h.ConsumeResult(context.Background(), f.app.App.ID, f.app.Secret, code); err == nil || errors.Is(err, apps.ErrUnauthorized) || errors.Is(err, ErrUnavailable) {
			t.Fatalf("storage error was hidden: %v", err)
		}
	})
}
