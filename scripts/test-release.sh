#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )) || [[ -z "$1" ]]; then
  printf 'Usage: %s IMAGE\n' "$0" >&2
  exit 2
fi

image=$1
repository=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd -- "$repository"
umask 077
mkdir -p .tmp
nonce=$(python3 -c 'import uuid; print(uuid.uuid4().hex)')
fixture=$(mktemp -d "$repository/.tmp/release-smoke.XXXXXXXX")
hash_container="uniauth-smoke-hash-$nonce"
password='release-smoke-administrator-password'

compose() {
  local environment=$1
  shift
  timeout --signal=TERM --kill-after=5s 90s docker compose \
    --project-name "uniauth-smoke-$environment-$nonce" \
    --env-file "$fixture/release.env" \
    --file "$fixture/$environment/compose.yml" "$@"
}

cleanup() {
  local status=$? cleanup_status=0 environment
  trap - EXIT
  set +e
  for environment in staging prod; do
    if [[ -f "$fixture/$environment/compose.yml" ]]; then
      if (( status != 0 )); then
        compose "$environment" logs --no-color
      fi
      compose "$environment" down --volumes --remove-orphans || cleanup_status=1
    fi
  done
  if timeout --signal=TERM --kill-after=5s 30s docker container inspect "$hash_container" >/dev/null 2>&1; then
    timeout --signal=TERM --kill-after=5s 30s docker container rm --force "$hash_container" || cleanup_status=1
  fi
  rm -rf -- "$fixture" || cleanup_status=1
  if (( status == 0 && cleanup_status != 0 )); then
    status=$cleanup_status
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

hash=$(printf '%s\n' "$password" |
  timeout --signal=TERM --kill-after=5s 90s docker run --rm --interactive \
    --name "$hash_container" -- "$image" hash-password)
printf 'UNIAUTH_IMAGE=%s\nUNIAUTH_HOST_PORT=0\n' "$image" > "$fixture/release.env"
for environment in staging prod; do
  mkdir "$fixture/$environment"
  cp deploy/compose.yml "$fixture/$environment/compose.yml"
  printf 'UNIAUTH_PUBLIC_URL=https://%s.uniauth.test\nUNIAUTH_ADMIN_PASSWORD_HASH=%s\n' \
    "$environment" "$hash" > "$fixture/$environment/.env"
  compose "$environment" up --detach --wait --wait-timeout 60
done

python3 - "$fixture" "$nonce" "$password" <<'PY'
import base64
import http.client
import json
import re
import subprocess
import sys
from http.cookies import SimpleCookie

fixture, nonce, password = sys.argv[1:]


def compose(environment, *arguments, capture=False):
    command = [
        "docker", "compose", "--project-name", f"uniauth-smoke-{environment}-{nonce}",
        "--env-file", f"{fixture}/release.env", "--file", f"{fixture}/{environment}/compose.yml",
        *arguments,
    ]
    result = subprocess.run(
        command, check=True, timeout=90, text=True,
        stdout=subprocess.PIPE if capture else None,
    )
    return result.stdout


def endpoint(environment):
    address = compose(environment, "port", "uniauth", "8080", capture=True).strip()
    host, port = address.rsplit(":", 1)
    assert host == "127.0.0.1", f"{environment} must publish only on loopback: {address}"
    return host, int(port)


def request(address, method, path, *, data=None, cookie=None, csrf=None, origin=None, authorization=None, status=200):
    headers = {}
    body = None
    if data is not None:
        body = json.dumps(data).encode()
        headers["Content-Type"] = "application/json"
    if cookie is not None:
        headers["Cookie"] = cookie
    if csrf is not None:
        headers["X-CSRF-Token"] = csrf
    if origin is not None:
        headers["Origin"] = origin
    if authorization is not None:
        headers["Authorization"] = authorization
    connection = http.client.HTTPConnection(*address, timeout=10)
    try:
        connection.request(method, path, body=body, headers=headers)
        response = connection.getresponse()
        content = response.read()
        assert response.status == status, (
            f"{method} {path}: expected HTTP {status}, received {response.status}"
        )
        return response.headers, content
    finally:
        connection.close()


def login(environment, address):
    headers, body = request(
        address, "POST", "/console/api/login", data={"password": password},
        origin=f"https://{environment}.uniauth.test",
    )
    cookies = SimpleCookie()
    cookies.load(headers["Set-Cookie"])
    session = cookies["__Host-uniauth_admin"]
    assert session["secure"] and session["httponly"]
    assert session["samesite"] == "Strict" and session["path"] == "/" and not session["domain"]
    csrf = json.loads(body)["csrf_token"]
    assert session.value and csrf
    return f"{session.key}={session.value}", csrf


def registry(address, cookie):
    _, body = request(address, "GET", "/console/api/apps", cookie=cookie)
    return json.loads(body)["apps"]


def data_volume(environment):
    container = compose(environment, "ps", "--quiet", "uniauth", capture=True).strip()
    mounts = json.loads(subprocess.check_output(
        ["docker", "container", "inspect", "--format", "{{json .Mounts}}", container],
        text=True, timeout=30,
    ))
    data = [mount for mount in mounts if mount["Destination"] == "/data"]
    assert len(data) == 1 and data[0]["Type"] == "volume"
    return data[0]["Name"]


addresses = {environment: endpoint(environment) for environment in ("staging", "prod")}
for environment, address in addresses.items():
    request(address, "GET", "/readyz")
    headers, page = request(address, "GET", "/console/")
    assert headers.get_content_type() == "text/html"
    assets = re.findall(rb'(?:src|href)="(/console/assets/[^"]+)"', page)
    assert assets, f"{environment} Console must reference embedded assets"
    for asset in assets:
        _, content = request(address, "GET", asset.decode())
        assert content, f"{environment} Console asset is empty"
    request(address, "GET", "/console/mockup.html", status=404)
    request(address, "GET", "/console/api/apps", status=401)

staging = addresses["staging"]
cookie, csrf = login("staging", staging)
assert registry(staging, cookie) == [], "Fresh staging volume must have an empty registry"
app_input = {
    "name": "Release smoke application", "enabled": True,
    "redirect_uris": ["https://product.uniauth.test/callback"], "providers": ["github"],
}
_, body = request(
    staging, "POST", "/console/api/apps", data=app_input, cookie=cookie, csrf=csrf,
    origin="https://staging.uniauth.test", status=201,
)
created = json.loads(body)
assert created["app"]["id"] and created["secret"] and created["secret_id"]
assert len(registry(staging, cookie)) == 1
print("Fresh volumes, embedded Console, administrator login, and App creation passed.", flush=True)

staging_volume = data_volume("staging")
assert staging_volume != data_volume("prod"), "Staging and production must use distinct data volumes"
compose("staging", "restart", "uniauth")
compose("staging", "up", "--detach", "--wait", "--wait-timeout", "60")
staging = endpoint("staging")
request(staging, "GET", "/readyz")
request(staging, "GET", "/console/api/session", cookie=cookie, status=401)
cookie, _ = login("staging", staging)
assert registry(staging, cookie) == [created["app"]], "Restart must preserve App configuration"
assert data_volume("staging") == staging_volume, "Restart must retain the staging data volume"
_, body = request(staging, "GET", f'/console/api/apps/{created["app"]["id"]}/secrets', cookie=cookie)
secrets = json.loads(body)["secrets"]
assert [secret["id"] for secret in secrets] == [created["secret_id"]]
assert created["secret"] not in body.decode(), "Secret listing must expose metadata only"
credentials = base64.b64encode(f'{created["app"]["id"]}:{created["secret"]}'.encode()).decode()
authorization = f"Basic {credentials}"
_, body = request(
    staging, "POST", "/auth/result", data={"code": "release-smoke-unavailable-result"},
    authorization=authorization, status=410,
)
assert json.loads(body)["error"]["code"] == "result_unavailable", "Restart must preserve valid App credentials"

prod_cookie, _ = login("prod", addresses["prod"])
assert registry(addresses["prod"], prod_cookie) == [], "Production registry must remain isolated"
request(addresses["prod"], "GET", "/console/api/session", cookie=cookie, status=401)
request(
    addresses["prod"], "POST", "/auth/result", data={"code": "release-smoke-unavailable-result"},
    authorization=authorization, status=401,
)
print("Restart session invalidation, App/credential persistence, and production isolation passed.", flush=True)
PY
