#!/usr/bin/env bash
# Map www-test, app-test, and admin-test hostnames to the test Cloud Run service.
#
# Prerequisites:
#   - gcloud auth login
#   - prototype-backend-test already deployed
#   - shelftalker.ai verified for your Google account (if create fails):
#       gcloud domains verify shelftalker.ai
#   - DNS at your registrar ready for the records gcloud prints
#
# Usage:
#   export GOOGLE_CLOUD_PROJECT=spareeye-prototype-project
#   export CLOUD_RUN_REGION=europe-west1
#   ./test-domain-mappings.sh

set -euo pipefail

PROJECT="${GOOGLE_CLOUD_PROJECT:?set GOOGLE_CLOUD_PROJECT}"
SERVICE="${CLOUD_RUN_SERVICE:-prototype-backend-test}"
REGION="${CLOUD_RUN_REGION:-europe-west1}"

echo "Project:  $PROJECT"
echo "Service:  $SERVICE"
echo "Region:   $REGION"
echo ""
echo "Creating domain mappings (test Cloud Run service, three hostnames)..."

for HOST in www-test.shelftalker.ai app-test.shelftalker.ai admin-test.shelftalker.ai; do
  echo "--- $HOST"
  gcloud beta run domain-mappings create \
    --service="$SERVICE" \
    --domain="$HOST" \
    --region="$REGION" \
    --project="$PROJECT" \
    || echo "(may already exist: $HOST)"
done

echo ""
echo "Add the DNS records printed above at your registrar, then wait for TLS."
echo ""
echo "Test smoke check (after DNS propagates):"
echo "  curl -sI https://app-test.shelftalker.ai/{testSurface} | head -1"
echo "  curl -sI https://www-test.shelftalker.ai/{surfaceId} | head -1   # expect 404"
echo "  curl -sI https://admin-test.shelftalker.ai/ | head -1"
