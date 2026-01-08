package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type indexTemplateEntry struct {
	once sync.Once
	html string
	err  error
}

var indexTemplateCache sync.Map

func getIndexTemplate(staticDir string) (string, error) {
	staticDir = filepath.Clean(staticDir)
	if staticDir == "" {
		return "", fmt.Errorf("staticDir is empty")
	}
	val, _ := indexTemplateCache.LoadOrStore(staticDir, &indexTemplateEntry{})
	entry := val.(*indexTemplateEntry)
	entry.once.Do(func() {
		bytes, err := os.ReadFile(filepath.Join(staticDir, "index.html"))
		if err != nil {
			entry.err = err
			return
		}
		entry.html = string(bytes)
	})
	return entry.html, entry.err
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); proto != "" {
		if sanitized := sanitizeScheme(proto); sanitized != "" {
			scheme = sanitized
		}
	}
	host := sanitizeHost(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]))
	if host == "" {
		host = sanitizeHost(r.Host)
	}
	return scheme + "://" + host
}

func sanitizeScheme(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "http" || s == "https" {
		return s
	}
	return ""
}

func sanitizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/\\@") || strings.Contains(host, " ") {
		return ""
	}
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '.', '-', ':', '[', ']':
			continue
		default:
			return ""
		}
	}
	return host
}

func injectBeforeEndTag(doc, tag, injection string) string {
	idx := strings.Index(doc, tag)
	if idx < 0 {
		return doc
	}
	return doc[:idx] + injection + doc[idx:]
}

func setTitle(doc, title string) string {
	open := strings.Index(strings.ToLower(doc), "<title>")
	if open < 0 {
		return injectBeforeEndTag(doc, "</head>", "<title>"+html.EscapeString(title)+"</title>")
	}
	close := strings.Index(strings.ToLower(doc[open:]), "</title>")
	if close < 0 {
		return doc
	}
	close += open
	return doc[:open] + "<title>" + html.EscapeString(title) + "</title>" + doc[close+len("</title>"):]
}

func injectIntoAppRoot(doc, innerHTML string) string {
	lower := strings.ToLower(doc)
	start := strings.Index(lower, "<app-root")
	if start < 0 {
		return doc
	}
	tagEnd := strings.Index(lower[start:], ">")
	if tagEnd < 0 {
		return doc
	}
	tagEnd += start
	end := strings.Index(lower[tagEnd:], "</app-root>")
	if end < 0 {
		return doc
	}
	end += tagEnd
	return doc[:tagEnd+1] + innerHTML + doc[tagEnd+1:end] + doc[end:]
}

func stripHTMLTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func excerptFromArticle(a article, maxRunes int) string {
	content := strings.TrimSpace(a.BodyHTML)
	if content == "" {
		content = renderMarkdown(a.BodyMD)
	}
	text := html.UnescapeString(stripHTMLTags(content))
	text = collapseWhitespace(text)
	return truncateRunes(text, maxRunes)
}

func buildJSONLD(data any) string {
	bytes, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(bytes)
}

func escapeJSONForHTMLScript(jsonLD string) string {
	return strings.ReplaceAll(jsonLD, "</", "<\\/")
}

func seoHead(siteTitle, pageTitle, description, canonical, ogType, jsonLD string) string {
	fullTitle := pageTitle
	if siteTitle != "" && pageTitle != "" && siteTitle != pageTitle {
		fullTitle = pageTitle + " - " + siteTitle
	} else if siteTitle != "" && pageTitle == "" {
		fullTitle = siteTitle
	}

	var b strings.Builder
	b.WriteString(`<meta name="description" content="` + html.EscapeString(description) + `">`)
	b.WriteString(`<link rel="canonical" href="` + html.EscapeString(canonical) + `">`)
	b.WriteString(`<meta property="og:title" content="` + html.EscapeString(fullTitle) + `">`)
	b.WriteString(`<meta property="og:description" content="` + html.EscapeString(description) + `">`)
	b.WriteString(`<meta property="og:url" content="` + html.EscapeString(canonical) + `">`)
	if siteTitle != "" {
		b.WriteString(`<meta property="og:site_name" content="` + html.EscapeString(siteTitle) + `">`)
	}
	if ogType == "" {
		ogType = "website"
	}
	b.WriteString(`<meta property="og:type" content="` + html.EscapeString(ogType) + `">`)
	b.WriteString(`<meta name="twitter:card" content="summary">`)
	if jsonLD != "" {
		b.WriteString(`<script type="application/ld+json">` + escapeJSONForHTMLScript(jsonLD) + `</script>`)
	}
	return b.String()
}

func (s *server) queryPublishedPostBySlug(ctx context.Context, slug string) (article, bool, error) {
	var a article
	var archiveName sql.NullString
	var publishedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT art.id, art.type, art.title, art.slug, COALESCE(ar.name, '') AS archive, art.status,
		       art.body_md, art.body_html, art.published_at, art.created_at, art.updated_at
		FROM articles art
		LEFT JOIN archives ar ON ar.id = art.archive_id
		WHERE art.status='published' AND art.type='post' AND art.slug=$1
		LIMIT 1`, slug).
		Scan(&a.ID, &a.Type, &a.Title, &a.Slug, &archiveName, &a.Status, &a.BodyMD, &a.BodyHTML, &publishedAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if errorsIsNotFound(err) {
			return article{}, false, nil
		}
		return article{}, false, err
	}
	if archiveName.Valid {
		a.Archive = archiveName.String
	}
	if publishedAt.Valid {
		a.PublishedAt = &publishedAt.Time
	}
	return a, true, nil
}

func errorsIsNotFound(err error) bool {
	return err == sql.ErrNoRows
}

func (s *server) queryLatestPosts(ctx context.Context, limit int) ([]article, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT art.id, art.type, art.title, art.slug, COALESCE(ar.name, '') AS archive, art.status,
		       art.body_md, art.body_html, art.published_at, art.created_at, art.updated_at
		FROM articles art
		LEFT JOIN archives ar ON ar.id = art.archive_id
		WHERE art.status='published' AND art.type='post'
		ORDER BY COALESCE(art.published_at, art.created_at) DESC, art.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []article
	for rows.Next() {
		var a article
		var archiveName sql.NullString
		var publishedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.Type, &a.Title, &a.Slug, &archiveName, &a.Status, &a.BodyMD, &a.BodyHTML, &publishedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if archiveName.Valid {
			a.Archive = archiveName.String
		}
		if publishedAt.Valid {
			a.PublishedAt = &publishedAt.Time
		}
		items = append(items, a)
	}
	return items, nil
}

func (s *server) queryAllPublishedPostSlugs(ctx context.Context) ([]struct {
	Slug    string
	Updated time.Time
}, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT slug, updated_at
		FROM articles
		WHERE status='published' AND type='post'
		ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []struct {
		Slug    string
		Updated time.Time
	}
	for rows.Next() {
		var it struct {
			Slug    string
			Updated time.Time
		}
		if err := rows.Scan(&it.Slug, &it.Updated); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

func (s *server) queryCategorySummaries(ctx context.Context) ([]categorySummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(ar.name, '未分类') AS name, COUNT(*) AS count
		FROM articles art
		LEFT JOIN archives ar ON ar.id = art.archive_id
		WHERE art.status = 'published' AND art.type = 'post'
		GROUP BY COALESCE(ar.name, '未分类')
		ORDER BY count DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []categorySummary
	for rows.Next() {
		var cs categorySummary
		if err := rows.Scan(&cs.Name, &cs.Count); err != nil {
			return nil, err
		}
		items = append(items, cs)
	}
	return items, nil
}

func (s *server) queryPostsByArchive(ctx context.Context, archive string, limit int) ([]article, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	archive = strings.TrimSpace(archive)

	var rows *sql.Rows
	var err error
	if archive == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT art.id, art.type, art.title, art.slug, COALESCE(ar.name, '') AS archive, art.status,
			       '' AS body_md, '' AS body_html, art.published_at, art.created_at, art.updated_at
			FROM articles art
			LEFT JOIN archives ar ON ar.id = art.archive_id
			WHERE art.status='published' AND art.type='post'
			ORDER BY COALESCE(art.published_at, art.created_at) DESC, art.created_at DESC
			LIMIT $1`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT art.id, art.type, art.title, art.slug, COALESCE(ar.name, '') AS archive, art.status,
			       '' AS body_md, '' AS body_html, art.published_at, art.created_at, art.updated_at
			FROM articles art
			LEFT JOIN archives ar ON ar.id = art.archive_id
			WHERE art.status='published' AND art.type='post' AND COALESCE(ar.name, '') = $1
			ORDER BY COALESCE(art.published_at, art.created_at) DESC, art.created_at DESC
			LIMIT $2`, archive, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []article
	for rows.Next() {
		var a article
		var archiveName sql.NullString
		var publishedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.Type, &a.Title, &a.Slug, &archiveName, &a.Status, &a.BodyMD, &a.BodyHTML, &publishedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if archiveName.Valid {
			a.Archive = archiveName.String
		}
		if publishedAt.Valid {
			a.PublishedAt = &publishedAt.Time
		}
		items = append(items, a)
	}
	return items, nil
}

func (s *server) seoHomeHandler(staticDir, siteTitle string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		base := requestBaseURL(c.Request)
		canonical := base + "/"

		items, err := s.queryLatestPosts(ctx, 20)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		var b strings.Builder
		b.WriteString(`<section class="space-y-6 py-[3em]">`)
		for _, it := range items {
			desc := excerptFromArticle(it, 180)
			b.WriteString(`<article class="article-entry space-y-3">`)
			b.WriteString(`<header class="space-y-1">`)
			b.WriteString(`<h2 class="text-[1.6rem] font-semibold text-[#3d3d3f] py-2">`)
			b.WriteString(`<a href="/post/` + urlPathEscape(it.Slug) + `" class="text-[#3c546c]">` + html.EscapeString(it.Title) + `</a>`)
			b.WriteString(`</h2>`)
			b.WriteString(`<p class="text-xs text-[#aaa] py-1">发布时间：` + html.EscapeString(it.CreatedAt.Format("2006-01-02 15:04")) + `</p>`)
			b.WriteString(`</header>`)
			b.WriteString(`<p class="text-[16px] leading-8 text-[#3d3d3f] tracking-[0.0625em]">` + html.EscapeString(desc) + `</p>`)
			b.WriteString(`</article>`)
		}
		b.WriteString(`</section>`)

		description := "最新文章列表"
		if siteTitle != "" {
			description = siteTitle + " - " + description
		}
		headExtras := seoHead(siteTitle, siteTitle, description, canonical, "website", "")

		doc, err := getIndexTemplate(staticDir)
		if err != nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, minimalHTML(siteTitle, headExtras, b.String()))
			return
		}
		doc = setTitle(doc, siteTitle)
		doc = injectBeforeEndTag(doc, "</head>", headExtras)
		doc = injectIntoAppRoot(doc, b.String())
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, doc)
	}
}

func (s *server) seoPostHandler(staticDir, siteTitle string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		slug := strings.TrimSpace(c.Param("slug"))
		if slug == "" {
			c.Status(http.StatusNotFound)
			return
		}

		a, ok, err := s.queryPublishedPostBySlug(ctx, slug)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}

		base := requestBaseURL(c.Request)
		canonical := base + "/post/" + urlPathEscape(slug)
		desc := excerptFromArticle(a, 180)

		var jsonLD string
		jsonLD = buildJSONLD(map[string]any{
			"@context": "https://schema.org",
			"@type":    "BlogPosting",
			"headline": a.Title,
			"datePublished": func() string {
				if a.PublishedAt != nil {
					return a.PublishedAt.Format(time.RFC3339)
				}
				return a.CreatedAt.Format(time.RFC3339)
			}(),
			"dateModified":        a.UpdatedAt.Format(time.RFC3339),
			"mainEntityOfPage":    canonical,
			"url":                 canonical,
			"isAccessibleForFree": true,
		})

		headExtras := seoHead(siteTitle, a.Title, desc, canonical, "article", jsonLD)

		bodyHTML := strings.TrimSpace(a.BodyHTML)
		if bodyHTML == "" {
			bodyHTML = renderMarkdown(a.BodyMD)
		}
		archiveName := a.Archive
		if strings.TrimSpace(archiveName) == "" {
			archiveName = "未分类"
		}

		var b strings.Builder
		b.WriteString(`<section class="space-y-5 py-6">`)
		b.WriteString(`<article class="space-y-3">`)
		b.WriteString(`<header class="post-meta">`)
		b.WriteString(`<h1 class="post-title text-[2rem] font-semibold text-[#3d3d3f] py-[4em]">` + html.EscapeString(a.Title) + `</h1>`)
		publishedAt := a.CreatedAt
		if a.PublishedAt != nil {
			publishedAt = *a.PublishedAt
		}
		b.WriteString(`<p class="post-time text-xs text-[#aaa]">发布时间：` + html.EscapeString(publishedAt.Format("2006-01-02 15:04")) + `</p>`)
		b.WriteString(`<p class="post-time text-xs text-[#aaa]">分类：<a href="/category/` + urlPathEscape(archiveName) + `" class="category-link">` + html.EscapeString(archiveName) + `</a></p>`)
		b.WriteString(`</header>`)
		b.WriteString(`<div class="article-body space-y-3 text-[16px] leading-8 text-[#3d3d3f] tracking-[0.0625em]">` + bodyHTML + `</div>`)
		b.WriteString(`<div class="pt-2"><a href="/" class="text-sm text-[#3c546c] hover:underline">← 返回首页</a></div>`)
		b.WriteString(`</article>`)
		b.WriteString(`</section>`)

		doc, err := getIndexTemplate(staticDir)
		if err != nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, minimalHTML(a.Title, headExtras, b.String()))
			return
		}
		doc = setTitle(doc, a.Title)
		doc = injectBeforeEndTag(doc, "</head>", headExtras)
		doc = injectIntoAppRoot(doc, b.String())
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, doc)
	}
}

func (s *server) seoCategoriesHandler(staticDir, siteTitle string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		base := requestBaseURL(c.Request)
		canonical := base + "/categories"

		items, err := s.queryCategorySummaries(ctx)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		var b strings.Builder
		b.WriteString(`<section class="mx-auto max-w-3xl px-6 py-8 text-center sm:px-9 md:px-12 lg:px-[10rem]">`)
		b.WriteString(`<div class="grid grid-cols-1 gap-4">`)
		for _, it := range items {
			b.WriteString(`<a class="rounded border border-slate-200 px-4 py-3 text-left transition hover:border-[#3273dc] hover:bg-[#f6f9ff]" href="/category/` + urlPathEscape(it.Name) + `">`)
			b.WriteString(`<div class="text-[1.2rem] font-bold text-[#3273dc] tracking-[0.09375em]">` + html.EscapeString(it.Name) + `</div>`)
			b.WriteString(`<div class="mt-1 text-xs text-[#aaa]">` + fmt.Sprintf("%d", it.Count) + ` 篇</div>`)
			b.WriteString(`</a>`)
		}
		b.WriteString(`</div></section>`)

		headExtras := seoHead(siteTitle, "分类", "分类列表", canonical, "website", "")
		doc, err := getIndexTemplate(staticDir)
		if err != nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, minimalHTML("分类", headExtras, b.String()))
			return
		}
		doc = setTitle(doc, "分类")
		doc = injectBeforeEndTag(doc, "</head>", headExtras)
		doc = injectIntoAppRoot(doc, b.String())
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, doc)
	}
}

func (s *server) seoArchiveHandler(staticDir, siteTitle string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		selected := strings.TrimSpace(c.Query("archive"))
		base := requestBaseURL(c.Request)
		canonical := base + "/archive"
		if selected != "" {
			canonical += "?archive=" + urlQueryEscape(selected)
		}

		posts, err := s.queryPostsByArchive(ctx, selected, 200)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		var b strings.Builder
		b.WriteString(`<section class="mx-auto max-w-3xl px-6 py-8 text-center sm:px-9 md:px-12 lg:px-[10rem]">`)
		if selected != "" {
			b.WriteString(`<div class="mb-4 inline-flex rounded-[3px] bg-[#3273dc] px-3 py-1 text-sm font-semibold text-white">` + html.EscapeString(selected) + `</div>`)
		}
		for _, it := range posts {
			b.WriteString(`<div class="pb-6 space-y-1">`)
			b.WriteString(`<div class="text-[1.4rem] font-bold tracking-[0.09375em]">`)
			b.WriteString(`<a href="/post/` + urlPathEscape(it.Slug) + `" class="text-[#3273dc] no-underline">` + html.EscapeString(it.Title) + `</a>`)
			b.WriteString(`</div>`)
			b.WriteString(`<div class="mt-1 text-xs text-[#aaa]">` + html.EscapeString(it.CreatedAt.Format("2006-01-02 15:04")) + `</div>`)
			b.WriteString(`</div>`)
		}
		b.WriteString(`</section>`)

		title := "归档"
		if selected != "" {
			title = "归档 - " + selected
		}
		headExtras := seoHead(siteTitle, title, "归档文章列表", canonical, "website", "")

		doc, err := getIndexTemplate(staticDir)
		if err != nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, minimalHTML(title, headExtras, b.String()))
			return
		}
		doc = setTitle(doc, title)
		doc = injectBeforeEndTag(doc, "</head>", headExtras)
		doc = injectIntoAppRoot(doc, b.String())
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, doc)
	}
}

func (s *server) seoCategoryHandler(staticDir, siteTitle string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		name := strings.TrimSpace(c.Param("name"))
		if name == "" {
			c.Status(http.StatusNotFound)
			return
		}

		queryName := name
		if name == "未分类" {
			queryName = ""
		}

		base := requestBaseURL(c.Request)
		canonical := base + "/category/" + urlPathEscape(name)

		posts, err := s.queryPostsByArchive(ctx, queryName, 200)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		var b strings.Builder
		b.WriteString(`<section class="mx-auto max-w-3xl px-6 py-8 text-center sm:px-9 md:px-12 lg:px-[10rem]">`)
		b.WriteString(`<div class="mb-4 inline-flex rounded-[3px] bg-[#3273dc] px-3 py-1 text-sm font-semibold text-white">` + html.EscapeString(name) + `</div>`)
		for _, it := range posts {
			b.WriteString(`<div class="pb-6 space-y-1">`)
			b.WriteString(`<div class="text-[1.4rem] font-bold tracking-[0.09375em]">`)
			b.WriteString(`<a href="/post/` + urlPathEscape(it.Slug) + `" class="text-[#3273dc] no-underline">` + html.EscapeString(it.Title) + `</a>`)
			b.WriteString(`</div>`)
			b.WriteString(`<div class="mt-1 text-xs text-[#aaa]">` + html.EscapeString(it.CreatedAt.Format("2006-01-02 15:04")) + `</div>`)
			b.WriteString(`</div>`)
		}
		b.WriteString(`</section>`)

		title := "分类 - " + name
		headExtras := seoHead(siteTitle, title, "分类文章列表", canonical, "website", "")

		doc, err := getIndexTemplate(staticDir)
		if err != nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, minimalHTML(title, headExtras, b.String()))
			return
		}
		doc = setTitle(doc, title)
		doc = injectBeforeEndTag(doc, "</head>", headExtras)
		doc = injectIntoAppRoot(doc, b.String())
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, doc)
	}
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

func (s *server) seoSitemapHandler(siteTitle string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		base := requestBaseURL(c.Request)

		slugs, err := s.queryAllPublishedPostSlugs(ctx)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		categories, err := s.queryCategorySummaries(ctx)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		var urls []sitemapURL
		urls = append(urls, sitemapURL{Loc: base + "/"})
		urls = append(urls, sitemapURL{Loc: base + "/archive"})
		urls = append(urls, sitemapURL{Loc: base + "/categories"})
		_ = siteTitle
		for _, it := range categories {
			if strings.TrimSpace(it.Name) == "" {
				continue
			}
			urls = append(urls, sitemapURL{
				Loc: base + "/category/" + url.PathEscape(it.Name),
			})
		}
		for _, it := range slugs {
			if strings.TrimSpace(it.Slug) == "" {
				continue
			}
			urls = append(urls, sitemapURL{
				Loc:     base + "/post/" + url.PathEscape(it.Slug),
				LastMod: it.Updated.Format(time.RFC3339),
			})
		}

		payload := sitemapURLSet{
			Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
			URLs:  urls,
		}
		bytes, err := xml.MarshalIndent(payload, "", "  ")
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		c.Header("Content-Type", "application/xml; charset=utf-8")
		c.Header("Vary", "Host, X-Forwarded-Proto, X-Forwarded-Host")
		c.Header("Cache-Control", "public, max-age=300")
		c.String(http.StatusOK, xml.Header+string(bytes))
	}
}

func (s *server) seoRobotsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		base := requestBaseURL(c.Request)
		lines := []string{
			"User-agent: *",
			"Allow: /",
			"Disallow: /admin",
			"Disallow: /api",
			"Sitemap: " + base + "/sitemap.xml",
			"",
		}
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.Header("Vary", "Host, X-Forwarded-Proto, X-Forwarded-Host")
		c.Header("Cache-Control", "public, max-age=300")
		c.String(http.StatusOK, strings.Join(lines, "\n"))
	}
}

func minimalHTML(title, headExtras, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title>` + headExtras +
		`</head><body>` + body + `</body></html>`
}

func urlPathEscape(s string) string {
	return url.PathEscape(s)
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}
