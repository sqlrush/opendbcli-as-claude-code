#!/bin/sh
set -eu
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -x "$DIR/bin/planhotspot" ]; then
  exec "$DIR/bin/planhotspot"
fi
if command -v go >/dev/null 2>&1; then
  cd "$DIR"
  exec go run ./cmd/planhotspot
fi
printf 'go_plan_hotspot_analyzer 无法运行：未找到 bin/planhotspot，且 PATH 中没有 go。\n' >&2
printf '处理建议：在客户沙箱中执行 ./build.sh 预编译二进制。\n' >&2
exit 2

