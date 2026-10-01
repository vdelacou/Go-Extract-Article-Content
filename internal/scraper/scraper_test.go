package scraper

import (
	"strings"
	"testing"

	"extract-html-scraper/internal/models"
)

func TestBrowserResultBetter(t *testing.T) {
	body := strings.Repeat("The robot sparred without teleoperation. ", 40)
	cases := []struct {
		name    string
		phase1  models.ScrapeResponse
		browser models.ScrapeResponse
		want    bool
	}{
		{
			name:    "browser found the body of the same story",
			phase1:  models.ScrapeResponse{Title: "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model"},
			browser: models.ScrapeResponse{Title: "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model - Pandaily", Content: body},
			want:    true,
		},
		{
			name:    "browser landed on the site's not-found page",
			phase1:  models.ScrapeResponse{Title: "AgiBot Unveils GE-Act 2.0 Native World-Action Model"},
			browser: models.ScrapeResponse{Title: "Pandaily - China Tech News, AI & Electric Vehicle Insights", Content: body},
			want:    false,
		},
		{
			name:    "browser text is still thin",
			phase1:  models.ScrapeResponse{Title: "Story"},
			browser: models.ScrapeResponse{Title: "Story", Content: "Short teaser."},
			want:    false,
		},
		{
			name:    "browser text is not clearly longer",
			phase1:  models.ScrapeResponse{Title: "Story", Content: body[:400]},
			browser: models.ScrapeResponse{Title: "Story", Content: body[:700]},
			want:    false,
		},
		{
			name:    "Chinese titles compare by character",
			phase1:  models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期"},
			browser: models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期 | 极客公园", Content: body},
			want:    true,
		},
		{
			name:    "different Chinese story",
			phase1:  models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期"},
			browser: models.ScrapeResponse{Title: "页面不存在", Content: body},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserResultBetter(tc.phase1, tc.browser); got != tc.want {
				t.Errorf("browserResultBetter = %v, want %v (overlap %.2f)", got, tc.want, titleOverlap(tc.phase1.Title, tc.browser.Title))
			}
		})
	}
}
