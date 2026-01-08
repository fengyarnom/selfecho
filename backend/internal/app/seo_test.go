package app

import (
	"strings"
	"testing"
)

func TestSetTitle_ReplacesExisting(t *testing.T) {
	doc := `<!doctype html><html><head><title>Old</title></head><body></body></html>`
	got := setTitle(doc, `New & Title`)
	if !strings.Contains(got, `<title>New &amp; Title</title>`) {
		t.Fatalf("expected replaced title, got: %s", got)
	}
	if strings.Contains(got, "<title>Old</title>") {
		t.Fatalf("expected old title removed, got: %s", got)
	}
}

func TestInjectIntoAppRoot_InsertsInnerHTML(t *testing.T) {
	doc := `<!doctype html><html><body><app-root></app-root></body></html>`
	got := injectIntoAppRoot(doc, `<h1>Hi</h1>`)
	if !strings.Contains(got, `<app-root><h1>Hi</h1></app-root>`) {
		t.Fatalf("expected injected inner html, got: %s", got)
	}
}

func TestSeoHead_JSONLDNotHTMLEscaped(t *testing.T) {
	jsonLD := `{"x":"</script>"}`
	head := seoHead("Site", "Post", "Desc", "https://example.com/post/1", "article", jsonLD)
	if strings.Contains(head, "&quot;") {
		t.Fatalf("unexpected html-escaped json-ld: %s", head)
	}
	if !strings.Contains(head, `<script type="application/ld+json">{"x":"<\/script>"}</script>`) {
		t.Fatalf("expected escaped closing tag sequence, got: %s", head)
	}
}
