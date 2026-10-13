#!/usr/bin/env bash
set -euo pipefail

case "${DEPLOY_ENVIRONMENT:?DEPLOY_ENVIRONMENT must be set}" in
  staging|prod) ;;
  *) printf 'Unsupported deployment environment: %s\n' "$DEPLOY_ENVIRONMENT" >&2; exit 1 ;;
esac

: "${UNIAUTH_IMAGE:?UNIAUTH_IMAGE must be set}"
: "${UNIAUTH_HOST_PORT:?UNIAUTH_HOST_PORT must be set}"
: "${DEPLOY_PATH:?DEPLOY_PATH must be set}"
: "${OC_VM_HOST:?OC_VM_HOST must be set}"
: "${OC_VM_USER:?OC_VM_USER must be set}"
: "${OC_VM_SSH_KEY:?OC_VM_SSH_KEY must be set}"
: "${OC_VM_KNOWN_HOSTS:?OC_VM_KNOWN_HOSTS must be set}"
: "${REGISTRY:?REGISTRY must be set}"
: "${HARBOR_USER:?HARBOR_USER must be set}"
: "${HARBOR_API_TOKEN:?HARBOR_API_TOKEN must be set}"

digest=${UNIAUTH_IMAGE#"$REGISTRY/default/uniauth@sha256:"}
if [[ ! "$REGISTRY" =~ ^[a-zA-Z0-9.-]+(:[0-9]+)?$ ]] ||
   [[ ! "$digest" =~ ^[a-f0-9]{64}$ ]]; then
  printf 'UNIAUTH_IMAGE must be a default/uniauth digest on REGISTRY.\n' >&2
  exit 1
fi
if [[ ! "$UNIAUTH_HOST_PORT" =~ ^[1-9][0-9]{0,4}$ ]] || (( UNIAUTH_HOST_PORT > 65535 )); then
  printf 'UNIAUTH_HOST_PORT must be an integer from 1 to 65535.\n' >&2
  exit 1
fi
if [[ ! "$OC_VM_USER" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_-]*$ ]] ||
   [[ ! "$OC_VM_HOST" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]]; then
  printf 'OC_VM_USER and OC_VM_HOST must identify an SSH user and host.\n' >&2
  exit 1
fi
if [[ "$DEPLOY_PATH" != /* || "$DEPLOY_PATH" == / || "$DEPLOY_PATH" == *$'\n'* || "$DEPLOY_PATH" == *$'\r'* ]]; then
  printf 'DEPLOY_PATH must be an absolute deployment directory.\n' >&2
  exit 1
fi

umask 077
mkdir -p .tmp
package=$(mktemp -d .tmp/deploy.XXXXXX)
trap 'rm -rf "$package"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
printf '%s\n' "$OC_VM_SSH_KEY" > "$package/key"
printf '%s\n' "$OC_VM_KNOWN_HOSTS" > "$package/known_hosts"
printf 'UNIAUTH_IMAGE=%s\nUNIAUTH_HOST_PORT=%s\n' "$UNIAUTH_IMAGE" "$UNIAUTH_HOST_PORT" > "$package/release.env"
printf '%s' "$REGISTRY" > "$package/registry-host"
printf '%s' "$HARBOR_USER" > "$package/registry-user"
printf '%s' "$HARBOR_API_TOKEN" > "$package/registry-password"
cp deploy/compose.yml scripts/deploy-remote.sh "$package/"

ssh_options=(-i "$package/key" -o BatchMode=yes -o IdentitiesOnly=yes
  -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$package/known_hosts"
  -o ConnectTimeout=15 -o ServerAliveInterval=15 -o ServerAliveCountMax=3)

# The VM deadline terminates Docker children that inherit the deployment lock.
remote_script=$(cat <<'REMOTE'
set -euo pipefail
umask 077
deployment_path=$2
test -r "$deployment_path/.env"
directory=$(mktemp -d "$deployment_path/.uniauth-release.XXXXXX")
interrupted=false
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if ! rm -rf "$directory"; then status=1; fi
  if "$interrupted"; then
    # GNU timeout isolates this process group from the SSH login shell.
    kill -KILL 0
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'interrupted=true; exit 129' HUP
trap 'interrupted=true; exit 130' INT
trap 'interrupted=true; exit 143' TERM
tar -xf - -C "$directory"
# A builtin wait can be interrupted even when the child ignores TERM.
bash "$directory/deploy-remote.sh" "$directory" "$1" "$deployment_path" &
wait "$!"
REMOTE
)
quoted_script=${remote_script//\'/\'\\\'\'}
quoted_path=${DEPLOY_PATH//\'/\'\\\'\'}
remote_command="timeout --signal=TERM --kill-after=10s 480s bash -c '$quoted_script' -- '$DEPLOY_ENVIRONMENT' '$quoted_path'"

# shellcheck disable=SC2029
tar -cf - -C "$package" compose.yml deploy-remote.sh release.env registry-host registry-user registry-password |
  timeout --signal=TERM --kill-after=10s 540s \
    ssh "${ssh_options[@]}" "$OC_VM_USER@$OC_VM_HOST" "$remote_command"
