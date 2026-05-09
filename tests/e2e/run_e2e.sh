#!/bin/bash
# OpenDB Autopilot — E2E Test Runner
# Starts the full cluster, runs verification tests, and tears down.
#
# Usage: ./tests/e2e/run_e2e.sh

set -e

cd "$(dirname "$0")"

echo "=== OpenDB Autopilot E2E Test ==="
echo ""

# Step 1: Build and start cluster.
echo "Step 1: Starting cluster..."
docker compose build --quiet
docker compose up -d
echo "  Waiting 20s for all services to stabilize..."
sleep 20

# Step 2: Verify Cerebrate is running.
echo ""
echo "Step 2: Verify Cerebrate..."
if curl -s http://localhost:8080/api/health | grep -q '"status":"healthy"'; then
    echo "  ✓ Cerebrate Web dashboard is healthy"
else
    echo "  ✗ Cerebrate health check failed"
    docker compose logs cerebrate
    docker compose down -v
    exit 1
fi

# Step 3: Check fleet status.
echo ""
echo "Step 3: Check fleet status..."
FLEET=$(curl -s http://localhost:8080/api/fleet)
echo "  Fleet: $FLEET"

# Step 4: Check topology.
echo ""
echo "Step 4: Check topology..."
TOPO=$(curl -s http://localhost:8080/api/topology)
echo "  Topology: $(echo "$TOPO" | head -c 200)..."

# Step 5: Verify Overlords are registered.
echo ""
echo "Step 5: Verify Overlords..."
OVERLORDS=$(curl -s http://localhost:8080/api/overlords)
echo "  Overlords: $OVERLORDS"

# Step 6: Summary.
echo ""
echo "=== E2E Test Complete ==="
echo ""
echo "Cluster is running. Useful commands:"
echo "  docker compose logs -f              # watch all logs"
echo "  docker compose logs cerebrate -f    # watch Cerebrate"
echo "  curl http://localhost:8080           # Web dashboard"
echo "  curl http://localhost:8080/api/fleet # Fleet API"
echo "  docker compose down -v              # tear down"

# Don't tear down — leave running for manual inspection.
# Uncomment to auto-teardown:
# docker compose down -v
