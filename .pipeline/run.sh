#!/bin/bash
# OpenDB Autopilot — CI/CD Pipeline
# Runs Build → Test → Vet → Report in a loop.
# Usage: nohup .pipeline/run.sh &

set -e

INTERVAL=${PIPELINE_INTERVAL:-1800}  # 30 minutes default
REPORT_DIR=".pipeline/reports"
ISSUE_DIR=".pipeline/issues"
MAX_REPORTS=100

mkdir -p "$REPORT_DIR" "$ISSUE_DIR"

run_pipeline() {
    local ts=$(date +%Y%m%d_%H%M%S)
    local report="$REPORT_DIR/${ts}.md"
    local passed=0
    local failed=0
    local start=$(date +%s)

    echo "# Pipeline Report $ts" > "$report"
    echo "" >> "$report"

    # Stage 1: Build
    echo "## Build" >> "$report"
    if go build -tags full -o opendb ./cmd/opendb/ 2>> "$report"; then
        echo "- Build: **PASS**" >> "$report"
        ((passed++))
    else
        echo "- Build: **FAIL**" >> "$report"
        ((failed++))
        return 1
    fi
    echo "" >> "$report"

    # Stage 2: Vet
    echo "## Vet" >> "$report"
    if go vet -tags full ./internal/drone/... ./internal/overlord/... ./internal/cerebrate/... ./internal/cluster/... ./cmd/opendb/... 2>> "$report"; then
        echo "- Vet: **PASS**" >> "$report"
        ((passed++))
    else
        echo "- Vet: **FAIL**" >> "$report"
        ((failed++))
    fi
    echo "" >> "$report"

    # Stage 3: Unit Tests
    echo "## Tests" >> "$report"
    if go test -tags full -race ./internal/drone/ ./internal/cluster/ -count=1 -v 2>&1 | tee -a "$report" | tail -5; then
        echo "- Tests: **PASS**" >> "$report"
        ((passed++))
    else
        echo "- Tests: **FAIL**" >> "$report"
        ((failed++))
    fi
    echo "" >> "$report"

    # Summary
    local end=$(date +%s)
    local duration=$((end - start))
    echo "## Summary" >> "$report"
    echo "- Passed: $passed" >> "$report"
    echo "- Failed: $failed" >> "$report"
    echo "- Duration: ${duration}s" >> "$report"
    echo "- Timestamp: $(date)" >> "$report"

    # Cleanup old reports
    local count=$(ls -1 "$REPORT_DIR"/*.md 2>/dev/null | wc -l)
    if [ "$count" -gt "$MAX_REPORTS" ]; then
        ls -1t "$REPORT_DIR"/*.md | tail -n +$((MAX_REPORTS + 1)) | xargs rm -f
    fi

    if [ $failed -gt 0 ]; then
        echo "[pipeline] $ts: $failed FAILED (see $report)"
        return 1
    else
        echo "[pipeline] $ts: ALL PASSED ($passed stages, ${duration}s)"
        return 0
    fi
}

echo "[pipeline] Starting CI/CD loop (interval=${INTERVAL}s)"
echo "[pipeline] Reports: $REPORT_DIR"

while true; do
    echo ""
    echo "=== Pipeline run $(date) ==="
    if run_pipeline; then
        echo "[pipeline] Sleeping ${INTERVAL}s until next run..."
    else
        echo "[pipeline] Pipeline failed. Sleeping ${INTERVAL}s..."
    fi
    sleep "$INTERVAL"
done
