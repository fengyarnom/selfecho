INSERT INTO archives (name, description)
VALUES
    ('default', 'Default archive bucket'),
    ('tech', 'Technology articles')
ON CONFLICT (name) DO NOTHING;

INSERT INTO articles (slug, title, body_md, status, archive_id, published_at)
SELECT
    'hello-world',
    'Hello World',
    '# Hello World\n\nSample article body in markdown.',
    'published',
    a.id,
    now()
FROM archives a
WHERE a.name = 'default'
ON CONFLICT (slug) DO NOTHING;
