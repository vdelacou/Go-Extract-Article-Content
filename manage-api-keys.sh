#!/bin/bash

# WARNING: Do not commit API keys or sensitive data to version control
PROJECT_ID=${GOOGLE_CLOUD_PROJECT}
if [ -z "$PROJECT_ID" ]; then
    echo "❌ GOOGLE_CLOUD_PROJECT environment variable is required"
    echo "Please set it with: export GOOGLE_CLOUD_PROJECT=your-project-id"
    exit 1
fi

SERVICE_NAME="extract-html-scraper"
REGION=${GOOGLE_CLOUD_REGION:-"us-central1"}
SECRET_NAME="scraper-api-keys"

case "$1" in
  set-env)
    if [ -z "$2" ]; then
        echo "❌ Usage: $0 set-env <key1,key2,key3>"
        echo "Example: $0 set-env 'abc123,def456'"
        exit 1
    fi
    echo "Setting API keys via environment variable..."
    gcloud run services update $SERVICE_NAME \
        --set-env-vars="SCRAPER_API_KEYS=$2" \
        --region=$REGION \
        --project=$PROJECT_ID
    echo "✅ API keys updated via environment variable"
    ;;
  set-secret)
    if [ -z "$2" ]; then
        echo "❌ Usage: $0 set-secret <key1,key2,key3>"
        echo "Example: $0 set-secret 'abc123,def456'"
        exit 1
    fi
    echo "Setting API keys via Secret Manager..."
    
    # Check if secret exists
    if ! gcloud secrets describe $SECRET_NAME --project=$PROJECT_ID &>/dev/null; then
        echo "Creating new secret: $SECRET_NAME"
        echo -n "$2" | gcloud secrets create $SECRET_NAME \
            --data-file=- \
            --project=$PROJECT_ID
    else
        echo "Updating existing secret: $SECRET_NAME"
        echo -n "$2" | gcloud secrets versions add $SECRET_NAME \
            --data-file=- \
            --project=$PROJECT_ID
    fi
    
    # Grant Cloud Run service access to secret
    echo "Granting Cloud Run service access to secret..."
    # Get the service account used by Cloud Run (default is Compute Engine default SA)
    SERVICE_ACCOUNT=$(gcloud run services describe $SERVICE_NAME \
        --region=$REGION \
        --format='value(spec.template.spec.serviceAccountName)' \
        --project=$PROJECT_ID 2>/dev/null)
    
    if [ -z "$SERVICE_ACCOUNT" ]; then
        # Use Compute Engine default service account
        SERVICE_ACCOUNT="${PROJECT_ID}@appspot.gserviceaccount.com"
    fi
    
    gcloud secrets add-iam-policy-binding $SECRET_NAME \
        --member="serviceAccount:${SERVICE_ACCOUNT}" \
        --role="roles/secretmanager.secretAccessor" \
        --project=$PROJECT_ID 2>/dev/null || echo "Note: Service account may need manual secret access configuration"
    
    # Update Cloud Run service to use secret
    gcloud run services update $SERVICE_NAME \
        --set-secrets="SCRAPER_API_KEY_SECRET=${SECRET_NAME}:latest" \
        --region=$REGION \
        --project=$PROJECT_ID
    
    echo "✅ API keys updated via Secret Manager"
    ;;
  list)
    echo "Current API key configuration:"
    echo ""
    echo "Environment variable:"
    gcloud run services describe $SERVICE_NAME \
        --region=$REGION \
        --format="value(spec.template.spec.containers[0].env)" \
        --project=$PROJECT_ID | grep SCRAPER_API || echo "  Not set"
    echo ""
    echo "Secret Manager:"
    if gcloud secrets describe $SECRET_NAME --project=$PROJECT_ID &>/dev/null; then
        echo "  Secret exists: $SECRET_NAME"
        echo "  Latest version: $(gcloud secrets versions list $SECRET_NAME --project=$PROJECT_ID --limit=1 --format='value(name)' 2>/dev/null || echo 'N/A')"
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
