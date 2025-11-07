# Image Alt Text Addition

## Summary

Images now return **both URL and alt text** in the API response, providing better accessibility information and context for each image.

## Changes Made

### 1. Updated Response Model

**File**: `internal/models/models.go`

**New `Image` struct**:
```go
type Image struct {
    URL string `json:"url"`
    Alt string `json:"alt,omitempty"`
}
```

**Updated `ScrapeResponse`**:
```go
type ScrapeResponse struct {
    // ... other fields
    Images []Image `json:"images"`  // Was: []string
}
```

### 2. Updated Image Extraction

**Files Modified**:
- `internal/scraper/images.go` - Extract and store alt text
- `internal/scraper/extractor.go` - Return Image objects

**Alt Text Sources**:
1. **HTML img tags**: `<img alt="..." />` attribute
2. **OG tags**: `<meta property="og:image:alt" />` if available
3. **JSON-LD**: Uses article headline as fallback

## Response Format

### Before
```json
{
  "images": [
    "https://example.com/image1.jpg",
    "https://example.com/image2.jpg"
  ]
}
```

### After
```json
{
  "images": [
    {
      "url": "https://example.com/image1.jpg",
      "alt": "Satellite maker Uspace's new tech centre"
    },
    {
      "url": "https://example.com/image2.jpg",
      "alt": "World's first remote robotic surgery"
    }
  ]
}
```

## Alt Text Extraction Logic

### From HTML `<img>` Tags
```html
<img src="image.jpg" alt="Article image description" />
```
↓
```json
{
  "url": "https://example.com/image.jpg",
  "alt": "Article image description"
}
```

### From OG Meta Tags
```html
<meta property="og:image" content="https://example.com/og.jpg" />
<meta property="og:image:alt" content="Social media preview image" />
```
↓
```json
{
  "url": "https://example.com/og.jpg",
  "alt": "Social media preview image"
}
```

### From JSON-LD (Fallback)
When JSON-LD image has no alt, uses article headline:
```json
{
  "@type": "NewsArticle",
  "headline": "Satellite maker pivots to AI",
  "image": "https://example.com/featured.jpg"
}
```
↓
```json
{
  "url": "https://example.com/featured.jpg",
  "alt": "Satellite maker pivots to AI"
}
```

### Missing Alt Text
If no alt text is available:
```json
{
  "url": "https://example.com/image.jpg",
  "alt": ""
}
```

The `alt` field is omitted from JSON if empty (using `omitempty` tag).

## Benefits

### 1. Accessibility
- Provides descriptive text for screen readers
- Better support for visually impaired users
- Follows web accessibility best practices (WCAG)

### 2. Context
- Helps understand what each image depicts
- Useful for AI/ML applications processing the content
- Better for content analysis and summarization

### 3. SEO
- Alt text is valuable for search engine optimization
- Provides semantic information about images
- Matches what publishers intended for accessibility

## Example Responses

### SCMP Article
```json
{
  "images": [
    {
      "url": "https://cdn.i-scmp.com/sites/default/files/og-image.jpg",
      "alt": "Satellite maker Uspace pivots to AI applications at new tech centre in Shenzhen"
    },
    {
      "url": "https://img.i-scmp.com/cdn-cgi/image/article-img.jpg",
      "alt": "Uspace technology center interior view"
    }
  ]
}
```

### pandaily Article
```json
{
  "images": [
    {
      "url": "https://cms-image.pandaily.com/AA_1_P_Ka_WM_c3c9c640a1.png",
      "alt": "World's First Remote Robotic Subretinal Injection Performed in China"
    }
  ]
}
```

## Backward Compatibility

⚠️ **Breaking Change**: This is a **breaking change** for API consumers.

**Before**:
```javascript
// Old code accessing images
response.images[0]  // "https://example.com/image.jpg"
```

**After**:
```javascript
// New code accessing images
response.images[0].url  // "https://example.com/image.jpg"
response.images[0].alt  // "Image description"
```

### Migration Guide for API Users

**Option 1: Update to use object**
```javascript
// Before
const imageUrl = response.images[0];

// After
const imageUrl = response.images[0].url;
const imageAlt = response.images[0].alt;
```

**Option 2: Extract URLs only**
```javascript
const imageUrls = response.images.map(img => img.url);
// ["https://...", "https://..."]
```

**Option 3: Use both**
```javascript
response.images.forEach(img => {
    console.log(`Image: ${img.url}`);
    console.log(`Alt: ${img.alt || 'No description'}`);
});
```

## Testing

### Deploy
```bash
./deploy.sh
```

### Test Response
```bash
SERVICE_URL="YOUR_SERVICE_URL"
API_KEY="YOUR_KEY"

# Test SCMP
curl "$SERVICE_URL?url=https://www.scmp.com/business/companies/article/3331382/satellite-maker-uspace-pivots-ai-applications-new-tech-centre-shenzhen&key=$API_KEY" | jq '.images'

# Expected output:
# [
#   {
#     "url": "https://cdn.i-scmp.com/sites/default/files/...",
#     "alt": "Satellite maker Uspace..."
#   },
#   ...
# ]
```

### Test pandaily
```bash
curl "$SERVICE_URL?url=https://pandaily.com/world-s-first-remote-robotic-subretinal-injection-performed-in-china&key=$API_KEY" | jq '.images'
```

## Implementation Details

### Alt Text Priority

For each image source:

**OG Images**:
1. Try `og:image:alt` meta tag
2. If not found, alt will be empty

**JSON-LD Images**:
1. Try to use article `headline` as alt
2. If headline not found, alt will be empty

**HTML Images**:
1. Use `alt` attribute from `<img>` tag
2. Trim whitespace
3. If not present, alt will be empty

### Code Locations

**Extract alt from img tag**: `images.go:283-284`
```go
alt, _ := s.Attr("alt")
alt = strings.TrimSpace(alt)
```

**Extract alt from OG tags**: `images.go:146-149`
```go
case "og:image:alt":
    if content, exists := s.Attr("content"); exists {
        ogImageAlt = content
    }
```

**Use headline as alt**: `images.go:119-122`
```go
if headline, _, _, found := ExtractJSONLD(doc); found && headline != "" {
    alt = headline
}
```

**Return Image objects**: `images.go:765-768`
```go
result = append(result, models.Image{
    URL: c.URL,
    Alt: c.Alt,
})
```

## Quality Considerations

### Good Alt Text
- ✅ Descriptive: "Uspace technology center interior view"
- ✅ Concise: "Remote robotic surgery in progress"
- ✅ Contextual: "World's first subretinal injection robot"

### Poor Alt Text (from source)
- ⚠️ Generic: "image", "photo", "picture"
- ⚠️ Empty: No alt attribute on image
- ⚠️ Filename: "IMG_1234.jpg"

We return alt text **as-is** from the source - we don't clean or modify it.

## Future Enhancements

Potential improvements:

1. **Alt text quality scoring**: Flag low-quality alt text
2. **Alt text generation**: Use AI to generate alt text if missing
3. **Alt text translation**: Translate alt text to match response language
4. **Alt text validation**: Check if alt text matches image content

## Summary

This update provides **richer image data** by including alt text alongside URLs. While it's a breaking change for API consumers, it significantly improves:

- ✅ Accessibility support
- ✅ Content understanding
- ✅ SEO value
- ✅ AI/ML integration potential

API consumers will need to update their code to access `response.images[i].url` instead of `response.images[i]`.
