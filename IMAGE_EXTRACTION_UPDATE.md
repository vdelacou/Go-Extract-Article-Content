# Image Extraction Update - Article Images Only

## Summary

Modified image extraction to **only return**:
1. **OG tag image** (og:image from meta tags)
2. **Images within article content** (from `<article>`, `<main>`, or other content containers)

**Excludes**: Navigation images, sidebar images, ads, footer images, header logos, etc.

## Changes Made

### 1. New Function: `extractArticleImgTags()`

**Location**: `internal/scraper/images.go:156-179`

**What it does**:
- Only searches for images within article content containers
- Uses the enhanced content selectors: `[data-module='ArticleBody']`, `article`, `main`, etc.
- Marks all found images as `InArticle = true`

**Before** (extracted all images):
```go
doc.Find("img").Each(...) // Finds ALL images on page
```

**After** (article images only):
```go
doc.Find(ContentSelectors).Each(func(container) {
    container.Find("img").Each(...) // Only images inside article
})
```

### 2. New Function: `filterArticleImages()`

**Location**: `internal/scraper/images.go:391-413`

**Strict filtering logic**:
```go
// ONLY keep images that are:
if c.Source != "og" && !c.InArticle {
    continue // Skip this image
}
```

This means:
- ✅ OG tag image always included
- ✅ Images within article containers included
- ❌ Images outside article containers excluded

### 3. New Function: `passesBasicFilters()`

**Location**: `internal/scraper/images.go:433-466`

**More lenient filtering for article images**:

| Filter | Old Behavior | New Behavior |
|--------|-------------|--------------|
| **OG images** | Apply all filters | Always pass (publisher curated) |
| **Minimum size** | 300px short side | 100px (only filter tiny icons) |
| **Aspect ratio** | Strict ratios | No restriction |
| **Area** | 140,000px² minimum | No minimum |
| **Bad hints** | Filter if detected | Only filter if very small |

### 4. Increased Image Limit

**Location**: `internal/scraper/images.go:85`

```go
return ie.getTopImages(filtered, 5) // Was 3, now 5
```

Allows returning up to 5 article images instead of 3.

## Example: SCMP Article

### Before Changes

**Returned images**:
1. SCMP logo (header)
2. Ad banner
3. Sidebar thumbnail
4. OG image (maybe)
5. Article image (if scored high enough)

### After Changes

**Returned images**:
1. OG image (og:image meta tag)
2. Main article image
3. Additional article images (if any)
4. Charts/diagrams in article (if any)

**NOT included**:
- ❌ Header/footer images
- ❌ Navigation icons
- ❌ Ad banners
- ❌ Sidebar images
- ❌ Author avatars (unless in article)

## Testing

### Deploy

```bash
./deploy.sh
```

### Test SCMP Article

```bash
SERVICE_URL="YOUR_SERVICE_URL"
API_KEY="YOUR_KEY"

# Extract images from SCMP article
curl "$SERVICE_URL?url=https://www.scmp.com/business/companies/article/3331382/satellite-maker-uspace-pivots-ai-applications-new-tech-centre-shenzhen&key=$API_KEY" | jq '.images'
```

### Expected Output

```json
{
  "images": [
    "https://cdn.i-scmp.com/sites/default/files/styles/og_image_scmp_generic/public/d8/images/canvas/2025/11/03/some-image.jpg",
    "https://cdn.i-scmp.com/sites/default/files/d8/images/2025/11/03/article-image-1.jpg"
  ]
}
```

## Scoring Priority

Images are sorted by score:

1. **OG image**: Base score + 1.0 + automatic pass through filters
2. **Article images**: Base score + 2.0 (if InArticle)
3. **Larger images**: Get higher scores (logarithmic area bonus)
4. **Good aspect ratios**: +1.0 bonus (1.33, 1.5, 1.6, 1.67, 1.78, etc.)

**Result**: OG image usually comes first (if present), followed by article images sorted by size/quality.

## Benefits

### ✅ Cleaner Results
- No more navigation/sidebar/ad images
- Only images relevant to the article content

### ✅ More Reliable
- Less affected by page layout changes
- Focused on semantic content areas

### ✅ Better Performance
- Searches smaller DOM subset
- Faster image extraction

### ✅ More Images
- Limit increased from 3 to 5
- Better for articles with multiple images/charts

## Backward Compatibility

The changes are **fully backward compatible**:

- ✅ Old extraction functions still exist (marked as legacy)
- ✅ Same API response format
- ✅ No breaking changes to existing code
- ✅ If article containers not found, falls back gracefully

## Content Selectors Used

Images are extracted from elements matching:

```
[data-module='ArticleBody']  ← SCMP-specific
[data-qa='article-body']      ← Common QA selector
.article__body                ← BEM pattern
.story__content-body          ← News sites
article                       ← HTML5 semantic
main                          ← HTML5 semantic
[role='main']                 ← ARIA role
.content                      ← Generic
.post-content                 ← Blogs
.entry-content                ← WordPress
.article-content              ← News sites
.story-content                ← News sites
```

## Advanced: Customizing Filters

### Make Filters Even Stricter

Edit `passesBasicFilters()` in `images.go:435`:

```go
// Only allow images > 200px
if shortSide < 200 { // Was 100
    return false
}
```

### Return Only OG Image

Edit `ExtractImagesFromHTML()` in `images.go:34`:

```go
// Comment out article image extraction
// wg.Add(1)
// go func() {
//     defer wg.Done()
//     imgCandidates := ie.extractArticleImgTags(doc, baseURL)
//     candidatesChan <- imgCandidates
// }()
```

### Change Image Limit

Edit `images.go:85`:

```go
return ie.getTopImages(filtered, 10) // Return up to 10 images
```

## Troubleshooting

### No Images Returned

**Possible causes**:
1. Page has no OG tag
2. Article container not detected
3. Images too small (< 100px)
4. Images are ad sizes

**Debug**:
```bash
# Check logs for extraction details
gcloud logs tail --follow --filter='resource.labels.service_name="extract-html-scraper"' | grep -i image
```

### Wrong Images Returned

**If getting non-article images**:
1. Check if site uses non-standard article containers
2. Add site-specific selector to `ContentSelectors` in `constants.go:15`

**If missing article images**:
1. Images might be < 100px (too small)
2. Images might have "bad hints" (icon, logo, ad in class/URL)
3. Check if images are in `<figure>` or other containers outside article tag

### OG Image Not Returned

**Possible causes**:
1. Site doesn't have og:image meta tag
2. OG image URL is invalid or not a real image
3. OG image URL doesn't end in .jpg/.png/.webp/etc.

**Fix**:
Check the page source for og:image meta tag:
```bash
curl -s "https://www.scmp.com/..." | grep -i "og:image"
```

## Summary

**Before**:
- Extracted all images from entire page
- Scored and returned top 3
- Often included ads, navigation, sidebars

**After**:
- Only extracts OG tag + article images
- Returns up to 5 images
- Clean, article-focused results

This change gives you **exactly what you asked for**: OG tag image and images from within the article content only! 🎯
