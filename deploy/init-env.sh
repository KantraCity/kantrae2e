#!/bin/sh
# Creates deploy/.env with random secrets:  deploy/init-env.sh chat.example.com you@example.com
# Adds secrets introduced later to an existing deploy/.env:  deploy/init-env.sh --upgrade
set -eu
rnd() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }
# Public IPv4 of this server (cloud VMs only see a private one on the interface).
public_ip() {
	for u in https://ipv4-internet.yandex.net/api/v0/ip https://api.ipify.org https://ifconfig.me/ip https://ipv4.icanhazip.com; do
		ip=$( (curl -fsS -m 5 "$u" 2>/dev/null || wget -qO- -T 5 "$u" 2>/dev/null) | tr -d '" \r\n')
		if echo "$ip" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$'; then echo "$ip"; return 0; fi
	done
	return 1
}
# The address of the interface that reaches the internet (private on cloud VMs).
local_ip() {
	ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}'
}
if [ "${1:-}" = "--upgrade" ]; then
	cd "$(dirname "$0")"
	[ -f .env ] || { echo "deploy/.env not found: run deploy/init-env.sh <domain> <email> first" >&2; exit 1; }
	changed=0
	if ! grep -q '^TURN_SECRET=' .env; then
		printf 'TURN_SECRET=%s\n' "$(rnd 32)" >> .env
		echo "deploy/.env: added TURN_SECRET (calls)"
		changed=1
	fi
	if ! grep -q '^TURN_EXTERNAL_IP=' .env; then
		if ip=$(public_ip); then
			printf 'TURN_EXTERNAL_IP=%s\n' "$ip" >> .env
			echo "deploy/.env: added TURN_EXTERNAL_IP=$ip (public address for calls)"
			changed=1
		else
			echo "could not detect the public IP: add TURN_EXTERNAL_IP=<public IPv4> to deploy/.env by hand" >&2
		fi
	fi
	if ! grep -q '^TURN_RELAY_IP=' .env; then
		if ip=$(local_ip) && [ -n "$ip" ]; then
			printf 'TURN_RELAY_IP=%s\n' "$ip" >> .env
			echo "deploy/.env: added TURN_RELAY_IP=$ip (this server's interface address for calls)"
			changed=1
		else
			echo "could not detect the interface address: add TURN_RELAY_IP=<address of eth0> to deploy/.env by hand" >&2
		fi
	fi
	[ "$changed" = 1 ] || echo "deploy/.env is up to date"
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
# Calls: shared with coturn (TURN credentials); the server's public IPv4.
TURN_SECRET=$(rnd 32)
TURN_EXTERNAL_IP=$(public_ip || true)
TURN_RELAY_IP=$(local_ip || true)
# Go module mirror for building the images (uncomment if proxy.golang.org
# times out from this server):
# GOPROXY=https://goproxy.cn,https://goproxy.io,direct
EOT
echo "deploy/.env written for $DOMAIN (secrets are random; keep this file private and backed up)"
