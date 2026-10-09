#!/usr/bin/env bash

set -Eeuo pipefail

image_name="${IMAGE_NAME:-aiconnectionmanager:local}"
container_name="${CONTAINER_NAME:-aiconnectionmanager}"
host_port="${HOST_PORT:-8443}"
script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

if [ -z "$image_name" ] || [ -z "$container_name" ]; then
    printf '%s\n' 'IMAGE_NAME and CONTAINER_NAME must not be empty.' >&2
    exit 1
fi

if ! [[ "$host_port" =~ ^[0-9]+$ ]] || [ "$((10#$host_port))" -lt 1 ] || [ "$((10#$host_port))" -gt 65535 ]; then
    printf '%s\n' 'HOST_PORT must be a number between 1 and 65535.' >&2
    exit 1
fi

if ! command -v make >/dev/null 2>&1; then
    printf '%s\n' 'make was not found. Install make and try again.' >&2
    exit 1
fi

exec make -C "$script_directory" docker-run \
    IMAGE="$image_name" \
    CONTAINER="$container_name" \
    HOST_PORT="$host_port"
