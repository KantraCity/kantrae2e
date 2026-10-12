#!/bin/sh
# Creates deploy/.env with random secrets:  deploy/init-env.sh chat.example.com you@example.com
set -eu
DOMAIN=${1:?usage: deploy/init-env.sh <domain> <email for LetsEncrypt>}
EMAIL=${2:?usage: deploy/init-env.sh <domain> <email for LetsEncrypt>}
cd "$(dirname "$0")"
if [ -f .env ]; then
	echo "deploy/.env already exists; remove it first if you really want new secrets" >&2
	exit 1
fi
rnd() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }
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
# Go module mirror for building the images (uncomment if proxy.golang.org
# times out from this server):
# GOPROXY=https://goproxy.cn,https://goproxy.io,direct
EOT
echo "deploy/.env written for $DOMAIN (secrets are random; keep this file private and backed up)"
