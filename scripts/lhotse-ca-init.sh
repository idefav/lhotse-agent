#!/bin/sh
set -eu

: "${LHOTSE_CA_URL:=http://127.0.0.1:15030/ca.crt}"
: "${LHOTSE_CA_FILE:=/tmp/lhotse-ca-bundle.pem}"
: "${LHOTSE_CA_REFRESH_INTERVAL:=300}"
: "${LHOTSE_CA_INIT_TIMEOUT:=120}"

fetch_ca_bundle() {
  tmp_file="${LHOTSE_CA_FILE}.tmp.$$"
  dir_name=$(dirname "$LHOTSE_CA_FILE")
  mkdir -p "$dir_name"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$LHOTSE_CA_URL" -o "$tmp_file"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$tmp_file" "$LHOTSE_CA_URL"
  else
    echo "lhotse-ca-init: curl or wget is required" >&2
    return 1
  fi

  mv "$tmp_file" "$LHOTSE_CA_FILE"
}

wait_for_initial_ca() {
  start_ts=$(date +%s)
  while ! fetch_ca_bundle; do
    now_ts=$(date +%s)
    if [ $((now_ts - start_ts)) -ge "$LHOTSE_CA_INIT_TIMEOUT" ]; then
      echo "lhotse-ca-init: timed out waiting for $LHOTSE_CA_URL" >&2
      exit 1
    fi
    sleep 2
  done
}

refresh_loop() {
  while :; do
    sleep "$LHOTSE_CA_REFRESH_INTERVAL"
    if ! fetch_ca_bundle; then
      echo "lhotse-ca-init: background refresh failed for $LHOTSE_CA_URL" >&2
    fi
  done
}

wait_for_initial_ca

export SSL_CERT_FILE="$LHOTSE_CA_FILE"
export REQUESTS_CA_BUNDLE="$LHOTSE_CA_FILE"
export NODE_EXTRA_CA_CERTS="$LHOTSE_CA_FILE"

case "$LHOTSE_CA_REFRESH_INTERVAL" in
  ''|*[!0-9]*)
    echo "lhotse-ca-init: LHOTSE_CA_REFRESH_INTERVAL must be an integer number of seconds" >&2
    exit 1
    ;;
esac

if [ "$LHOTSE_CA_REFRESH_INTERVAL" -gt 0 ]; then
  refresh_loop &
fi

if [ "$#" -eq 0 ]; then
  exec sh
fi

exec "$@"
