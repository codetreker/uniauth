#!/usr/bin/env bash
set -euo pipefail

incoming=${1:?Release directory is required}
environment=${2:?Deployment environment is required}
directory=${3:?Deployment directory is required}
case "$environment" in
  staging|prod) ;;
  *) printf 'Unsupported deployment environment: %s\n' "$environment" >&2; exit 1 ;;
esac

test -r "$directory/.env"
exec 9> "$directory/.uniauth-deploy.lock"
flock -w 180 9

export DOCKER_CONFIG="$incoming/docker"
mkdir -m 700 "$DOCKER_CONFIG"
docker login "$(cat "$incoming/registry-host")" --username "$(cat "$incoming/registry-user")" \
  --password-stdin < "$incoming/registry-password"

ln -s "$directory/.env" "$incoming/.env"
compose=(docker compose --project-name "uniauth-$environment" --env-file "$incoming/release.env" --file "$incoming/compose.yml")
"${compose[@]}" config --quiet
"${compose[@]}" pull

install -m 600 "$incoming/compose.yml" "$directory/compose.yml"
install -m 600 "$incoming/release.env" "$directory/release.env"
compose=(docker compose --project-name "uniauth-$environment" --env-file "$directory/release.env" --file "$directory/compose.yml")
"${compose[@]}" up --detach --wait --wait-timeout 120 --remove-orphans
"${compose[@]}" ps
