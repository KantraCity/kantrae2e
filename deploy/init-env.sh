#!/bin/sh
# Creates deploy/.env with random secrets:  deploy/init-env.sh chat.example.com you@example.com
# Adds secrets introduced later to an existing deploy/.env:  deploy/init-env.sh --upgrade
set -eu
rnd() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }
if [ "${1:-}" = "--upgrade" ]; then
	cd "$(dirname "$0")"
	[ -f .env ] || { echo "deploy/.env not found: run deploy/init-env.sh <domain> <email> first" >&2; exit 1; }
	if grep -q '^TURN_SECRET=' .env; then
		echo "deploy/.env is up to date"
	else
		printf 'TURN_SECRET=%s\n' "$(rnd 32)" >> .env
		echo "deploy/.env: added TURN_SECRET (calls)"
	fi
	exit 0
fi
DOMAIN=${1:?usage: deploy/init-env.sh <domain> <email for LetsEncrypt>}
EMAIL=${2:?usage: deploy/init-env.sh <domain> <email for LetsEncrypt>}
cd "$(dirname "$0")"
if [ -f .env ]; then
	echo "deploy/.env already exists; remove it first if you really want new secrets" >&2
	exit 1
fi
umask 077
cat > .env <<EOT
DOMAIN=$DOMAIN
ACME_EMAIL=$EMAIL
JWT_SECRET=$(rnd 48)
TOKEN_TTL=24h
INTERNAL_TOKEN=$(rnd 32)
POSTGRES_USER=kantra
POSTGRES_PASSWORD=$(rnd 24)
POSTGRES_DB=kantra
MINIO_ROOT_USER=kantra
MINIO_ROOT_PASSWORD=$(rnd 24)
# Calls: shared with coturn (TURN credentials).
TURN_SECRET=$(rnd 32)
# Go module mirror for building the images (uncomment if proxy.golang.org
# times out from this server):
# GOPROXY=https://goproxy.cn,https://goproxy.io,direct
EOT
echo "deploy/.env written for $DOMAIN (secrets are random; keep this file private and backed up)"
