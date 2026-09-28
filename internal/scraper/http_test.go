package scraper

import "testing"

func TestHTTPLooksLikeCFBlock(t *testing.T) {
	cases := []struct {
		name string
		html string
		want bool
	}{
		{
			// cnevpost and carnewschina: plain articles that load Cloudflare's own scripts
			name: "article served through Cloudflare",
			html: `<html><head><title>Mazda launches updated EZ-60 in China</title>
				<script type="module" src="https://static.cloudflareinsights.com/beacon.min.js/v31edd6df95cf"></script>
				</head><body><article><p>Mazda has launched the updated EZ-60.</p></article>
				<script data-cfasync="false" src="/cdn-cgi/scripts/5c5dd728/cloudflare-static/email-decode.min.js"></script>
				</body></html>`,
			want: false,
		},
		{
			name: "challenge page as served to plain HTTP",
			html: `<html><head><title>Just a moment...</title></head>
				<body><span id="challenge-error-text">Enable JavaScript and cookies to continue</span></body></html>`,
			want: true,
		},
		{
			// tvinsider half a second into the challenge: the script has replaced the
			// noscript text and has not drawn its own yet
			name: "challenge page mid-render",
			html: `<html><head><title>Just a moment...</title></head><body><div class="main-content"></div>
				<script>(function(){window._cf_chl_opt = {cType: 'managed'};})();</script></body></html>`,
			want: true,
		},
		{
			name: "article quoting the challenge title",
			html: `<html><head><title>Paradise season 3 update</title></head>
				<body><article><p>"Just a moment..." she said, and the scene cut away.</p></article></body></html>`,
			want: false,
		},
		{
			// tvinsider: what the challenge renders to in a browser
			name: "challenge page as rendered",
			html: `<body><h1>www.tvinsider.com</h1><h2>Performing security verification</h2>
				<p>This website uses a security service to protect against malicious bots.</p></body>`,
			want: true,
		},
		{
			name: "block page",
			html: `<body><h1>Sorry, you have been blocked</h1><p>Cloudflare Ray ID: 8c1f2e3d4a5b6c7d</p></body>`,
			want: true,
		},
	}

	h := NewHTTPClient()
	for _, c := range cases {
		if got := h.LooksLikeCFBlock(c.html); got != c.want {
			t.Errorf("%s: LooksLikeCFBlock = %v, want %v", c.name, got, c.want)
		}
	}
}
