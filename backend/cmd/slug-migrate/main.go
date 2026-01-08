package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"selfecho/backend/internal/slugmigrate"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
)

type config struct {
	Database dbConfig       `yaml:"database"`
	Deepseek deepseekConfig `yaml:"deepseek"`
}

type dbConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslmode"`
}

type deepseekConfig struct {
	APIKey  string `yaml:"apiKey"`
	BaseURL string `yaml:"baseUrl"`
	Model   string `yaml:"model"`
}

type postRow struct {
	ID    string
	Title string
	Slug  string
}

type mapping struct {
	ID      string
	Title   string
	OldSlug string
	NewSlug string
}

func main() {
	var (
		configPath      string
		statusFilter    string
		limit           int
		apply           bool
		outPath         string
		requestTimeout  time.Duration
		sleepBetween    time.Duration
		continueOnError bool
		skipIfUnchanged bool
	)

	flag.StringVar(&configPath, "config", "", "config.yaml path (or use CONFIG_PATH)")
	flag.StringVar(&statusFilter, "status", "", "filter by status (draft/published), empty means all")
	flag.IntVar(&limit, "limit", 0, "max posts to process, 0 means all")
	flag.BoolVar(&apply, "apply", false, "apply updates to DB (default: dry-run)")
	flag.StringVar(&outPath, "out", "", "write mapping CSV to path (default: stdout)")
	flag.DurationVar(&requestTimeout, "timeout", 20*time.Second, "per-request timeout to DeepSeek")
	flag.DurationVar(&sleepBetween, "sleep", 0, "sleep duration between DeepSeek calls (e.g. 200ms)")
	flag.BoolVar(&continueOnError, "continue-on-error", false, "continue when a DeepSeek call fails")
	flag.BoolVar(&skipIfUnchanged, "skip-unchanged", true, "skip updates when new slug equals old slug")
	flag.Parse()

	ctx := context.Background()

	statusFilter = strings.TrimSpace(statusFilter)
	if statusFilter != "" && statusFilter != "draft" && statusFilter != "published" {
		fatal(fmt.Errorf("--status must be draft or published"))
	}

	cfgPath, err := resolveConfigPath(configPath)
	if err != nil {
		fatal(err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fatal(err)
	}
	if env := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")); env != "" {
		cfg.Deepseek.APIKey = env
	}
	if cfg.Deepseek.APIKey == "" {
		fatal(fmt.Errorf("missing DeepSeek API key: set deepseek.apiKey in config or DEEPSEEK_API_KEY"))
	}

	db, err := openDB(ctx, cfg.Database)
	if err != nil {
		fatal(err)
	}
	defer db.Close()

	hasType, err := hasColumn(ctx, db, "articles", "type")
	if err != nil {
		fatal(err)
	}

	used, err := fetchAllSlugs(ctx, db)
	if err != nil {
		fatal(err)
	}

	posts, err := fetchPosts(ctx, db, hasType, statusFilter, limit)
	if err != nil {
		fatal(err)
	}
	if len(posts) == 0 {
		fmt.Println("no posts matched")
		return
	}

	client := &deepseekClient{
		baseURL: strings.TrimSuffix(strings.TrimSpace(cfg.Deepseek.BaseURL), "/"),
		model:   strings.TrimSpace(cfg.Deepseek.Model),
		apiKey:  cfg.Deepseek.APIKey,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}
	if client.baseURL == "" {
		client.baseURL = "https://api.deepseek.com"
	}
	if client.model == "" {
		client.model = "deepseek-chat"
	}

	var mappings []mapping
	var updated int
	var skipped int
	var failures int

	for i, p := range posts {
		newSlug, err := client.generateSlug(ctx, p.Title)
		if err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "fail %d/%d id=%s title=%q: %v\n", i+1, len(posts), p.ID, p.Title, err)
			if !continueOnError {
				break
			}
			continue
		}

		newSlug = slugmigrate.EnsureUniqueSlug(newSlug, p.ID, used)
		if newSlug == "" {
			failures++
			fmt.Fprintf(os.Stderr, "fail %d/%d id=%s title=%q: empty slug\n", i+1, len(posts), p.ID, p.Title)
			if !continueOnError {
				break
			}
			continue
		}

		if skipIfUnchanged && newSlug == p.Slug {
			skipped++
			continue
		}

		mappings = append(mappings, mapping{
			ID:      p.ID,
			Title:   p.Title,
			OldSlug: p.Slug,
			NewSlug: newSlug,
		})

		slugmigrate.ApplySlugChange(p.ID, p.Slug, newSlug, used)

		if apply {
			if err := updateSlug(ctx, db, p.ID, newSlug); err != nil {
				failures++
				fmt.Fprintf(os.Stderr, "fail update %d/%d id=%s: %v\n", i+1, len(posts), p.ID, err)
				if !continueOnError {
					break
				}
				continue
			}
			updated++
		}

		if sleepBetween > 0 {
			time.Sleep(sleepBetween)
		}
	}

	if err := writeMappingCSV(outPath, mappings); err != nil {
		fatal(err)
	}

	summaryOut := io.Writer(os.Stdout)
	if strings.TrimSpace(outPath) == "" {
		summaryOut = os.Stderr
	}
	if apply {
		fmt.Fprintf(summaryOut, "done: updated=%d skipped=%d failed=%d\n", updated, skipped, failures)
	} else {
		fmt.Fprintf(summaryOut, "dry-run: would-update=%d skipped=%d failed=%d (use --apply to write DB)\n", len(mappings), skipped, failures)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

func resolveConfigPath(flagPath string) (string, error) {
	if strings.TrimSpace(flagPath) != "" {
		return flagPath, nil
	}
	if env := strings.TrimSpace(os.Getenv("CONFIG_PATH")); env != "" {
		return env, nil
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml", nil
	}
	if _, err := os.Stat(filepath.Join("..", "config.yaml")); err == nil {
		return filepath.Join("..", "config.yaml"), nil
	}
	return "", fmt.Errorf("config.yaml not found (use --config or CONFIG_PATH)")
}

func loadConfig(path string) (config, error) {
	var cfg config
	bytes, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(bytes, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func openDB(ctx context.Context, cfg dbConfig) (*sql.DB, error) {
	sslmode := cfg.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, sslmode)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return db, nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	return exists, err
}

func fetchAllSlugs(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, slug FROM articles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	used := make(map[string]string)
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, err
		}
		used[slug] = id
	}
	return used, nil
}

func fetchPosts(ctx context.Context, db *sql.DB, hasType bool, status string, limit int) ([]postRow, error) {
	var where []string
	var args []any
	argPos := 1

	if hasType {
		where = append(where, fmt.Sprintf("type = $%d", argPos))
		args = append(args, "post")
		argPos++
	}

	status = strings.TrimSpace(status)
	if status != "" {
		where = append(where, fmt.Sprintf("status = $%d", argPos))
		args = append(args, status)
		argPos++
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	limitSQL := ""
	if limit > 0 {
		limitSQL = fmt.Sprintf("LIMIT $%d", argPos)
		args = append(args, limit)
	}

	query := fmt.Sprintf(`
		SELECT id, title, slug
		FROM articles
		%s
		ORDER BY created_at ASC
		%s`, whereSQL, limitSQL)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []postRow
	for rows.Next() {
		var p postRow
		if err := rows.Scan(&p.ID, &p.Title, &p.Slug); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

func updateSlug(ctx context.Context, db *sql.DB, id, slug string) error {
	_, err := db.ExecContext(ctx, `UPDATE articles SET slug=$1, updated_at=now() WHERE id=$2`, slug, id)
	return err
}

func writeMappingCSV(outPath string, items []mapping) error {
	var w io.Writer = os.Stdout
	var file *os.File
	if strings.TrimSpace(outPath) != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		file = f
		defer file.Close()
		w = f
	}

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"id", "title", "old_slug", "new_slug"}); err != nil {
		return err
	}
	for _, it := range items {
		if err := cw.Write([]string{it.ID, it.Title, it.OldSlug, it.NewSlug}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

type deepseekClient struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

func (c *deepseekClient) generateSlug(ctx context.Context, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("empty title")
	}

	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "将我下面给你的中文标题转换为SEO友好的英文slug格式。输出要求：全小写、用连字符连接、简洁明了。仅输出slug本身。",
			},
			{
				"role":    "user",
				"content": title,
			},
		},
		"stream": false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("deepseek http %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("deepseek returned empty choices")
	}

	out := slugmigrate.NormalizeLLMOutputToSlug(result.Choices[0].Message.Content)
	if out == "" {
		return "", fmt.Errorf("empty slug after normalization")
	}
	return out, nil
}
