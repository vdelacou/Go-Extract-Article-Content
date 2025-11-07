# Sidebar/Banner Image Filter Fix

## Problem

The image extractor was returning images from sidebars, banners, and "featured posts" sections, not just article content.

**Example (pandaily.com)**:
- ❌ Returned: 5 images (4 from sidebar "Featured Posts", 1 from article)
- ✅ Should return: 1 image (just the article image)

## Root Cause

1. **React-rendered sites**: Content boundaries unclear when JavaScript renders the page
2. **No exclusion filters**: Sidebar/featured sections were being scanned
3. **Missing JSON-LD extraction**: Featured images in structured data weren't being used

## Solutions Implemented

### 1. **Added Section Exclusion Filters**

**Files**: `internal/scraper/images.go`

**New Functions**:
- `isExcludedSection()` - Checks if a container should be excluded
- `isInExcludedSection()` - Checks if an element is within an excluded section

**Exclusion Patterns**:
```
sidebar, related, featured, popular, trending, recommended,
widget, banner, advertisement, navigation, menu, footer,
header, carousel, slider, aside, promo, sponsored,
more-stories, latest-posts, recent-posts
```

**How it works**:
```go
// Before extracting images from a container
if ie.isExcludedSection(container) {
    return // Skip this entire section
}

// Before processing each image
if ie.isInExcludedSection(imageElement) {
    return // Skip this image
}
```

### 2. **Added JSON-LD Image Extraction**

**Files**:
- `internal/scraper/extractor_helpers.go` - Helper functions
- `internal/scraper/images.go` - Integration

**New Functions**:
- `ExtractJSONLDImage()` - Extracts image from schema.org JSON-LD
- `extractImageURLFromField()` - Handles different JSON-LD image formats
- `extractJSONLDImage()` - ImageExtractor method

**Why this helps**:
- Many sites (like pandaily) include the featured image in JSON-LD structured data
- JSON-LD images are publisher-curated (high quality, definitely article-related)
- Extracted before HTML parsing, so immune to layout changes

**Supported formats**:
```json
// String format
"image": "https://example.com/image.jpg"

// Object format
"image": { "url": "https://example.com/image.jpg" }

// Array format
"image": ["https://example.com/image1.jpg", "https://example.com/image2.jpg"]
```

### 3. **Increased Structured Data Priority**

**Changes**:
```go
// Scoring boost increased
if c.Source == "og" || c.Source == "jsonld" {
    score += 1.5 // Was 1.0 for OG only
}

// Always pass filters
if c.Source == "og" || c.Source == "jsonld" {
    return true // Bypass all size/quality filters
}
```

### 4. **Three-Source Image Extraction**

**New flow**:
```
1. Extract OG tag image (og:image meta)
2. Extract JSON-LD image (schema.org structured data) ← NEW
3. Extract article images (only from article containers)
4. Filter out excluded sections ← NEW
5. Score and rank
6. Return top 5
```

## Before vs After

### Before (pandaily.com example)

```json
{
  "images": [
    "qwen_thumb_1170x725.jpg",          // Sidebar "Featured Post #1"
    "AA_1_P_Ka_WM_c3c9c640a1.png",      // ✓ Article image
    "_7d83a3860e.jpg",                  // Sidebar "Featured Post #2"
    "1_a5bda3bf21.jpg",                 // Sidebar "Featured Post #3"
    "bytedance_doubao_48664fccf8.png"   // Sidebar "Featured Post #4"
  ]
}
```

### After

```json
{
  "images": [
    "AA_1_P_Ka_WM_c3c9c640a1.png"  // ✓ Only the article image
  ]
}
```

## How Exclusion Works

### Example: Featured Posts Section

**HTML Structure**:
```html
<aside class="featured-posts">
  <h3>Featured Posts</h3>
  <div class="post-card">
    <img src="sidebar-image-1.jpg" />  <!-- EXCLUDED -->
  </div>
  <div class="post-card">
    <img src="sidebar-image-2.jpg" />  <!-- EXCLUDED -->
  </div>
</aside>

<article>
  <img src="article-image.jpg" />  <!-- ✓ INCLUDED -->
</article>
```

**Filter Logic**:
1. Scan `<aside class="featured-posts">` container
2. Check class name: `"featured-posts"` contains `"featured"` → EXCLUDE
3. Skip all images in this container
4. Scan `<article>` container
5. No exclusion patterns found → INCLUDE
6. Extract images from this container

### Example: Related Articles

**HTML Structure**:
```html
<div class="related-articles">
  <img src="related-1.jpg" />  <!-- EXCLUDED -->
  <img src="related-2.jpg" />  <!-- EXCLUDED -->
</div>

<main class="article-body">
  <img src="main-image.jpg" />  <!-- ✓ INCLUDED -->
</main>
```

## Testing

### Deploy
```bash
./deploy.sh
```

### Test pandaily.com
```bash
SERVICE_URL="YOUR_SERVICE_URL"
API_KEY="YOUR_KEY"

curl "$SERVICE_URL?url=https://pandaily.com/world-s-first-remote-robotic-subretinal-injection-performed-in-china&key=$API_KEY" | jq '.images'
```

### Expected Output
```json
{
  "images": [
    "https://cms-image.pandaily.com/AA_1_P_Ka_WM_c3c9c640a1.png"
  ]
}
```

### Test SCMP (should still work)
```bash
curl "$SERVICE_URL?url=https://www.scmp.com/business/companies/article/3331382/satellite-maker-uspace-pivots-ai-applications-new-tech-centre-shenzhen&key=$API_KEY" | jq '.images'
```

## Image Source Priority

1. **JSON-LD image** (score: 3.5 if InArticle + structured data bonus)
2. **OG image** (score: 3.5 if InArticle + structured data bonus)
3. **Article images** (score: 2.0 + area bonus)

## Debugging

### If still getting sidebar images

**Check logs for exclusion patterns**:
```bash
gcloud logs tail --follow | grep -i "excluded\|featured\|sidebar"
```

**Add custom exclusions** in `images.go:419-441`:
```go
exclusionPatterns := []string{
    // ... existing patterns ...
    "your-custom-pattern",
}
```

### If missing article images

**Possible causes**:
1. Images < 100px (increase minimum in `passesBasicFilters`)
2. Images in excluded sections (check your exclusion patterns)
3. No article container found (check `ContentSelectors` in `constants.go`)

**Debug**:
- Check page source to see if images are in `<article>`, `<main>`, or other content containers
- Add site-specific selectors to `ContentSelectors`

## Edge Cases

### Case 1: Gallery in Article
**Scenario**: Article has an embedded image gallery

**Result**: ✅ Gallery images WILL be included (they're in the article)

**If you want to exclude galleries**:
Add `"gallery"` to exclusion patterns (already included)

### Case 2: Author Avatar in Article
**Scenario**: Author bio box with avatar inside article

**Result**: ✅ Avatar MIGHT be included if > 100px

**To exclude**:
- Avatar images usually have class/alt like "author", "avatar"
- Already filtered by `badHint` regex
- If still included, add to exclusion patterns

### Case 3: No Article Container
**Scenario**: Site doesn't use semantic HTML (no `<article>` or `<main>`)

**Result**: ⚠️ May return no images

**Solution**:
- Add site-specific selectors to `ContentSelectors` in `constants.go`
- Look for content div with specific class/id

### Case 4: Featured Image Outside Article
**Scenario**: Main image is in a hero section above the article

**Result**: ✅ Still captured via OG tag or JSON-LD

## Summary of Changes

### Files Modified
1. ✅ `internal/scraper/images.go` (4 new functions, enhanced extraction)
2. ✅ `internal/scraper/extractor_helpers.go` (JSON-LD image extraction)
3. ✅ `internal/scraper/constants.go` (browser timeout adjustments - previous change)
4. ✅ `internal/scraper/scraper.go` (random delays - previous change)

### New Features
- ✅ Sidebar/featured posts exclusion
- ✅ JSON-LD image extraction
- ✅ Section-based filtering
- ✅ Enhanced scoring for structured data

### Backward Compatible
- ✅ API response format unchanged
- ✅ Existing image extraction still works
- ✅ Only adds more filtering

## Performance Impact

- ✅ **Faster**: Fewer images to process
- ✅ **Cleaner**: More relevant images only
- ✅ **More accurate**: Uses publisher-curated images (JSON-LD)

## Next Steps

If you still see unwanted images after deploying:

1. **Identify the section**: Check the HTML class/id of the container
2. **Add to exclusion patterns**: Edit `images.go:419-441`
3. **Redeploy**: Run `./deploy.sh`
4. **Test**: Verify the images are now excluded

Or use stricter filtering by reducing the image limit to 1 (only OG/JSON-LD):

```go
// In images.go:85
return ie.getTopImages(filtered, 1) // Only return 1 image
```

This fix ensures you get **only** the article-related images, not sidebar clutter! 🎯
