#!/bin/bash
# Vegeta attack script for fixed load test
# Usage: ./attack.sh [rate] [duration] [target]

RATE=${1:-50}
DURATION=${2:-30s}
TARGET=${3:-http://localhost/extract}

echo "Generating targets..."
cat > targets.txt << TARGETEOF
POST ${TARGET}
@../../testdata/2020-Scrum-Guide-Spanish-Latin-South-American.pdf
Content-Type: application/pdf
TARGETEOF

echo "Running vegeta attack at ${RATE} rps for ${DURATION}..."
vegeta attack -rate=${RATE} -duration=${DURATION} -targets=targets.txt | vegeta report
