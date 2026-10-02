#!/bin/bash

# `sh manage-api-keys.sh` and `zsh manage-api-keys.sh` ignore the shebang, so re-run under bash
if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi

set -euo pipefail

# WARNING: Do not commit API keys or sensitive data to version control
PROJECT_ID=${GOOGLE_CLOUD_PROJECT:-}
if [ -z "$PROJECT_ID" ]; then
    echo "❌ GOOGLE_CLOUD_PROJECT environment variable is required"
    echo "Please set it with: export GOOGLE_CLOUD_PROJECT=your-project-id"
    exit 1
fi

SERVICE_NAME="extract-html-scraper"
REGION=${GOOGLE_CLOUD_REGION:-"us-central1"}
SECRET_NAME="scraper-api-keys"
KEYS=${2:-}

case "${1:-}" in
  set-env)
    if [ -z "$KEYS" ]; then
        echo "❌ Usage: $0 set-env <key1,key2,key3>"
        echo "Example: $0 set-env 'abc123,def456'"
        exit 1
    fi
    if [[ "$KEYS" == *@* ]]; then
        echo "❌ Keys must not contain '@' (used as the gcloud list delimiter)"
        exit 1
    fi
    echo "Setting API keys via environment variable..."
    # --update-env-vars keeps the service's other variables (--set-env-vars
    # would drop them). gcloud splits the list on commas; ^@^ makes @ the
    # pair delimiter so the comma-separated key list stays in a single value
    gcloud run services update "$SERVICE_NAME" \
        --update-env-vars="^@^SCRAPER_API_KEYS=$KEYS" \
        --region="$REGION" \
        --project="$PROJECT_ID"
    echo "✅ API keys updated via environment variable"
    ;;
  set-secret)
    if [ -z "$KEYS" ]; then
        echo "❌ Usage: $0 set-secret <key1,key2,key3>"
        echo "Example: $0 set-secret 'abc123,def456'"
        exit 1
    fi
    echo "Setting API keys via Secret Manager..."
    
    # Check if secret exists
    if ! gcloud secrets describe "$SECRET_NAME" --project="$PROJECT_ID" &>/dev/null; then
        echo "Creating new secret: $SECRET_NAME"
        echo -n "$KEYS" | gcloud secrets create "$SECRET_NAME" \
            --data-file=- \
            --project="$PROJECT_ID"
    else
        echo "Updating existing secret: $SECRET_NAME"
        echo -n "$KEYS" | gcloud secrets versions add "$SECRET_NAME" \
            --data-file=- \
            --project="$PROJECT_ID"
    fi
    
    # Grant Cloud Run service access to secret
    echo "Granting Cloud Run service access to secret..."
    # Get the service account used by Cloud Run
    SERVICE_ACCOUNT=$(gcloud run services describe "$SERVICE_NAME" \
        --region="$REGION" \
        --format='value(spec.template.spec.serviceAccountName)' \
        --project="$PROJECT_ID" 2>/dev/null || true)
    
    if [ -z "$SERVICE_ACCOUNT" ]; then
        # Cloud Run's default is the Compute Engine default service account
        PROJECT_NUMBER=$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')
        SERVICE_ACCOUNT="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"
    fi
    
    gcloud secrets add-iam-policy-binding "$SECRET_NAME" \
        --member="serviceAccount:${SERVICE_ACCOUNT}" \
        --role="roles/secretmanager.secretAccessor" \
        --project="$PROJECT_ID" 2>/dev/null || echo "Note: Service account may need manual secret access configuration"
    
    # Have Cloud Run mount the secret as SCRAPER_API_KEYS, the variable the
    # service reads. A plain SCRAPER_API_KEYS has to be removed in the same
    # update (gcloud applies env var removals before secret updates), and
    # SCRAPER_API_KEY_SECRET, which older versions of this script set, is
    # ignored by the service
    gcloud run services update "$SERVICE_NAME" \
        --remove-env-vars=SCRAPER_API_KEYS \
        --remove-secrets=SCRAPER_API_KEY_SECRET \
        --update-secrets="SCRAPER_API_KEYS=${SECRET_NAME}:latest" \
        --region="$REGION" \
        --project="$PROJECT_ID"
    
    echo "✅ API keys updated via Secret Manager"
    ;;
  list)
    echo "Current API key configuration:"
    echo ""
    echo "Environment variable:"
    gcloud run services describe "$SERVICE_NAME" \
        --region="$REGION" \
        --format="value(spec.template.spec.containers[0].env)" \
        --project="$PROJECT_ID" | grep SCRAPER_API || echo "  Not set"
    echo ""
    echo "Secret Manager:"
    if gcloud secrets describe "$SECRET_NAME" --project="$PROJECT_ID" &>/dev/null; then
        echo "  Secret exists: $SECRET_NAME"
        echo "  Latest version: $(gcloud secrets versions list "$SECRET_NAME" --project="$PROJECT_ID" --limit=1 --format='value(name)' 2>/dev/null || echo 'N/A')"
    else
        echo "  Secret does not exist"
    fi
    ;;
  *)
    echo "Usage: $0 {set-env|set-secret|list}"
    echo ""
    echo "Commands:"
    echo "  set-env <keys>     Set API keys via environment variable (simple, for dev/test)"
    echo "  set-secret <keys>   Set API keys via Secret Manager (recommended for production)"
    echo "  list               Show current API key configuration"
    echo ""
    echo "Examples:"
    echo "  $0 set-env 'key1,key2,key3'"
    echo "  $0 set-secret 'key1,key2,key3'"
    echo "  $0 list"
    exit 1
    ;;
esac
