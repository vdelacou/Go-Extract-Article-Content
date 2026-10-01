package scraper

// Some Remix / React Router apps (pandaily.com) render an empty <body> on the
// server and ship the article only in the "single fetch" hydration payload,
// a turbo-stream embedded as
//   window.__remixContext.streamController.enqueue("<json string>")

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var remixEnqueueRe = regexp.MustCompile(`window\.__(?:remix|reactRouter)Context\.streamController\.enqueue\(("(?:[^"\\]|\\.)*")\)`)

// extractTurboStreamText joins every enqueue(...) chunk: they are pieces of one
// text stream, so they must be joined before splitting into lines
func extractTurboStreamText(page string) string {
	var sb strings.Builder
	for _, m := range remixEnqueueRe.FindAllStringSubmatch(page, -1) {
		var chunk string
		if err := json.Unmarshal([]byte(m[1]), &chunk); err == nil {
			sb.WriteString(chunk)
		}
	}
	return sb.String()
}

// decodeTurboStream decodes the root value (first line) of a turbo-stream payload.
// The line is a flat array: objects are {"_<keyIndex>": valueIndex}, arrays are
// lists of value indexes, negative integers are sentinels (null, undefined, NaN)
// and arrays starting with a one-letter string are typed values ("D" date, "U"
// URL, "P" promise). Deferred promise lines are ignored.
func decodeTurboStream(text string) (interface{}, error) {
	first := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		first = text[:i]
	}
	var arr []interface{}
	dec := json.NewDecoder(strings.NewReader(first))
	dec.UseNumber()
	if err := dec.Decode(&arr); err != nil {
		return nil, fmt.Errorf("turbo-stream: %w", err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("turbo-stream: empty")
	}

	memo := make(map[int]interface{}, len(arr))
	var hydrate func(i int, depth int) interface{}
	hydrate = func(i int, depth int) interface{} {
		if i < 0 || i >= len(arr) || depth > 200 {
			return nil // sentinels and anything out of range
		}
		if v, ok := memo[i]; ok {
			return v
		}
		switch v := arr[i].(type) {
		case map[string]interface{}:
			out := make(map[string]interface{}, len(v))
			memo[i] = out
			for k, vi := range v {
				key := k
				if strings.HasPrefix(k, "_") {
					if ki, err := strconv.Atoi(k[1:]); err == nil && ki >= 0 && ki < len(arr) {
						if s, ok := arr[ki].(string); ok {
							key = s
						}
					}
				}
				out[key] = hydrate(turboIndex(vi), depth+1)
			}
			return out
		case []interface{}:
			if len(v) > 0 {
				if _, typed := v[0].(string); typed {
					var out interface{}
					if len(v) > 1 {
						out = v[1]
					}
					memo[i] = out
					return out
				}
			}
			out := make([]interface{}, 0, len(v))
			for _, vi := range v {
				out = append(out, hydrate(turboIndex(vi), depth+1))
			}
			memo[i] = out
			return out
		case json.Number:
			f, _ := v.Float64()
			memo[i] = f
			return f
		default:
			memo[i] = v
			return v
		}
	}
	return hydrate(0, 0), nil
}

func turboIndex(v interface{}) int {
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return -1
}

// RemixArticle is an article recovered from a hydration payload
type RemixArticle struct {
	Title    string
	BodyHTML string
	ImageURL string
}

var remixBodyKeys = map[string]bool{"content": true, "body": true, "html": true, "articlebody": true, "contenthtml": true, "bodyhtml": true}
var remixImageKeys = []string{"featuredImageUrl", "coverImageUrl", "imageUrl", "image", "coverImage", "featuredImage", "thumbnail"}

// ExtractRemixArticle finds the one object whose "slug" matches the last path
// segment of pageURL (or of the canonical URL) and that carries a body. The slug
// match matters: the payload also holds the bodies of related posts, which are
// often longer than the article itself.
func ExtractRemixArticle(page, pageURL string) (RemixArticle, bool) {
	text := extractTurboStreamText(page)
	if text == "" {
		return RemixArticle{}, false
	}
	root, err := decodeTurboStream(text)
	if err != nil {
		return RemixArticle{}, false
	}

	slugs := map[string]bool{}
	for _, u := range []string{pageURL, canonicalHref(page)} {
		if s := lastPathSegment(u); s != "" {
			slugs[s] = true
		}
	}
	if len(slugs) == 0 {
		return RemixArticle{}, false
	}

	// turbo-stream encodes a value used twice as a reference, so the decoded
	// tree shares maps and slices and can loop: visit each one once
	var found []RemixArticle
	visited := map[uintptr]bool{}
	var walk func(o interface{}, depth int)
	walk = func(o interface{}, depth int) {
		if depth > 60 {
			return
		}
		switch v := o.(type) {
		case map[string]interface{}:
			if id := reflect.ValueOf(v).Pointer(); visited[id] {
				return
			} else {
				visited[id] = true
			}
			if slug, ok := v["slug"].(string); ok && slugs[lastPathSegment("/"+strings.Trim(slug, "/"))] {
				if a, ok := remixArticleFrom(v); ok {
					found = append(found, a)
				}
			}
			for _, val := range v {
				walk(val, depth+1)
			}
		case []interface{}:
			if len(v) == 0 {
				return
			}
			if id := reflect.ValueOf(v).Pointer(); visited[id] {
				return
			} else {
				visited[id] = true
			}
			for _, val := range v {
				walk(val, depth+1)
			}
		}
	}
	walk(root, 0)

	if len(found) != 1 {
		return RemixArticle{}, false // none, or ambiguous: don't guess
	}
	return found[0], true
}

// remixArticleFrom reads title, body and image from a slug-matched object
func remixArticleFrom(v map[string]interface{}) (RemixArticle, bool) {
	var a RemixArticle
	for k, val := range v {
		s, ok := val.(string)
		if ok && len(s) >= 200 && remixBodyKeys[strings.ToLower(k)] && len(s) > len(a.BodyHTML) {
			a.BodyHTML = s
		}
	}
	if a.BodyHTML == "" {
		return RemixArticle{}, false
	}
	a.Title, _ = v["title"].(string)
	for _, key := range remixImageKeys {
		if s, ok := v[key].(string); ok && strings.HasPrefix(s, "http") {
			a.ImageURL = s
			break
		}
		if m, ok := v[key].(map[string]interface{}); ok {
			if s, ok := m["url"].(string); ok && s != "" {
				a.ImageURL = s
				break
			}
		}
	}
	return a, true
}

var bodyOpenTagRe = regexp.MustCompile(`(?i)<body\b[^>]*>`)

// injectArticleIntoBody puts the recovered article right after <body> so the
// normal readability and image pipeline runs on it, with the head metadata kept
func injectArticleIntoBody(page string, a RemixArticle) string {
	article := "<article><h1>" + html.EscapeString(a.Title) + "</h1>"
	if a.ImageURL != "" && !strings.Contains(page, a.ImageURL) {
		article += `<img src="` + html.EscapeString(a.ImageURL) + `">`
	}
	article += a.BodyHTML + "</article>"

	// Match on the page itself: lowercasing can change byte offsets (İ becomes i)
	loc := bodyOpenTagRe.FindStringIndex(page)
	if loc == nil {
		return page + article
	}
	return page[:loc[1]] + article + page[loc[1]:]
}

var canonicalRe = regexp.MustCompile(`<link[^>]+rel="canonical"[^>]+href="([^"]+)"`)

func canonicalHref(page string) string {
	if m := canonicalRe.FindStringSubmatch(page); m != nil {
		return m[1]
	}
	return ""
}

func lastPathSegment(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := strings.Trim(u.Path, "/")
	if p == "" {
		return ""
	}
	return p[strings.LastIndex(p, "/")+1:]
}
