#!/usr/bin/env bash
# Build tess from this checkout and put it where Go puts installed binaries:
# $GOBIN, else $GOPATH/bin, usually ~/go/bin. Make sure that is on PATH.
# The binary records the commit it was built from; see `tess version`.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
go install ./cmd/tess
echo "installed $(go env GOPATH)/bin/tess"
