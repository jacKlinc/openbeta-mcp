#!/usr/bin/env bash
#
# Bring up the openbeta-graphql local dev stack, driven from this repo:
#   1. docker compose up -d (mongo replica set + mongo-express)
#   2. ensure `mongodb` resolves to 127.0.0.1 (the replica set advertises
#      itself as mongodb:27017, so replicaSet discovery fails without it)
#   3. wait for the replica set to elect a primary
#   4. yarn serve
#
# Usage:  ./scripts/dev-up.sh [yarn-script]     # default: serve
#         ./scripts/dev-up.sh serve-dev
#         GRAPHQL_DIR=/path/to/openbeta-graphql ./scripts/dev-up.sh
set -euo pipefail

GRAPHQL_DIR="${GRAPHQL_DIR:-$HOME/repos/openbeta/openbeta-graphql}"

if [[ ! -f "$GRAPHQL_DIR/docker-compose.yml" ]]; then
  echo "error: no docker-compose.yml under GRAPHQL_DIR ($GRAPHQL_DIR)" >&2
  echo "       set GRAPHQL_DIR to your openbeta-graphql checkout" >&2
  exit 1
fi

cd "$GRAPHQL_DIR"

YARN_SCRIPT="${1:-serve}"

echo "==> Using graphql repo at $GRAPHQL_DIR"

echo "==> Starting containers"
docker compose up -d

echo "==> Checking hostname resolution for 'mongodb'"
if getent hosts mongodb >/dev/null; then
  echo "    ok: $(getent hosts mongodb)"
else
  echo "    'mongodb' does not resolve; adding '127.0.0.1 mongodb' to /etc/hosts (sudo)"
  echo '127.0.0.1 mongodb' | sudo tee -a /etc/hosts >/dev/null
fi

echo "==> Waiting for replica set primary"
MONGO_CID="$(docker compose ps -q mongo_opentacos)"
if [[ -z "$MONGO_CID" ]]; then
  echo "    error: mongo_opentacos container not found" >&2
  exit 1
fi

# shellcheck disable=SC1091
set -a; source .env; set +a

primary_ready() {
  docker exec "$MONGO_CID" mongo \
    -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" \
    --quiet --eval 'quit(rs.status().myState === 1 ? 0 : 1)' >/dev/null 2>&1
}

for _ in $(seq 1 60); do
  if primary_ready; then
    echo "    primary is up"
    break
  fi
  sleep 2
done

if ! primary_ready; then
  echo "    error: timed out waiting for a replica set primary" >&2
  docker compose logs --tail 30 mongo_opentacos mongosetup >&2
  exit 1
fi

# openbeta-graphql pins node >=18.20.0 <19 (package.json engines). `nvm` is a
# shell function, not a binary, so it must be sourced before it can be used here.
echo "==> Selecting node ${NODE_VERSION:-18}"
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
if [[ -s "$NVM_DIR/nvm.sh" ]]; then
  set +u  # nvm.sh trips over `set -u`
  # shellcheck disable=SC1091
  . "$NVM_DIR/nvm.sh"
  if ! nvm use "${NODE_VERSION:-18}" >/dev/null; then
    echo "    error: node ${NODE_VERSION:-18} not installed; run: nvm install 18" >&2
    exit 1
  fi
  set -u
  echo "    node $(node -v)"
else
  echo "    warn: nvm not found at $NVM_DIR; using node $(node -v)" >&2
fi

echo "==> yarn $YARN_SCRIPT"
exec yarn "$YARN_SCRIPT"
