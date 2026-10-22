#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p .tmp/consumers .tmp/go-tmp .tmp/tmp
export GOCACHE="$PWD/.tmp/go-build" GOMODCACHE="$PWD/.tmp/go-mod" GOPATH="$PWD/.tmp/go"
export GOTMPDIR="$PWD/.tmp/go-tmp" TMPDIR="$PWD/.tmp/tmp" GOWORK=off
consumer_dir="$(mktemp -d "$PWD/.tmp/consumers/go-XXXXXX")"
trap 'python3 - "$consumer_dir" <<"PY"
import shutil
import sys
shutil.rmtree(sys.argv[1])
PY' EXIT

cat > "$consumer_dir/go.mod" <<'EOF'
module example.com/product

go 1.27.1

require github.com/codetreker/uniauth/sdk/go v0.0.0

replace github.com/codetreker/uniauth/sdk/go => ../../../sdk/go
EOF

cat > "$consumer_dir/consumer_test.go" <<'EOF'
package product

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/codetreker/uniauth/sdk/go"
)

func TestExternalProduct(t *testing.T) {
	client, err := uniauth.New(uniauth.Config{
		BaseURL: "https://auth.example", AppID: "app_product", AppSecret: "product-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	state := uniauth.GenerateState()
	for _, provider := range []uniauth.Provider{uniauth.Microsoft, uniauth.Google, uniauth.GitHub} {
		link, err := client.AuthorizationURL(uniauth.AuthorizationRequest{
			Provider: provider, RedirectURI: "https://product.example/callback?tenant=example", State: state,
		})
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(link)
		if err != nil || parsed.Query().Get("state") != state || parsed.Query().Get("provider") != string(provider) {
			t.Fatal("authorization URL lost the product's login parameters")
		}
	}
	code, err := uniauth.ParseCallback(url.Values{"state": {state}, "code": {"result-code"}}, state)
	if err != nil || code != "result-code" {
		t.Fatal("callback could not be validated")
	}
	_, err = uniauth.ParseCallback(url.Values{"state": {state}, "error": {"provider_denied"}}, state)
	var callbackError *uniauth.CallbackError
	if !errors.As(err, &callbackError) || callbackError.Code != "provider_denied" {
		t.Fatal("callback error type is not available to an external product")
	}
	var discover func(context.Context, string) ([]uniauth.Provider, error) = client.Providers
	_ = discover
	var retrieve func(context.Context, string) (*uniauth.Identity, error) = client.Retrieve
	_ = retrieve
	identity := uniauth.Identity{Provider: uniauth.GitHub, ID: "github:123", Origin: map[string]json.RawMessage{"profile": json.RawMessage(`{"id":9007199254740993}`)}}
	if string(identity.Origin["profile"]) != `{"id":9007199254740993}` {
		t.Fatal("source JSON lost precision")
	}
}
EOF

cd "$consumer_dir"
go test -race -count=1 -timeout=30s ./...
if [[ "$(go list -m all | wc -l)" -ne 2 ]]; then
    printf 'The Go SDK consumer unexpectedly depends on external modules.\n' >&2
    exit 1
fi
printf 'Standalone Go consumer passed with standard-library dependencies only.\n'
