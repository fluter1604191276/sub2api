WITH existing AS (
  SELECT COALESCE(value::jsonb, '[]'::jsonb) AS items
  FROM settings
  WHERE key = 'custom_menu_items'
),
base AS (
  SELECT COALESCE((SELECT items FROM existing), '[]'::jsonb) AS items
),
without_current AS (
  SELECT COALESCE(jsonb_agg(item), '[]'::jsonb) AS items
  FROM base
  CROSS JOIN LATERAL jsonb_array_elements(base.items) AS item
  WHERE item->>'id' <> 'available-models'
),
next_sort AS (
  SELECT COALESCE(MAX((item->>'sort_order')::int), -1) + 1 AS sort_order
  FROM without_current
  CROSS JOIN LATERAL jsonb_array_elements(without_current.items) AS item
  WHERE item ? 'sort_order'
),
merged AS (
  SELECT without_current.items || jsonb_build_array(jsonb_build_object(
    'id', 'available-models',
    'label', '可用模型',
    'icon_svg', '',
    'url', '/model-directory',
    'visibility', 'user',
    'sort_order', next_sort.sort_order
  )) AS items
  FROM without_current, next_sort
)
INSERT INTO settings (key, value, updated_at)
SELECT 'custom_menu_items', jsonb_pretty(items), now()
FROM merged
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = now();

SELECT key, value
FROM settings
WHERE key = 'custom_menu_items';
