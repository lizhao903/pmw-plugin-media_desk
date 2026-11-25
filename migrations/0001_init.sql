-- 内容工作台私有库（0.1.0）。数据模型参考 dsh-media-desk 的 state.json，拆成关系表。

-- 灵感 / 内容：同一条记录从一句灵感一路走到已发布
CREATE TABLE IF NOT EXISTS ideas (
  id            TEXT PRIMARY KEY,
  title         TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL DEFAULT '',
  url           TEXT NOT NULL DEFAULT '',
  source        TEXT NOT NULL DEFAULT '',
  tags          TEXT NOT NULL DEFAULT '[]',
  status        TEXT NOT NULL DEFAULT 'idea',
  platform      TEXT NOT NULL DEFAULT '',
  scheduled_at  TEXT NOT NULL DEFAULT '',
  published_at  TEXT NOT NULL DEFAULT '',
  pinned        INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ideas_created ON ideas(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ideas_scheduled ON ideas(scheduled_at);

-- 成稿：每条灵感最多一份（标题、封面大字、脚本、策划要点）
CREATE TABLE IF NOT EXISTS productions (
  idea_id      TEXT PRIMARY KEY,
  status       TEXT NOT NULL DEFAULT 'idle',
  stage        TEXT NOT NULL DEFAULT '',
  format       TEXT NOT NULL DEFAULT 'script',
  with_images  INTEGER NOT NULL DEFAULT 0,
  title        TEXT NOT NULL DEFAULT '',
  cover_text   TEXT NOT NULL DEFAULT '',
  article      TEXT NOT NULL DEFAULT '',
  ai_article   TEXT NOT NULL DEFAULT '',
  facts        TEXT NOT NULL DEFAULT '[]',
  outline      TEXT NOT NULL DEFAULT '[]',
  characters   TEXT NOT NULL DEFAULT '',
  style_name   TEXT NOT NULL DEFAULT '',
  notice       TEXT NOT NULL DEFAULT '',
  error        TEXT NOT NULL DEFAULT '',
  started_at   TEXT NOT NULL DEFAULT '',
  finished_at  TEXT NOT NULL DEFAULT '',
  updated_at   TEXT NOT NULL DEFAULT ''
);

-- 画面：封面（cover，no=0）、配图（image）、分镜（shot），no 从 1 起
CREATE TABLE IF NOT EXISTS assets (
  id         TEXT PRIMARY KEY,
  idea_id    TEXT NOT NULL,
  kind       TEXT NOT NULL,
  no         INTEGER NOT NULL DEFAULT 0,
  caption    TEXT NOT NULL DEFAULT '',
  anchor     TEXT NOT NULL DEFAULT '',
  shot       TEXT NOT NULL DEFAULT '',
  visual     TEXT NOT NULL DEFAULT '',
  voice      TEXT NOT NULL DEFAULT '',
  seconds    INTEGER NOT NULL DEFAULT 0,
  prompt     TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'idle',
  error      TEXT NOT NULL DEFAULT '',
  image_id   TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_slot ON assets(idea_id, kind, no);

-- 生成的图片：base64 文本存在私有库（宿主 store 参数只收字符串），单张解码后不超过 4 MB
CREATE TABLE IF NOT EXISTS images (
  id         TEXT PRIMARY KEY,
  idea_id    TEXT NOT NULL,
  mime       TEXT NOT NULL,
  bytes      INTEGER NOT NULL DEFAULT 0,
  data_b64   TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_images_idea ON images(idea_id);

-- 工作区左侧对话
CREATE TABLE IF NOT EXISTS chats (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  idea_id    TEXT NOT NULL,
  role       TEXT NOT NULL,
  text       TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chats_idea ON chats(idea_id, id);

-- 撤销：每次改稿前存一份成稿和画面文字的快照
CREATE TABLE IF NOT EXISTS revisions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  idea_id    TEXT NOT NULL,
  label      TEXT NOT NULL,
  snapshot   TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_revisions_idea ON revisions(idea_id, id);

-- 复盘数据：发布后手动回填
CREATE TABLE IF NOT EXISTS metrics (
  id         TEXT PRIMARY KEY,
  idea_id    TEXT NOT NULL DEFAULT '',
  platform   TEXT NOT NULL,
  date       TEXT NOT NULL,
  views      INTEGER NOT NULL DEFAULT 0,
  likes      INTEGER NOT NULL DEFAULT 0,
  comments   INTEGER NOT NULL DEFAULT 0,
  shares     INTEGER NOT NULL DEFAULT 0,
  saves      INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_metrics_date ON metrics(date);

-- 写作风格卡
CREATE TABLE IF NOT EXISTS styles (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  category   TEXT NOT NULL DEFAULT '',
  preset     INTEGER NOT NULL DEFAULT 0,
  summary    TEXT NOT NULL DEFAULT '',
  tone       TEXT NOT NULL DEFAULT '',
  sentences  TEXT NOT NULL DEFAULT '',
  opening    TEXT NOT NULL DEFAULT '',
  structure  TEXT NOT NULL DEFAULT '',
  logic      TEXT NOT NULL DEFAULT '',
  story      TEXT NOT NULL DEFAULT '',
  flow       TEXT NOT NULL DEFAULT '',
  words      TEXT NOT NULL DEFAULT '[]',
  avoid      TEXT NOT NULL DEFAULT '[]',
  samples    TEXT NOT NULL DEFAULT '[]',
  reference  TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- 写作规则：每种风格都遵守
CREATE TABLE IF NOT EXISTS rules (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  rule       TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'active',
  source     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

-- 杂项设置（当前风格、默认体裁、生图配置等，不含密钥）
CREATE TABLE IF NOT EXISTS kv (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
