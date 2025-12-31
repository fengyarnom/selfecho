CREATE OR REPLACE VIEW published_articles AS
SELECT
    ar.name AS archive_name,
    art.slug,
    art.title,
    art.published_at
FROM articles art
LEFT JOIN archives ar ON ar.id = art.archive_id
WHERE art.status = 'published'
ORDER BY art.published_at DESC NULLS LAST;
