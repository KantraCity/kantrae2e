# Caddy with the rate limiting plugin (roadmap "Upgrade 2").
FROM caddy:2-builder AS builder
ARG CADDY_RATELIMIT_VERSION=v0.1.0
RUN xcaddy build --with github.com/mholt/caddy-ratelimit@${CADDY_RATELIMIT_VERSION}

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
