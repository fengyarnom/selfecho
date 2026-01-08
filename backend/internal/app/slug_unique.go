package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ensureUniqueSlug returns baseSlug if it's free; otherwise returns baseSlug-<n>.
// It ignores the row with ignoreID (used for updates).
func (s *server) ensureUniqueSlug(ctx context.Context, baseSlug string, ignoreID string) (string, error) {
	baseSlug = strings.TrimSpace(baseSlug)
	if baseSlug == "" {
		return "", errors.New("slug 为空")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, slug
		FROM articles
		WHERE slug = $1 OR slug LIKE $2`, baseSlug, baseSlug+"-%")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	takenBase := false
	maxSuffix := 1 // base exists -> start from -2
	prefix := baseSlug + "-"

	for rows.Next() {
		var id, slugVal string
		if err := rows.Scan(&id, &slugVal); err != nil {
			return "", err
		}
		if ignoreID != "" && id == ignoreID {
			continue
		}
		if slugVal == baseSlug {
			takenBase = true
			continue
		}
		if strings.HasPrefix(slugVal, prefix) {
			suffix := strings.TrimPrefix(slugVal, prefix)
			n, err := strconv.Atoi(suffix)
			if err == nil && n > maxSuffix {
				maxSuffix = n
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	if !takenBase {
		return baseSlug, nil
	}
	return fmt.Sprintf("%s-%d", baseSlug, maxSuffix+1), nil
}
