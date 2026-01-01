package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/quotedprintable"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
	"github.com/gin-gonic/gin"
	"github.com/gosimple/slug"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/russross/blackfriday/v2"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/crypto/bcrypt"
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

type user struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"createdAt"`
}

type session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type imapAccount struct {
	ID              string    `json:"id"`
	Host            string    `json:"host"`
	Port            int       `json:"port"`
	Username        string    `json:"username"`
	Password        string    `json:"-"`
	UseSSL          bool      `json:"useSsl"`
	UseStartTLS     bool      `json:"useStartTls"`
	LastUID         uint32    `json:"lastUid"`
	LastUIDValidity uint32    `json:"lastUidValidity"`
	CreatedAt       time.Time `json:"createdAt"`
}

type imapMessage struct {
	UID     uint32   `json:"uid"`
	Subject string   `json:"subject"`
	From    string   `json:"from"`
	Date    string   `json:"date"`
	Flags   []string `json:"flags"`
	Snippet string   `json:"snippet"`
	Body    string   `json:"body"`
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
	Database   dbConfig   `yaml:"database"`
	Site       siteConfig `yaml:"site"`
	Port       int        `yaml:"port"`
	StaticDir  string     `yaml:"staticDir"`
	ImapSecret string     `yaml:"imapSecret"`
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

const (
	sessionCookieName = "selfecho_session"
	sessionTTL        = 7 * 24 * time.Hour
)

type ctxKey string

const userContextKey ctxKey = "user"

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
		Port:       8080,
		StaticDir:  "./static",
		ImapSecret: "",
	}
}

type server struct {
	db        *sql.DB
	cache     *listCache
	startedAt time.Time
	imapKey   []byte
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

func Run() error {
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
		return err
	}
	db, err := ensureDB(context.Background(), cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()

	router := gin.Default()
	router.SetTrustedProxies(nil)
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

	secret := cfg.ImapSecret
	if env := os.Getenv("IMAP_SECRET"); env != "" {
		secret = env
	}

	s := &server{db: db, cache: newListCache(30 * time.Second), startedAt: time.Now(), imapKey: deriveKey(secret)}

	if err := s.ensureAuthSchema(context.Background()); err != nil {
		return err
	}
	if err := s.ensureInitialAdmin(context.Background()); err != nil {
		return err
	}
	if err := s.ensureImapSchema(context.Background()); err != nil {
		return err
	}

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
		api.POST("/auth/login", s.login)
		api.POST("/auth/logout", s.logout)
		api.GET("/auth/me", s.me)
		api.GET("/archives", s.listArchives)
		api.GET("/categories", s.listCategories)
		api.GET("/imap/messages", s.listImapMessages)
		api.GET("/imap/accounts", s.listImapAccounts)
		api.GET("/imap/messages/:uid", s.getImapMessage)

		protected := api.Group("/")
		protected.Use(s.requireAuthMiddleware())
		protected.POST("/articles", s.createArticle)
		protected.PUT("/articles/:id", s.updateArticle)
		protected.DELETE("/articles/:id", s.deleteArticle)
		protected.POST("/archives", s.createArchive)
		protected.PUT("/archives/:id", s.updateArchive)
		protected.DELETE("/archives/:id", s.deleteArchive)
		protected.POST("/imap/accounts", s.createImapAccount)
	}

	if err := s.backfillBodyHTML(context.Background()); err != nil {
		fmt.Printf("warn: backfill body_html failed: %v\n", err)
	}

	serveSPA(router, cfg.StaticDir)

	if err := router.Run(fmt.Sprintf(":%d", cfg.Port)); err != nil {
		return err
	}
	return nil
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

func (s *server) ensureAuthSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE EXTENSION IF NOT EXISTS pgcrypto;
		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE IF NOT EXISTS sessions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
		CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
	`)
	return err
}

func deriveKey(secret string) []byte {
	if secret == "" {
		secret = "selfecho-imap-secret"
		fmt.Println("warn: imapSecret/IMAP_SECRET 未设置，使用默认密钥，请在生产环境配置")
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func encryptSecret(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func decryptSecret(key []byte, cipherText string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, data := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func (s *server) createUser(ctx context.Context, username, password, role string) error {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return errors.New("用户名和密码不能为空")
	}
	if role == "" {
		role = "admin"
	}
	pwHash, err := hashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) ON CONFLICT (username) DO NOTHING`, username, pwHash, role)
	return err
}

func (s *server) ensureInitialAdmin(ctx context.Context) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	user := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	pass := os.Getenv("ADMIN_PASSWORD")
	if user == "" || pass == "" {
		fmt.Println("warn: 未检测到用户，且未设置 ADMIN_USERNAME/ADMIN_PASSWORD，后台登录不可用")
		return nil
	}
	fmt.Println("info: 创建初始管理员用户")
	return s.createUser(ctx, user, pass, "admin")
}

func (s *server) ensureImapSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS imap_accounts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			host TEXT NOT NULL,
			port INT NOT NULL DEFAULT 993,
			username TEXT NOT NULL,
			password TEXT NOT NULL,
			use_ssl BOOLEAN NOT NULL DEFAULT TRUE,
			use_starttls BOOLEAN NOT NULL DEFAULT FALSE,
			last_uid BIGINT NOT NULL DEFAULT 0,
			last_uidvalidity BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_imap_accounts_host ON imap_accounts(host);
		ALTER TABLE imap_accounts ADD COLUMN IF NOT EXISTS use_starttls BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE imap_accounts ADD COLUMN IF NOT EXISTS last_uid BIGINT NOT NULL DEFAULT 0;
		ALTER TABLE imap_accounts ADD COLUMN IF NOT EXISTS last_uidvalidity BIGINT NOT NULL DEFAULT 0;

		CREATE TABLE IF NOT EXISTS imap_messages (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id UUID NOT NULL REFERENCES imap_accounts(id) ON DELETE CASCADE,
			uid BIGINT NOT NULL,
			uidvalidity BIGINT NOT NULL,
			subject TEXT,
			from_addr TEXT,
			msg_date TIMESTAMPTZ,
			flags TEXT,
			body_html TEXT,
			body_plain TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(account_id, uid, uidvalidity)
		);
		CREATE INDEX IF NOT EXISTS idx_imap_messages_acc_date ON imap_messages(account_id, msg_date DESC);
	`)
	return err
}

type sessionWithUser struct {
	SessionID string
	User      user
	Expires   time.Time
}

func (s *server) loadSession(ctx context.Context, sessionID string) (*sessionWithUser, error) {
	var swu sessionWithUser
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.expires_at, u.id, u.username, u.password_hash, u.role, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1`, sessionID).
		Scan(&swu.SessionID, &swu.Expires, &swu.User.ID, &swu.User.Username, &swu.User.PasswordHash, &swu.User.Role, &swu.User.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &swu, nil
}

func (s *server) createSession(ctx context.Context, userID string) (*sessionWithUser, error) {
	var swu sessionWithUser
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO sessions (user_id, expires_at)
		VALUES ($1, now() + ($2::int * interval '1 second'))
		RETURNING id, expires_at`, userID, int(sessionTTL.Seconds())).
		Scan(&swu.SessionID, &swu.Expires)
	if err != nil {
		return nil, err
	}
	// load user
	err = s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, role, created_at FROM users WHERE id=$1`, userID).
		Scan(&swu.User.ID, &swu.User.Username, &swu.User.PasswordHash, &swu.User.Role, &swu.User.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &swu, nil
}

func (s *server) deleteSession(ctx context.Context, sessionID string) {
	s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=$1`, sessionID)
}

func (s *server) setSessionCookie(c *gin.Context, sessionID string, expires time.Time) {
	secure := c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *server) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *server) ensureUser(c *gin.Context) (*user, bool) {
	if v, ok := c.Get(string(userContextKey)); ok {
		if u, ok2 := v.(user); ok2 {
			return &u, true
		}
	}
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || cookie == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return nil, false
	}
	swu, err := s.loadSession(c.Request.Context(), cookie)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return nil, false
	}
	if time.Now().After(swu.Expires) {
		s.deleteSession(c.Request.Context(), swu.SessionID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "会话已过期"})
		return nil, false
	}
	c.Set(string(userContextKey), swu.User)
	return &swu.User, true
}

func (s *server) requireAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := s.ensureUser(c); !ok {
			c.Abort()
			return
		}
		c.Next()
	}
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

	// 未指定 status 或请求非 published 的数据时，需要鉴权
	if statusFilter == "" || statusFilter != "published" {
		if _, ok := s.ensureUser(c); !ok {
			return
		}
	}

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

func (s *server) login(c *gin.Context) {
	ctx := c.Request.Context()
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	if payload.Username == "" || payload.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名和密码不能为空"})
		return
	}

	var u user
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, role, created_at FROM users WHERE username=$1`, payload.Username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(payload.Password)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	swu, err := s.createSession(ctx, u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建会话失败"})
		return
	}
	s.setSessionCookie(c, swu.SessionID, swu.Expires)
	c.JSON(http.StatusOK, gin.H{"username": swu.User.Username, "role": swu.User.Role})
}

func (s *server) logout(c *gin.Context) {
	ctx := c.Request.Context()
	cookie, err := c.Cookie(sessionCookieName)
	if err == nil && cookie != "" {
		s.deleteSession(ctx, cookie)
	}
	s.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

func (s *server) me(c *gin.Context) {
	u, ok := s.ensureUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"username": u.Username,
		"role":     u.Role,
	})
}

func (s *server) listImapAccounts(c *gin.Context) {
	rows, err := s.db.Query(`SELECT id, host, port, username, use_ssl, use_starttls, last_uid, last_uidvalidity, created_at FROM imap_accounts ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 IMAP 账号失败"})
		return
	}
	defer rows.Close()
	var items []imapAccount
	for rows.Next() {
		var a imapAccount
		if err := rows.Scan(&a.ID, &a.Host, &a.Port, &a.Username, &a.UseSSL, &a.UseStartTLS, &a.LastUID, &a.LastUIDValidity, &a.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "解析 IMAP 账号失败"})
			return
		}
		items = append(items, a)
	}
	c.JSON(http.StatusOK, items)
}

func (s *server) createImapAccount(c *gin.Context) {
	var payload struct {
		Host        string `json:"host"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		UseSSL      bool   `json:"useSsl"`
		UseStartTLS bool   `json:"useStartTls"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	payload.Host = strings.TrimSpace(payload.Host)
	payload.Username = strings.TrimSpace(payload.Username)
	if payload.Port == 0 {
		payload.Port = 993
	}
	if payload.Host == "" || payload.Username == "" || payload.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "地址、用户名、密码不能为空"})
		return
	}

	secret := payload.Password
	if s.imapKey != nil {
		enc, err := encryptSecret(s.imapKey, payload.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("加密密码失败: %v", err)})
			return
		}
		secret = enc
	}

	_, err := s.db.Exec(
		`INSERT INTO imap_accounts (host, port, username, password, use_ssl, use_starttls) VALUES ($1, $2, $3, $4, $5, $6)`,
		payload.Host, payload.Port, payload.Username, secret, payload.UseSSL, payload.UseStartTLS,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("保存 IMAP 账号失败: %v", err)})
		return
	}
	c.Status(http.StatusCreated)
}

func (s *server) listImapMessages(c *gin.Context) {
	ctx := c.Request.Context()
	accountID := strings.TrimSpace(c.Query("accountId"))
	limit := 12
	if l, err := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(strings.TrimSpace(c.Query("page"))); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * limit

	acc, err := s.pickImapAccount(ctx, accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if acc == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未找到 IMAP 账号，请先创建"})
		return
	}

	msgs, err := s.readCachedMessages(ctx, acc.ID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("读取邮件失败: %v", err)})
		return
	}
	total, _ := s.countCachedMessages(ctx, acc.ID)
	if len(msgs) > 0 {
		c.Header("X-Total-Count", strconv.Itoa(total))
		s.syncImapAccountAsync(*acc, 50)
		c.JSON(http.StatusOK, msgs)
		return
	}

	if err := s.syncImapAccount(ctx, acc, 50); err != nil {
		fmt.Printf("warn: 同步 IMAP 失败: %v\n", err)
	}

	msgs, err = s.readCachedMessages(ctx, acc.ID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("读取邮件失败: %v", err)})
		return
	}
	total, _ = s.countCachedMessages(ctx, acc.ID)
	if len(msgs) == 0 {
		// fallback 直接拉取
		if fresh, ferr := fetchImapMessages(ctx, *acc, limit); ferr == nil {
			c.Header("X-Total-Count", strconv.Itoa(len(fresh)))
			c.JSON(http.StatusOK, fresh)
			return
		}
	}
	c.Header("X-Total-Count", strconv.Itoa(total))
	c.JSON(http.StatusOK, msgs)
}

func (s *server) pickImapAccount(ctx context.Context, id string) (*imapAccount, error) {
	var row *sql.Row
	if id != "" {
		row = s.db.QueryRowContext(ctx, `SELECT id, host, port, username, password, use_ssl, use_starttls, last_uid, last_uidvalidity, created_at FROM imap_accounts WHERE id=$1`, id)
	} else {
		row = s.db.QueryRowContext(ctx, `SELECT id, host, port, username, password, use_ssl, use_starttls, last_uid, last_uidvalidity, created_at FROM imap_accounts ORDER BY created_at DESC LIMIT 1`)
	}
	var acc imapAccount
	if err := row.Scan(&acc.ID, &acc.Host, &acc.Port, &acc.Username, &acc.Password, &acc.UseSSL, &acc.UseStartTLS, &acc.LastUID, &acc.LastUIDValidity, &acc.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if s.imapKey != nil && acc.Password != "" {
		if dec, err := decryptSecret(s.imapKey, acc.Password); err == nil {
			acc.Password = dec
		}
	}
	return &acc, nil
}

func fetchImapMessages(ctx context.Context, acc imapAccount, limit int) ([]imapMessage, error) {
	address := fmt.Sprintf("%s:%d", acc.Host, acc.Port)
	var c *client.Client
	var err error
	if acc.UseSSL {
		c, err = client.DialTLS(address, nil)
	} else {
		c, err = client.Dial(address)
	}
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	if !acc.UseSSL && acc.UseStartTLS {
		if err := c.StartTLS(nil); err != nil {
			return nil, err
		}
	}

	if err := c.Login(acc.Username, acc.Password); err != nil {
		return nil, err
	}

	mbox, err := c.Select("INBOX", true)
	if err != nil {
		return nil, err
	}
	if mbox.Messages == 0 {
		return []imapMessage{}, nil
	}

	var from uint32 = 1
	if mbox.Messages > uint32(limit) {
		from = mbox.Messages - uint32(limit) + 1
	}
	set := new(imap.SeqSet)
	set.AddRange(from, mbox.Messages)

	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid}
	messages := make(chan *imap.Message, limit)
	if err := c.Fetch(set, items, messages); err != nil {
		return nil, err
	}

	var result []imapMessage
	for msg := range messages {
		var fromAddr string
		if msg.Envelope != nil && len(msg.Envelope.From) > 0 {
			fromAddr = msg.Envelope.From[0].Address()
		}
		date := ""
		if msg.Envelope != nil {
			date = msg.Envelope.Date.Format(time.RFC3339)
		}
		result = append([]imapMessage{
			{
				UID:     msg.Uid,
				Subject: msg.Envelope.Subject,
				From:    fromAddr,
				Date:    date,
				Flags:   msg.Flags,
				Snippet: "",
			},
		}, result...)
	}
	// reverse already prepended, so order newest-first
	return result, nil
}

func (s *server) getImapMessage(c *gin.Context) {
	ctx := c.Request.Context()
	accountID := strings.TrimSpace(c.Query("accountId"))
	uidStr := c.Param("uid")
	uid64, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uid 非法"})
		return
	}

	acc, err := s.pickImapAccount(ctx, accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if acc == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未找到 IMAP 账号，请先创建"})
		return
	}

	msg, err := s.readCachedMessage(ctx, acc.ID, uint32(uid64))
	if err == nil {
		s.syncImapAccountAsync(*acc, 20)
		c.JSON(http.StatusOK, msg)
		return
	}

	lastErr := err

	if err := s.syncImapAccount(ctx, acc, 20); err != nil {
		fmt.Printf("warn: 同步 IMAP 失败: %v\n", err)
		lastErr = err
	}

	msg, err = s.readCachedMessage(ctx, acc.ID, uint32(uid64))
	if err == nil {
		c.JSON(http.StatusOK, msg)
		return
	}
	if err != nil {
		lastErr = err
	}

	if direct, derr := fetchImapMessageDetail(ctx, *acc, uint32(uid64)); derr == nil {
		c.JSON(http.StatusOK, direct)
		return
	} else {
		lastErr = derr
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("加载邮件失败: %v", lastErr)})
}

func fetchImapMessageDetail(ctx context.Context, acc imapAccount, uid uint32) (imapMessage, error) {
	address := fmt.Sprintf("%s:%d", acc.Host, acc.Port)
	var c *client.Client
	var err error
	if acc.UseSSL {
		c, err = client.DialTLS(address, nil)
	} else {
		c, err = client.Dial(address)
	}
	if err != nil {
		return imapMessage{}, err
	}
	defer c.Logout()

	if !acc.UseSSL && acc.UseStartTLS {
		if err := c.StartTLS(nil); err != nil {
			return imapMessage{}, err
		}
	}
	if err := c.Login(acc.Username, acc.Password); err != nil {
		return imapMessage{}, err
	}
	if _, err := c.Select("INBOX", true); err != nil {
		return imapMessage{}, err
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid, section.FetchItem()}
	ch := make(chan *imap.Message, 1)
	if err := c.Fetch(seqset, items, ch); err != nil {
		return imapMessage{}, err
	}
	msg, ok := <-ch
	if !ok || msg == nil || msg.Envelope == nil {
		return imapMessage{}, errors.New("邮件不存在")
	}

	body, _ := parseBody(msg.GetBody(section))

	var fromAddr string
	if len(msg.Envelope.From) > 0 {
		fromAddr = safeUTF8(msg.Envelope.From[0].Address())
	}
	date := msg.Envelope.Date.Format(time.RFC3339)

	return imapMessage{
		UID:     msg.Uid,
		Subject: safeUTF8(msg.Envelope.Subject),
		From:    fromAddr,
		Date:    date,
		Flags:   msg.Flags,
		Snippet: "",
		Body:    safeUTF8(body),
	}, nil
}

func parseBody(body io.Reader) (string, error) {
	if body == nil {
		return "", nil
	}
	mr, err := mail.CreateReader(body)
	if err != nil {
		b, _ := io.ReadAll(body)
		return escapeText(string(b)), nil
	}
	var htmlBody string
	var textBody string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if ih, ok := p.Header.(*mail.InlineHeader); ok {
			mt, _, _ := ih.ContentType()
			data, _ := decodePart(ih, p.Body)
			if strings.HasPrefix(mt, "text/html") && len(data) > 0 {
				htmlBody = safeUTF8(string(data))
			} else if strings.HasPrefix(mt, "text/plain") && textBody == "" {
				textBody = safeUTF8(string(data))
			}
		}
	}
	if htmlBody != "" {
		return htmlBody, nil
	}
	if textBody != "" {
		return escapeText(textBody), nil
	}
	return "", nil
}

func escapeText(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

func decodePart(ih *mail.InlineHeader, r io.Reader) ([]byte, error) {
	if ih == nil {
		return io.ReadAll(r)
	}
	cte := ih.Header.Get("Content-Transfer-Encoding")
	switch strings.ToLower(cte) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	return io.ReadAll(r)
}

func (s *server) syncImapAccountAsync(acc imapAccount, limit int) {
	go func(a imapAccount) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.syncImapAccount(ctx, &a, limit); err != nil {
			fmt.Printf("warn: 同步 IMAP 失败: %v\n", err)
		}
	}(acc)
}

func (s *server) syncImapAccount(ctx context.Context, acc *imapAccount, limit int) error {
	address := fmt.Sprintf("%s:%d", acc.Host, acc.Port)
	var c *client.Client
	var err error
	if acc.UseSSL {
		c, err = client.DialTLS(address, nil)
	} else {
		c, err = client.Dial(address)
	}
	if err != nil {
		return err
	}
	defer c.Logout()

	if !acc.UseSSL && acc.UseStartTLS {
		if err := c.StartTLS(nil); err != nil {
			return err
		}
	}
	if err := c.Login(acc.Username, acc.Password); err != nil {
		return err
	}

	mbox, err := c.Select("INBOX", true)
	if err != nil {
		return err
	}
	if mbox.Messages == 0 {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM imap_messages WHERE account_id=$1`, acc.ID)
		_, _ = s.db.ExecContext(ctx, `UPDATE imap_accounts SET last_uid=$1, last_uidvalidity=$2 WHERE id=$3`, 0, mbox.UidValidity, acc.ID)
		acc.LastUID = 0
		acc.LastUIDValidity = mbox.UidValidity
		return nil
	}

	from := uint32(1)
	if mbox.Messages > uint32(limit) {
		from = mbox.Messages - uint32(limit) + 1
	}
	set := new(imap.SeqSet)
	set.AddRange(from, mbox.Messages)

	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid}
	messages := make(chan *imap.Message, limit)
	if err := c.Fetch(set, items, messages); err != nil {
		return err
	}

	type row struct {
		uid uint32
		msg *imap.Message
	}
	var fetched []row
	for msg := range messages {
		if msg == nil || msg.Envelope == nil {
			continue
		}
		fetched = append(fetched, row{uid: msg.Uid, msg: msg})
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// reset on uidvalidity change (except initial 0)
	if acc.LastUIDValidity != 0 && acc.LastUIDValidity != mbox.UidValidity {
		if _, err := tx.ExecContext(ctx, `DELETE FROM imap_messages WHERE account_id=$1`, acc.ID); err != nil {
			return err
		}
		acc.LastUID = 0
	}

	var maxUID uint32 = acc.LastUID
	for i := len(fetched) - 1; i >= 0; i-- { // ensure ascending insert
		msg := fetched[i].msg
		uid := fetched[i].uid
		if uid <= acc.LastUID {
			continue
		}
		if uid > maxUID {
			maxUID = uid
		}
		detail, err := fetchImapMessageDetail(ctx, *acc, uid)
		if err != nil {
			continue
		}
		var msgTime *time.Time
		if detail.Date != "" {
			if t, err := time.Parse(time.RFC3339, detail.Date); err == nil {
				msgTime = &t
			}
		}
		flags := strings.Join(detail.Flags, " ")
		if msgTime == nil && msg.Envelope != nil {
			t := msg.Envelope.Date
			msgTime = &t
		}
		subj := safeUTF8(detail.Subject)
		from := safeUTF8(detail.From)
		body := safeUTF8(detail.Body)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO imap_messages (account_id, uid, uidvalidity, subject, from_addr, msg_date, flags, body_html, body_plain)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (account_id, uid, uidvalidity) DO UPDATE
			SET subject=EXCLUDED.subject, from_addr=EXCLUDED.from_addr, msg_date=EXCLUDED.msg_date,
			    flags=EXCLUDED.flags, body_html=EXCLUDED.body_html, body_plain=EXCLUDED.body_plain
		`, acc.ID, uid, mbox.UidValidity, subj, from, msgTime, flags, body, "")
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE imap_accounts SET last_uid=$1, last_uidvalidity=$2 WHERE id=$3`, maxUID, mbox.UidValidity, acc.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	acc.LastUID = maxUID
	acc.LastUIDValidity = mbox.UidValidity
	return nil
}

func (s *server) readCachedMessages(ctx context.Context, accountID string, limit, offset int) ([]imapMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT uid, subject, from_addr, msg_date, flags, body_html, body_plain
		FROM imap_messages
		WHERE account_id=$1
		ORDER BY msg_date DESC NULLS LAST
		LIMIT $2 OFFSET $3`, accountID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []imapMessage
	for rows.Next() {
		var m imapMessage
		var flags string
		var msgDate sql.NullTime
		var bodyHTML, bodyPlain sql.NullString
		if err := rows.Scan(&m.UID, &m.Subject, &m.From, &msgDate, &flags, &bodyHTML, &bodyPlain); err != nil {
			return nil, err
		}
		if msgDate.Valid {
			m.Date = msgDate.Time.Format(time.RFC3339)
		}
		if flags != "" {
			m.Flags = strings.Fields(flags)
		}
		if bodyHTML.Valid && bodyHTML.String != "" {
			m.Body = bodyHTML.String
		} else if bodyPlain.Valid && bodyPlain.String != "" {
			m.Body = escapeText(bodyPlain.String)
		}
		res = append(res, m)
	}
	return res, nil
}

func (s *server) countCachedMessages(ctx context.Context, accountID string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM imap_messages WHERE account_id=$1`, accountID).Scan(&total)
	return total, err
}

func (s *server) readCachedMessage(ctx context.Context, accountID string, uid uint32) (imapMessage, error) {
	var m imapMessage
	var flags string
	var msgDate sql.NullTime
	var bodyHTML, bodyPlain sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT uid, subject, from_addr, msg_date, flags, body_html, body_plain
		FROM imap_messages
		WHERE account_id=$1 AND uid=$2
	`, accountID, uid).Scan(&m.UID, &m.Subject, &m.From, &msgDate, &flags, &bodyHTML, &bodyPlain)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return m, errors.New("未找到邮件")
		}
		return m, err
	}
	if msgDate.Valid {
		m.Date = msgDate.Time.Format(time.RFC3339)
	}
	if flags != "" {
		m.Flags = strings.Fields(flags)
	}
	if bodyHTML.Valid && bodyHTML.String != "" {
		m.Body = bodyHTML.String
	} else if bodyPlain.Valid && bodyPlain.String != "" {
		m.Body = escapeText(bodyPlain.String)
	}
	return m, nil
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
