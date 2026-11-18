#!/usr/bin/env bash

toplevel="$(git rev-parse --show-toplevel)"
go build \
    -C "${toplevel}" \
    -o ~/.local/bin/tess \
    -v \
    ./cmd/tess
