#!/usr/bin/env bash
# Compatibility entry. The panel still offers this filename.
set -euo pipefail
here="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
exec bash "$here/dnsmarty-node.sh" "$@"
