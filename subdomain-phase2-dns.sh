#!/usr/bin/env bash
# Phase 2: map www, app, and admin hostnames to ONE existing Cloud Run service.
# Run after subdomain-split code is deployed. Adjust SERVICE_NAME and REGION.
#
# Prerequisites:
#   - gcloud auth login
#   - Cloud Run service already serving the new host-router build
#   - shelftalker.ai verified for your Google account (if create fails):
#       gcloud domains verify shelftalker.ai
#     Check: gcloud domains list-user-verified
#   - DNS at your registrar (or Cloud DNS) ready for the records gcloud prints
#
# Usage:
#   export GOOGLE_CLOUD_PROJECT=your-project
#   export CLOUD_RUN_SERVICE=prototype-backend   # your service name
#   export CLOUD_RUN_REGION=europe-west1         # your region
#   ./subdomain-phase2-dns.sh

set -euo pipefail

PROJECT="${GOOGLE_CLOUD_PROJECT:?set GOOGLE_CLOUD_PROJECT}"
SERVICE="${CLOUD_RUN_SERVICE:-prototype-backend}"
REGION="${CLOUD_RUN_REGION:-europe-west1}"

echo "Project:  $PROJECT"
echo "Service:  $SERVICE"
echo "Region:   $REGION"
echo ""
echo "Creating domain mappings (one Cloud Run service, three hostnames)..."

for HOST in www.shelftalker.ai app.shelftalker.ai admin.shelftalker.ai; do
  echo "--- $HOST"
  gcloud beta run domain-mappings create \
    --service="$SERVICE" \
    --domain="$HOST" \
    --region="$REGION" \
    --project="$PROJECT" \
    || echo "(may already exist: $HOST)"
done

echo ""
echo "Apex shelftalker.ai: point DNS A/AAAA or CNAME per registrar; app returns 308 to www."
echo ""
echo "Prod smoke test (after DNS propagates):"
echo "  curl -sI https://app.shelftalker.ai/{validSurface} | head -1"
echo "  curl -sI https://www.shelftalker.ai/{surfaceId} | head -1   # expect 404"
echo "  curl -sI https://admin.shelftalker.ai/ | head -1"
echo "  curl -sI https://shelftalker.ai/ | grep -i location          # expect www"
