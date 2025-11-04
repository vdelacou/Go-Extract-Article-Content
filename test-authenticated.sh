#!/bin/bash

# Test script for authenticated Cloud Run requests
# Use this when public access (allUsers) is blocked by organization policy

SERVICE_URL=${1:-"https://extract-html-scraper-961436604049.us-central1.run.app"}
API_KEY=${2:-"2hvNE355UQKIZoGJAYxaNIKHb6WhlG8tHfJcT67iv6I="}
TEST_URL=${3:-"https://example.com"}

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
