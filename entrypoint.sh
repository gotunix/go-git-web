#!/bin/sh
set -e

# Support dynamic environments, fallback to 1000 natively
PUID=${PUID:-1000}
PGID=${PGID:-1000}

echo "Ensuring UID:GID environment mapping is configured: ${PUID}:${PGID}"

# Create group if missing
if ! getent group gituser >/dev/null; then
    addgroup -g ${PGID} gituser
fi

# Create user if missing
if ! getent passwd gituser >/dev/null; then
    adduser -u ${PUID} -G gituser -h /home/gituser -D gituser
fi

# Dynamically apply ownership to the application
chown -R ${PUID}:${PGID} /app

# Safely drop privileges using su-exec and natively launch the Go application process
exec su-exec gituser "$@"
