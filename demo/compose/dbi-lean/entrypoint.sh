#!/bin/sh
set -eu
mkdir -p /tmp/dynamorio-config /tmp/dynamorio-logs
exec /shimmy "$@"
