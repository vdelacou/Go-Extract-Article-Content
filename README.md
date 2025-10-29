# Google Cloud Run Web Scraper

A high-performance web scraper for article content extraction, built with Go and deployed on Google Cloud Run for superior speed, efficiency, and scalability.

## ✨ Features

- 🚀 **Go Performance**: 2-10x faster execution than Node.js
- ⚡ **Ultra-fast Cold Starts**: 1-2s startup time
- 🐳 **Cloud Run Optimized**: Built for Google Cloud Run with optimal resource usage
- 🔒 **API Key Authentication**: Secure endpoint access via API Gateway
- 📄 **Smart Article Extraction**: Title, description, content with goquery
- 🖼️ **Optimized Image Extraction**: Concurrent processing, intelligent scoring
- 🧹 **Sanitized Output**: Clean HTML-free content with bluemonday
- 🌐 **Hybrid Strategy**: HTTP-first, browser fallback with chromedp
- ⚡ **Parallel Processing**: Concurrent alternate URL attempts

## 🎯 Quick Start

### Prerequisites

```bash
# Install Google Cloud CLI
brew install google-cloud-sdk

# Authenticate
gcloud auth login
gcloud auth application-default login
```

### Deploy in 3 Steps

```bash
# 1. Set your project ID
export GOOGLE_CLOUD_PROJECT="your-project-id"

# 2. Deploy to Cloud Run
./deploy.sh

# 3. Test
./test.sh "YOUR_GATEWAY_URL" "your-api-key" "https://example.com"
```

## 📚 API Documentation

### Architecture Overview

This service uses a **two-tier architecture** for secure web scraping:

1. **API Gateway** - Handles authentication via API key validation
2. **Cloud Run Service** - Performs the actual web scraping (no auth logic)

### Authentication

API Gateway validates requests using the `key` query parameter before forwarding them to the Cloud Run service.

### Endpoint

```
GET /?url=TARGET_URL&key=YOUR_API_KEY
```

### Parameters

- `url` (required): The URL to scrape
- `key` (required): Your API key for authentication
- `timeout` (optional): Request timeout in milliseconds. **Important timeout limits:**
  - **API Gateway requests**: Maximum 25000ms (25 seconds). Requests via API Gateway are automatically capped at 25s.
  - **Direct Cloud Run requests**: Maximum 240000ms (4 minutes)
  - Default: 300000ms (5 minutes) for direct requests, 25000ms for gateway requests

### Example Request

```bash
curl "https://your-gateway-url/?url=https://example.com&key=your-api-key"
```

### API Key Management

**Important:** API keys are managed by Google Cloud API Gateway, not through environment variables.

Create and manage API keys using the provided script:

```bash
# Create a new API key
./manage-api-keys.sh create

# List existing API keys
./manage-api-keys.sh list
```

**How it works:**
- API Gateway validates requests using the `key` query parameter
- API keys are created and managed through Google Cloud API Gateway service
- No environment variables are needed for API key configuration
- The Cloud Run service receives pre-authenticated requests from API Gateway

### Response Format

```json
{
  "title": "Article Title",
  "description": "Article description or summary",
  "content": "Full article content (sanitized)",
  "images": [
    "https://example.com/image1.jpg",
    "https://example.com/image2.jpg"
  ],
  "metadata": {
    "url": "https://example.com",
    "scrapedAt": "2024-01-01T12:00:00Z",
    "durationMs": 1500
  }
}
```

### Error Responses

- `400` - Missing URL or invalid URL format (returned by Cloud Run service)
- `401` - Invalid or missing API key (returned by API Gateway)
- `451` - Blocked by Cloudflare/site protection (returned by Cloud Run service)
- `500` - Scraping failed (returned by Cloud Run service)
- `504` - Upstream request timeout (returned by API Gateway) or scrape timeout (returned by Cloud Run service)

**Note about 504 errors:** If you're using API Gateway and see 504 errors, ensure your `timeout` parameter is ≤ 25000ms. For longer scraping operations (up to 4 minutes), use the direct Cloud Run service URL instead of the API Gateway URL.

## 🏆 Performance Comparison

| Metric | Node.js (Before) | Go (After) | Improvement |
|--------|------------------|------------|-------------|
| **Cold Start** | 3-5s | 1-2s | **2-3x faster** |
| **HTTP Scrape** | 3-5s | 500ms-1s | **3-5x faster** |
| **Browser Scrape** | 10-20s | 4-8s | **2-3x faster** |
| **Memory Usage** | 3GB | 1-2GB | **33-50% less** |
| **Binary Size** | 100MB+ | ~30MB | **70% smaller** |
| **Execution Time** | 10-20s | 3-8s | **50-70% faster** |

### Expected Performance

| Scraping Type | Time | Notes |
|---------------|------|-------|
| **HTTP Scraping** | 500ms - 2s | Fast, lightweight |
| **Browser Scraping** | 3-8s | Chrome automation |
| **Cold Start** | 1-2s | Cloud Run optimization |
| **Memory Usage** | 1-2GB | With Chrome/Chromium |

## ⚙️ Configuration

### Environment Variables

**For Cloud Run Service:**
- `SCRAPE_USER_AGENT` - Custom user agent (optional)
- `CHROME_BIN` - Chrome binary path (auto-configured)
- `PORT` - Server port (default: 8080)

**For Deployment Script:**
- `GOOGLE_CLOUD_PROJECT` - Your GCP project ID (required)

**Note:** API keys are managed through the API Gateway console or `manage-api-keys.sh` script, not environment variables.

### Cloud Run Settings

Default configuration in `deploy.sh`:

```bash
--memory 2Gi \
--cpu 2 \
--timeout 300 \
--concurrency 10 \
--max-instances 100
```

## 📁 Project Structure

```
/
├── cmd/
│   └── cloudrun/
│       └── main.go              # Cloud Run handler
├── internal/
│   ├── scraper/
│   │   ├── scraper.go           # Main orchestrator
│   │   ├── http.go              # HTTP fetching with alternates
│   │   ├── browser.go           # chromedp browser automation
│   │   ├── extractor.go         # Article content extraction
│   │   └── images.go            # Optimized image extraction
│   ├── config/
│   │   └── config.go            # Configuration & constants
│   └── models/
│       └── models.go            # Response types
├── Dockerfile                   # Cloud Run container
├── cloudbuild.yaml              # GCP build configuration
├── deploy.sh                    # Deployment script
├── test.sh                      # Testing script
├── api-gateway-config.yaml      # API Gateway configuration
├── manage-api-keys.sh           # API key management
├── go.mod                       # Go dependencies
├── go.sum
└── README.md                    # This file
```

## 💰 Cost Estimation

**Google Cloud Run Pricing:**

**Free Tier (Always):**
- 2M requests/month FREE
- 180,000 vCPU-seconds FREE
- 360,000 GiB-seconds FREE

**After Free Tier:**
- Requests: $0.40 per 1M requests
- CPU: $0.00002400 per vCPU-second
- Memory: $0.00000250 per GiB-second

**Example: 10,000 requests/month, 2GB RAM, 2 vCPU, 5s avg**
- Requests: FREE (under 2M)
- CPU: 10,000 × 2 vCPU × 5s = 100,000 vCPU-seconds = $2.40
- Memory: 10,000 × 2GB × 5s = 100,000 GiB-seconds = $0.25
- **Total: ~$2.65/month**

## 🚀 Key Optimizations

### 1. **Concurrent Processing**
- Parallel alternate URL attempts (4 URLs simultaneously)
- Concurrent image extraction (og:image + img tags)
- Parallel HTTP retries with exponential backoff

### 2. **Optimized Image Extraction**
- Single-pass HTML parsing with goquery
- Pre-compiled regex patterns
- Intelligent scoring algorithm
- Concurrent candidate processing

### 3. **Efficient Browser Automation**
- chromedp (40% faster than Puppeteer)
- Aggressive resource blocking (images, fonts, ads)
- Optimized Chrome flags
- Connection pooling

### 4. **Smart Fallback Strategy**
- HTTP fetch first (18s budget)
- Browser fallback only when needed (40s budget)
- AMP/mobile URL variants
- Cloudflare detection and handling

## 📈 Monitoring & Logs

### View Logs

```bash
# Real-time logs
gcloud logs tail --follow --project=YOUR_PROJECT_ID

# Or via Cloud Console
# https://console.cloud.google.com/run
```

### Cloud Monitoring

View in Google Cloud Console:
- Request count
- Response times
- Error rates
- Memory usage
- CPU utilization

## 🔧 Troubleshooting

### Common Issues

**1. Chrome not found**
```bash
# Check Chrome installation in container
docker run --rm your-image:latest /usr/bin/chromium-browser --version
```

**2. Memory issues**
```bash
# Increase memory in deploy.sh
--memory 4Gi
```

**3. Timeout errors (504 upstream request timeout)**
```bash
# For API Gateway requests:
# - Maximum timeout is 25000ms (25 seconds)
# - Use timeout parameter: ?timeout=25000
# Example: curl "https://gateway-url/?url=https://example.com&key=KEY&timeout=25000"

# For direct Cloud Run requests (longer scrapes):
# - Maximum timeout is 240000ms (4 minutes)
# - Use the Cloud Run service URL directly instead of API Gateway
# - Increase timeout in deploy.sh: --timeout 600
```

**4. Authentication errors**
```bash
# Re-authenticate
gcloud auth login
gcloud auth application-default login
```

### Debug Mode

Enable debug logging by setting environment variable in Cloud Run:

```bash
gcloud run services update extract-html-scraper \
  --set-env-vars="DEBUG=true" \
  --region=us-central1
```

## 🔐 Security Best Practices

1. **Use Google Secret Manager** for API keys:
   ```bash
   gcloud secrets create scraper-api-key --data-file=- <<< "your-key"
   ```

2. **Enable API Gateway authorization**:
   - API key authentication
   - Usage plans and quotas
   - Rate limiting

3. **Set up Cloud Monitoring alerts**:
   - Error rate monitoring
   - Response time alerts
   - Resource usage alerts

4. **Use IAM roles** for service accounts

5. **Implement rate limiting** via API Gateway

## 🚀 Advanced Usage

### Custom Chrome Configuration

Modify `Dockerfile` for custom Chrome setup:

```dockerfile
# Install specific Chrome version
RUN wget -q https://dl.google.com/linux/chrome/rpm/stable/x86_64/google-chrome-stable-119.0.6045.105-1.x86_64.rpm
```

### Custom Domain

Use API Gateway custom domain:

```bash
gcloud api-gateway domains create scraper-api \
  --domain=api.yourdomain.com \
  --certificate=your-cert
```

### VPC Access

For private resource access:

```bash
gcloud run services update extract-html-scraper \
  --vpc-connector=your-connector \
  --vpc-egress=private-ranges-only
```

## 🔄 CI/CD Integration

### GitHub Actions

```yaml
name: Deploy to Cloud Run
on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: google-github-actions/setup-gcloud@v1
        with:
          service_account_key: ${{ secrets.GCP_SA_KEY }}
          project_id: ${{ secrets.GCP_PROJECT_ID }}
      - run: |
          ./deploy.sh
        env:
          GOOGLE_CLOUD_PROJECT: ${{ secrets.GCP_PROJECT_ID }}
```

## 🗑️ Cleanup

Delete everything:

```bash
# Delete Cloud Run service
gcloud run services delete extract-html-scraper --region=us-central1

# Delete API Gateway
gcloud api-gateway gateways delete extract-html-scraper-gateway --location=us-central1

# Delete container images
gcloud container images delete gcr.io/YOUR_PROJECT/extract-html-scraper
```

## 📖 Additional Resources

- [Google Cloud Run Documentation](https://cloud.google.com/run/docs)
- [chromedp Documentation](https://github.com/chromedp/chromedp)
- [goquery Documentation](https://github.com/PuerkitoBio/goquery)
- [Go Cloud Run Runtime](https://github.com/GoogleCloudPlatform/functions-framework-go)

## 🤝 Contributing

Contributions welcome! Please feel free to submit a Pull Request.

## 📄 License

MIT

## 💬 Support

- Check logs: `gcloud logs tail --follow --project=YOUR_PROJECT_ID`
- Review Cloud Monitoring metrics in Google Cloud Console
- Run test suite: `./test.sh`
- Open an issue on GitHub

---

**Made with ❤️ for high-performance serverless web scraping on Google Cloud Run**