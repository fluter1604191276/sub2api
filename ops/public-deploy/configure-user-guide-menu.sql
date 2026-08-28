INSERT INTO settings (key, value, updated_at)
VALUES (
  'custom_menu_items',
  $$[
    {
      "id": "fluterapi-guide",
      "label": "充值与教程",
      "icon_svg": "",
      "url": "md:fluterapi-guide",
      "page_slug": "fluterapi-guide",
      "visibility": "user",
      "sort_order": 0
    }
  ]$$,
  now()
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = now();

SELECT key, value
FROM settings
WHERE key = 'custom_menu_items';
