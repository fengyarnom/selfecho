package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosimple/slug"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/russross/blackfriday/v2"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"gopkg.in/yaml.v3"
)

type healthPayload struct {
	CPUPercent      float64 `json:"cpuPercent"`
	TotalMem        uint64  `json:"totalMemBytes"`
	UsedMem         uint64  `json:"usedMemBytes"`
	DiskTotal       uint64  `json:"diskTotalBytes"`
	DiskUsed        uint64  `json:"diskUsedBytes"`
	ProcessRSS      uint64  `json:"processRssBytes"`
	ProcessVMS      uint64  `json:"processVmsBytes"`
	ProcessFDs      int32   `json:"processOpenFds"`
	DBOpen          int     `json:"dbOpen"`
	DBIdle          int     `json:"dbIdle"`
	DBInUse         int     `json:"dbInUse"`
	GoVersion       string  `json:"goVersion"`
	BinarySize      int64   `json:"binarySizeBytes"`
	Goroutines      int     `json:"goroutines"`
	UptimeSeconds   int64   `json:"uptimeSeconds"`
	DBLatencyMs     float64 `json:"dbLatencyMs"`
	CacheEntries    int     `json:"cacheEntries"`
	CacheHits       int64   `json:"cacheHits"`
	CacheMisses     int64   `json:"cacheMisses"`
	CacheHitRate    float64 `json:"cacheHitRate"`
	CacheTTLSeconds int64   `json:"cacheTtlSeconds"`
}

type article struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Archive     string     `json:"archive,omitempty"`
	Status      string     `json:"status"`
	BodyMD      string     `json:"bodyMd"`
	BodyHTML    string     `json:"bodyHtml,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type config struct {
	Database  dbConfig   `yaml:"database"`
	Site      siteConfig `yaml:"site"`
	Port      int        `yaml:"port"`
	StaticDir string     `yaml:"staticDir"`
}

type dbConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslmode"`
}

type siteConfig struct {
	Title string `yaml:"title" json:"title"`
}

func defaultConfig() config {
	return config{
		Database: dbConfig{
			Host:     "127.0.0.1",
			Port:     5432,
			User:     "username",
			Password: "password",
			Name:     "selfechodb",
			SSLMode:  "disable",
		},
		Site: siteConfig{
			Title: "Yarnom'Blog",
		},
		Port:      8080,
		StaticDir: "./static",
	}
}

type server struct {
	db        *sql.DB
	cache     *listCache
	startedAt time.Time
}

func (s *server) backfillBodyHTML(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, body_md FROM articles WHERE (body_html IS NULL OR body_html = '')`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type item struct {
		id   string
		body string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.body); err != nil {
			return err
		}
		items = append(items, it)
	}

	for _, it := range items {
		html := string(blackfriday.Run([]byte(it.body)))
		_, err := s.db.ExecContext(ctx, `UPDATE articles SET body_html=$1, updated_at=now() WHERE id=$2`, html, it.id)
		if err != nil {
			return err
		}
	}
	return nil
}

func loadConfig(path string) (config, error) {
	cfg := defaultConfig()
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("warn: 未找到配置文件 %s，使用默认配置\n", path)
			return cfg, nil
		}
		return cfg, fmt.Errorf("读取配置失败: %w", err)
	}
	if err := yaml.Unmarshal(bytes, &cfg); err != nil {
		return cfg, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.Database.Host == "" || cfg.Database.User == "" || cfg.Database.Name == "" || cfg.Database.Port == 0 {
		return cfg, errors.New("配置不完整: database.host/user/name/port 必填")
	}
	if cfg.Site.Title == "" {
		cfg.Site.Title = defaultConfig().Site.Title
	}
	if cfg.Port == 0 {
		cfg.Port = defaultConfig().Port
	}
	if cfg.StaticDir == "" {
		cfg.StaticDir = defaultConfig().StaticDir
	}
	return cfg, nil
}

func buildDSN(cfg dbConfig) string {
	sslmode := cfg.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, sslmode)
}

func ensureDB(ctx context.Context, cfg dbConfig) (*sql.DB, error) {
	dsn := buildDSN(cfg)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("创建数据库连接失败: %w", err)
	}
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetMaxIdleConns(5)
	db.SetMaxOpenConns(10)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("数据库连接失败: %w", err)
	}
	return db, nil
}

func makeSlug(title, provided string) (string, error) {
	if provided != "" {
		s := strings.TrimSpace(provided)
		s = slug.Make(s)
		if s == "" {
			return "", errors.New("slug 不合法")
		}
		return s, nil
	}

	base := strings.TrimSpace(title)
	if base == "" {
		return "", errors.New("标题为空，无法生成 slug")
	}

	s := slug.MakeLang(base, "zh")
	if s == "" {
		return "", errors.New("无法根据标题生成 slug")
	}
	return s, nil
}

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		// Prefer local config.yaml next to the binary, then parent (for dev)
		if _, err := os.Stat("config.yaml"); err == nil {
			cfgPath = "config.yaml"
		} else if _, err := os.Stat(filepath.Join("..", "config.yaml")); err == nil {
			cfgPath = filepath.Join("..", "config.yaml")
		} else {
			cfgPath = "config.yaml" // default; will fail with clear error if missing
		}
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		panic(err)
	}
	db, err := ensureDB(context.Background(), cfg.Database)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	router := gin.Default()
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "X-Total-Count, X-Page, X-Limit")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	s := &server{db: db, cache: newListCache(30 * time.Second), startedAt: time.Now()}

	router.GET("/api/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "hello from backend"})
	})

	router.GET("/api/site", func(c *gin.Context) {
		c.JSON(http.StatusOK, cfg.Site)
	})

	router.GET("/health", func(c *gin.Context) {
		payload, err := s.collectHealth()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, payload)
	})
	router.GET("/api/health", func(c *gin.Context) {
		payload, err := s.collectHealth()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, payload)
	})

	api := router.Group("/api")
	{
		api.GET("/articles", s.listArticles)
		api.POST("/articles", s.createArticle)
		api.PUT("/articles/:id", s.updateArticle)
		api.DELETE("/articles/:id", s.deleteArticle)
		api.GET("/archives", s.listArchives)
		api.POST("/archives", s.createArchive)
		api.PUT("/archives/:id", s.updateArchive)
		api.DELETE("/archives/:id", s.deleteArchive)
		api.GET("/categories", s.listCategories)
	}

	if err := s.backfillBodyHTML(context.Background()); err != nil {
		fmt.Printf("warn: backfill body_html failed: %v\n", err)
	}

	serveSPA(router, cfg.StaticDir)

	router.Run(fmt.Sprintf(":%d", cfg.Port))
}

type archive struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type archivePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type categorySummary struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type cachedList struct {
	items    []article
	total    int
	cachedAt time.Time
}

type listCache struct {
	mu     sync.RWMutex
	data   map[string]cachedList
	ttl    time.Duration
	hits   int64
	misses int64
}

func newListCache(ttl time.Duration) *listCache {
	return &listCache{
		data: make(map[string]cachedList),
		ttl:  ttl,
	}
}

func (c *listCache) key(status, archive string, page, limit int, compact bool) string {
	return fmt.Sprintf("s=%s|a=%s|p=%d|l=%d|c=%t", status, archive, page, limit, compact)
}

func (c *listCache) get(status, archive string, page, limit int, compact bool) (cachedList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ck := c.key(status, archive, page, limit, compact)
	val, ok := c.data[ck]
	if !ok || time.Since(val.cachedAt) > c.ttl {
		c.misses++
		return cachedList{}, false
	}
	c.hits++
	return val, true
}

func (c *listCache) set(status, archive string, page, limit int, compact bool, items []article, total int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ck := c.key(status, archive, page, limit, compact)
	c.data[ck] = cachedList{
		items:    items,
		total:    total,
		cachedAt: time.Now(),
	}
}

func (c *listCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[string]cachedList)
}

func (c *listCache) stats() (entries int, hits, misses int64, ttlSeconds int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data), c.hits, c.misses, int64(c.ttl.Seconds())
}

func (s *server) collectHealth() (healthPayload, error) {
	var hp healthPayload

	cpuPercent := 0.0
	if percents, err := cpu.Percent(time.Second, false); err == nil && len(percents) > 0 {
		cpuPercent = percents[0]
	}
	hp.CPUPercent = cpuPercent

	memStats, memErr := mem.VirtualMemory()
	diskStats, diskErr := disk.Usage("/")
	if memErr != nil || diskErr != nil {
		return hp, fmt.Errorf("unable to read system metrics")
	}
	hp.TotalMem = memStats.Total
	hp.UsedMem = memStats.Used
	hp.DiskTotal = diskStats.Total
	hp.DiskUsed = diskStats.Used

	proc, _ := process.NewProcess(int32(os.Getpid()))
	if proc != nil {
		if memInfo, err := proc.MemoryInfo(); err == nil {
			hp.ProcessRSS = memInfo.RSS
			hp.ProcessVMS = memInfo.VMS
		}
		if n, err := proc.NumFDs(); err == nil {
			hp.ProcessFDs = n
		}
	}

	if s.db != nil {
		qCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		start := time.Now()
		var one int
		if err := s.db.QueryRowContext(qCtx, `SELECT 1`).Scan(&one); err == nil {
			hp.DBLatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
		}
		cancel()

		stats := s.db.Stats()
		hp.DBOpen = stats.OpenConnections
		hp.DBIdle = stats.Idle
		hp.DBInUse = stats.InUse
	}

	if s.cache != nil {
		entries, hits, misses, ttlSeconds := s.cache.stats()
		hp.CacheEntries = entries
		hp.CacheHits = hits
		hp.CacheMisses = misses
		hp.CacheTTLSeconds = ttlSeconds
		total := hits + misses
		if total > 0 {
			hp.CacheHitRate = float64(hits) / float64(total)
		}
	}

	hp.GoVersion = runtime.Version()
	if exePath, err := os.Executable(); err == nil {
		if info, err := os.Stat(exePath); err == nil {
			hp.BinarySize = info.Size()
		}
	}
	hp.Goroutines = runtime.NumGoroutine()
	if !s.startedAt.IsZero() {
		hp.UptimeSeconds = int64(time.Since(s.startedAt).Seconds())
	}

	return hp, nil
}

// serveSPA serves the built Angular app directly from disk, falling back to index.html
// for client-side routes, while keeping API/health 404s intact.
func serveSPA(router *gin.Engine, staticDir string) {
	if staticDir == "" {
		return
	}
	dir := filepath.Clean(staticDir)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		fmt.Printf("warn: 静态目录不存在，跳过静态文件服务: %s\n", dir)
		return
	}

	indexPath := filepath.Join(dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		fmt.Printf("warn: index.html 不存在于静态目录 %s，跳过静态文件服务\n", dir)
		return
	}

	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api") || path == "/health" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		rel := strings.TrimPrefix(path, "/")
		rel = filepath.Clean(rel)
		if rel == "." || rel == "/" {
			c.File(indexPath)
			return
		}
		fullPath := filepath.Join(dir, rel)
		// prevent path traversal
		if !strings.HasPrefix(fullPath, dir) {
			c.File(indexPath)
			return
		}
		if _, err := os.Stat(fullPath); err == nil {
			c.File(fullPath)
			return
		}
		c.File(indexPath)
	})
}

func (s *server) listArchives(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, COALESCE(description, ''), created_at FROM archives ORDER BY name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询归档失败"})
		return
	}
	defer rows.Close()

	var result []archive
	for rows.Next() {
		var a archive
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "解析归档数据失败"})
			return
		}
		result = append(result, a)
	}
	c.JSON(http.StatusOK, result)
}

func (s *server) listCategories(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(ar.name, '未分类') AS name, COUNT(*) AS count
		FROM articles art
		LEFT JOIN archives ar ON ar.id = art.archive_id
		WHERE art.status = 'published'
		GROUP BY COALESCE(ar.name, '未分类')
		ORDER BY count DESC, name ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分类失败"})
		return
	}
	defer rows.Close()

	var items []categorySummary
	for rows.Next() {
		var cs categorySummary
		if err := rows.Scan(&cs.Name, &cs.Count); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "解析分类数据失败"})
			return
		}
		items = append(items, cs)
	}
	c.JSON(http.StatusOK, items)
}

func (s *server) listArticles(c *gin.Context) {
	ctx := c.Request.Context()
	pageStr := c.Query("page")
	limitStr := c.Query("limit")
	usePaging := pageStr != "" || limitStr != ""
	statusFilter := strings.TrimSpace(c.Query("status"))
	archiveFilter := strings.TrimSpace(c.Query("archive"))
	compact := c.Query("compact") == "1" || strings.EqualFold(c.Query("fields"), "compact")

	page := 1
	limit := 6
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if !usePaging {
		page = 0
		limit = 0
	}

	var total int
	whereParts := []string{}
	args := []any{}
	argPos := 1
	if statusFilter != "" {
		whereParts = append(whereParts, fmt.Sprintf("art.status = $%d", argPos))
		args = append(args, statusFilter)
		argPos++
	}
	if archiveFilter != "" {
		whereParts = append(whereParts, fmt.Sprintf("COALESCE(ar.name, '') = $%d", argPos))
		args = append(args, archiveFilter)
		argPos++
	}
	whereSQL := ""
	if len(whereParts) > 0 {
		whereSQL = "WHERE " + strings.Join(whereParts, " AND ")
	}

	if cached, ok := s.cache.get(statusFilter, archiveFilter, page, limit, compact); ok {
		if usePaging {
			c.Header("X-Total-Count", strconv.Itoa(cached.total))
			c.Header("X-Page", strconv.Itoa(page))
			c.Header("X-Limit", strconv.Itoa(limit))
		}
		c.JSON(http.StatusOK, cached.items)
		return
	}

	if usePaging {
		countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM articles art LEFT JOIN archives ar ON ar.id = art.archive_id %s`, whereSQL)
		if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "统计文章数失败"})
			return
		}
	}

	var rows *sql.Rows
	var err error
	selectBody := "art.body_md, art.body_html"
	if compact {
		selectBody = "'' AS body_md, '' AS body_html"
	}

	if usePaging {
		offset := (page - 1) * limit
		query := fmt.Sprintf(`
			SELECT art.id, art.title, art.slug, COALESCE(ar.name, '') AS archive, art.status, %s,
			       art.published_at, art.created_at, art.updated_at
			FROM articles art
			LEFT JOIN archives ar ON ar.id = art.archive_id
			%s
			ORDER BY art.created_at DESC
			LIMIT $%d OFFSET $%d`, selectBody, whereSQL, argPos, argPos+1)
		argsWithPage := append(args, limit, offset)
		rows, err = s.db.QueryContext(ctx, query, argsWithPage...)
	} else {
		query := fmt.Sprintf(`
			SELECT art.id, art.title, art.slug, COALESCE(ar.name, '') AS archive, art.status, %s,
			       art.published_at, art.created_at, art.updated_at
			FROM articles art
			LEFT JOIN archives ar ON ar.id = art.archive_id
			%s
			ORDER BY art.created_at DESC`, selectBody, whereSQL)
		rows, err = s.db.QueryContext(ctx, query, args...)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文章失败"})
		return
	}
	defer rows.Close()

	var result []article
	for rows.Next() {
		var a article
		var archiveName sql.NullString
		var publishedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.Title, &a.Slug, &archiveName, &a.Status, &a.BodyMD, &a.BodyHTML, &publishedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "解析文章数据失败"})
			return
		}
		if archiveName.Valid {
			a.Archive = archiveName.String
		}
		if publishedAt.Valid {
			a.PublishedAt = &publishedAt.Time
		}
		result = append(result, a)
	}
	if usePaging {
		c.Header("X-Total-Count", strconv.Itoa(total))
		c.Header("X-Page", strconv.Itoa(page))
		c.Header("X-Limit", strconv.Itoa(limit))
		s.cache.set(statusFilter, archiveFilter, page, limit, compact, result, total)
	} else {
		s.cache.set(statusFilter, archiveFilter, page, limit, compact, result, len(result))
	}
	c.JSON(http.StatusOK, result)
}

type articlePayload struct {
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Archive string `json:"archive"`
	Status  string `json:"status"`
	BodyMD  string `json:"bodyMd"`
}

func (s *server) createArticle(c *gin.Context) {
	ctx := c.Request.Context()
	var payload articlePayload
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	if err := validatePayload(payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slug, err := makeSlug(payload.Title, payload.Slug)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var archiveID *string
	if payload.Archive != "" {
		id, err := s.ensureArchive(ctx, payload.Archive)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建归档失败"})
			return
		}
		archiveID = &id
	}

	var publishedAt sql.NullTime
	if payload.Status == "published" {
		publishedAt = sql.NullTime{Valid: true, Time: time.Now()}
	}

	bodyHTML := renderMarkdown(payload.BodyMD)

	var createdID string
	err = s.db.QueryRowContext(
		ctx,
		`INSERT INTO articles (slug, title, body_md, body_html, status, archive_id, published_at) 
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		slug, payload.Title, payload.BodyMD, bodyHTML, payload.Status, archiveID, publishedAt,
	).Scan(&createdID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("创建文章失败: %v", err)})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": createdID})
	s.cache.invalidateAll()
}

func (s *server) updateArticle(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	var payload articlePayload
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	if err := validatePayload(payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slug, err := makeSlug(payload.Title, payload.Slug)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var archiveID *string
	if payload.Archive != "" {
		aid, err := s.ensureArchive(ctx, payload.Archive)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建归档失败"})
			return
		}
		archiveID = &aid
	}

	var publishedAt sql.NullTime
	if payload.Status == "published" {
		publishedAt = sql.NullTime{Valid: true, Time: time.Now()}
	}

	bodyHTML := renderMarkdown(payload.BodyMD)

	res, err := s.db.ExecContext(
		ctx,
		`UPDATE articles 
		 SET title=$1, slug=$2, body_md=$3, body_html=$4, status=$5, archive_id=$6, published_at=$7, updated_at=now()
		 WHERE id=$8`,
		payload.Title, slug, payload.BodyMD, bodyHTML, payload.Status, archiveID, publishedAt, id,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("更新文章失败: %v", err)})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到文章"})
		return
	}
	c.Status(http.StatusNoContent)
	s.cache.invalidateAll()
}

func (s *server) deleteArticle(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	res, err := s.db.ExecContext(ctx, `DELETE FROM articles WHERE id=$1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除文章失败"})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到文章"})
		return
	}
	c.Status(http.StatusNoContent)
	s.cache.invalidateAll()
}

func (s *server) createArchive(c *gin.Context) {
	ctx := c.Request.Context()
	var payload archivePayload
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称不能为空"})
		return
	}
	var id string
	err := s.db.QueryRowContext(ctx, `INSERT INTO archives (name, description) VALUES ($1, $2) RETURNING id`, payload.Name, payload.Description).
		Scan(&id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("创建归档失败: %v", err)})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
	s.cache.invalidateAll()
}

func (s *server) updateArchive(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	var payload archivePayload
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称不能为空"})
		return
	}
	res, err := s.db.ExecContext(ctx, `UPDATE archives SET name=$1, description=$2, created_at=created_at WHERE id=$3`, payload.Name, payload.Description, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("更新归档失败: %v", err)})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到归档"})
		return
	}
	c.Status(http.StatusNoContent)
	s.cache.invalidateAll()
}

func (s *server) deleteArchive(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "启动事务失败"})
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE articles SET archive_id=NULL WHERE archive_id=$1`, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理文章关联失败"})
		return
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM archives WHERE id=$1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除归档失败"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到归档"})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交事务失败"})
		return
	}
	c.Status(http.StatusNoContent)
	s.cache.invalidateAll()
}

func (s *server) ensureArchive(ctx context.Context, name string) (string, error) {
	var id string
	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO archives (name) VALUES ($1)
		 ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name
		 RETURNING id`,
		name,
	).Scan(&id)
	return id, err
}

func validatePayload(p articlePayload) error {
	if p.Title == "" {
		return errors.New("标题不能为空")
	}
	if p.Status != "draft" && p.Status != "published" {
		return errors.New("status 只能是 draft 或 published")
	}
	return nil
}

func renderMarkdown(md string) string {
	return string(blackfriday.Run([]byte(md)))
}
