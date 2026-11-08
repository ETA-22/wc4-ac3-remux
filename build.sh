#!/bin/sh
# Cross-compile static binaries into dist/ (Go 1.23+). Symbols are kept (no
# -s -w): stripped Go binaries trip more antivirus heuristics.
set -e
cd "$(dirname "$0")"
export CGO_ENABLED=0
mkdir -p dist
GOOS=linux   GOARCH=amd64 go build -trimpath -o dist/wc4-ac3-remux-linux-x64 .
# version resource + manifest for the Windows exe (tools/winres, standard library only)
VER=$(sed -n 's/^const version = "\(.*\)"/\1/p' main.go)
go run ./tools/winres "$VER"
GOOS=windows GOARCH=amd64 go build -trimpath -o dist/wc4-ac3-remux-windows-x64.exe .
cd dist && sha256sum wc4-ac3-remux-* > SHA256SUMS && cat SHA256SUMS
