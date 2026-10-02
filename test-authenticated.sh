#!/bin/bash

# Test script for authenticated Cloud Run requests
# Use this when public access (allUsers) is blocked by organization policy
#
# Usage: ./test-authenticated.sh SERVICE_URL API_KEY [TEST_URL]
# SERVICE_URL and API_KEY fall back to the SCRAPER_URL and SCRAPER_API_KEY
# environment variables. Prefer the variable for the key: arguments end up
# in shell history.

SERVICE_URL=${1:-$SCRAPER_URL}
API_KEY=${2:-$SCRAPER_API_KEY}
TEST_URL=${3:-"https://example.com"}

if [ -z "$SERVICE_URL" ] || [ -z "$API_KEY" ]; then
    echo "❌ Service URL and API key are required"
    echo "Usage: $0 SERVICE_URL API_KEY [TEST_URL]"
    echo "   or: set SCRAPER_URL and SCRAPER_API_KEY in the environment"
    exit 1
fi

echo "Testing authenticated request to Cloud Run service..."
echo "Service: $SERVICE_URL"
echo ""

# Get access token
TOKEN=$(gcloud auth print-identity-token)

# Make authenticated request
curl -X GET \
  "$SERVICE_URL?url=$TEST_URL&key=$API_KEY" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json"

echo ""
