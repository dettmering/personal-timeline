#!/bin/sh
set -eu

# Secret aus Docker Secret File lesen
if [ -f /run/secrets/timeline_encryption_key ]; then
  export ENCRYPTION_KEY="$(cat /run/secrets/timeline_encryption_key)"
fi

exec /app/timeline
