#!/bin/sh
set -eu
mkdir -p bin
go build -o bin/planhotspot ./cmd/planhotspot
printf 'built %s\n' "$(pwd)/bin/planhotspot"

