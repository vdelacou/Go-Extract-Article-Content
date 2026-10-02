#!/bin/bash
# Google Cloud Run deployment script

# `sh deploy.sh` and `zsh deploy.sh` ignore the shebang, so re-run under bash
if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🚀 Google Cloud Run Deployment${NC}"
echo "================================="

# Configuration
# WARNING: Do not commit API keys or sensitive data to version control
PROJECT_ID=${GOOGLE_CLOUD_PROJECT:-}
if [ -z "$PROJECT_ID" ]; then
    echo -e "${RED}❌ GOOGLE_CLOUD_PROJECT environment variable is required${NC}"
    echo "Please set it with: export GOOGLE_CLOUD_PROJECT=your-project-id"
    exit 1
fi
SERVICE_NAME="extract-html-scraper"
REGION=${GOOGLE_CLOUD_REGION:-"us-central1"}
IMAGE_NAME="gcr.io/$PROJECT_ID/$SERVICE_NAME"

# Tag the image with the commit, so the deploy names the image this build
# pushed and never falls back to an older :latest. The build uploads the
# working tree, so uncommitted changes get a timestamp suffix.
if GIT_SHA=$(git rev-parse --short=12 HEAD 2>/dev/null); then
    IMAGE_TAG=$GIT_SHA
    if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
        IMAGE_TAG="$IMAGE_TAG-dirty-$(date -u +%Y%m%d%H%M%S)"
    fi
else
    IMAGE_TAG="build-$(date -u +%Y%m%d%H%M%S)"
fi
IMAGE="$IMAGE_NAME:$IMAGE_TAG"

# Check if gcloud is installed
if ! command -v gcloud &> /dev/null; then
    echo -e "${RED}❌ gcloud CLI not found. Please install it first:${NC}"
    echo "https://cloud.google.com/sdk/docs/install"
    exit 1
fi

# Check if user is authenticated
ACTIVE_ACCOUNT=$(gcloud auth list --filter=status:ACTIVE --format="value(account)" 2>/dev/null || true)
if [ -z "$ACTIVE_ACCOUNT" ]; then
    echo -e "${RED}❌ Not authenticated with gcloud. Please run:${NC}"
    echo "gcloud auth login"
    exit 1
fi

# Set project
echo -e "${BLUE}Setting project to: $PROJECT_ID${NC}"
gcloud config set project "$PROJECT_ID"

# Enable required APIs
echo -e "${BLUE}Enabling required APIs...${NC}"
gcloud services enable cloudbuild.googleapis.com
gcloud services enable run.googleapis.com
gcloud services enable containerregistry.googleapis.com

# Build and push Docker image. A failed build stops the script here.
echo -e "${BLUE}Building Docker image $IMAGE...${NC}"
gcloud builds submit --config cloudbuild.yaml \
    --substitutions="_PROJECT_ID=$PROJECT_ID,_TAG=$IMAGE_TAG" .

# Deploy to Cloud Run
echo -e "${BLUE}Deploying to Cloud Run...${NC}"
gcloud run deploy "$SERVICE_NAME" \
    --image "$IMAGE" \
    --platform managed \
    --region "$REGION" \
    --allow-unauthenticated \
    --memory 2Gi \
    --cpu 2 \
    --timeout 300 \
    --concurrency 10 \
    --max-instances 100

# Get service URL
SERVICE_URL=$(gcloud run services describe "$SERVICE_NAME" --region="$REGION" --format="value(status.url)")

# Ensure public access (in case --allow-unauthenticated didn't set IAM correctly)
echo -e "${BLUE}Ensuring public access...${NC}"
gcloud run services add-iam-policy-binding "$SERVICE_NAME" \
    --region="$REGION" \
    --member="allUsers" \
    --role="roles/run.invoker" \
    --project="$PROJECT_ID" 2>/dev/null || echo "Note: Public access may be restricted by organization policy. Service may require authenticated access."

echo -e "${GREEN}✅ Cloud Run service deployed!${NC}"
echo -e "${GREEN}Image: $IMAGE${NC}"
echo -e "${GREEN}Service URL: $SERVICE_URL${NC}"
echo ""
echo -e "${BLUE}📝 Next steps:${NC}"
echo -e "${BLUE}1. Set API keys, if the service has none yet. Without keys it accepts every request.${NC}"
echo "   Via environment variable (other variables on the service are kept):"
echo "   gcloud run services update $SERVICE_NAME \\"
echo "     --update-env-vars=\"^@^SCRAPER_API_KEYS=your-key-1,your-key-2\" \\"
echo "     --region=$REGION"
echo ""
echo -e "${BLUE}   Or via Secret Manager (recommended for production):${NC}"
echo "   ./manage-api-keys.sh set-secret 'your-key-1,your-key-2'"
echo "   This stores the keys in the scraper-api-keys secret, grants the service"
echo "   account access, and has Cloud Run mount the secret as SCRAPER_API_KEYS."
echo ""
echo -e "${BLUE}2. Test the service:${NC}"
echo "curl \"$SERVICE_URL?url=https://example.com&key=YOUR_API_KEY\""
