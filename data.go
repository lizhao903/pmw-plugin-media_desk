// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

// 私有库读写：灵感、成稿、画面、风格、规则、设置。

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

//go:embed data/style-presets.json
var presetFS embed.FS

var errNotFound = errors.New("找不到这条灵感")

// Production 是一条灵感的成稿。
type Production struct {
	IdeaID     string   `json:"idea_id"`
	Status     string   `json:"status"`
	Stage      string   `json:"stage"`
	Format     string   `json:"format"`
	WithImages bool     `json:"with_images"`
	Title      string   `json:"title"`
	CoverText  string   `json:"cover_text"`
	Article    string   `json:"article"`
	AIArticle  string   `json:"-"`
	Facts      []string `json:"facts"`
	Outline    []string `json:"outline"`
	Characters string   `json:"characters"`
	StyleName  string   `json:"style_name"`
	Notice     string   `json:"notice"`
	Error      string   `json:"error"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
}

// Asset 是一格画面：封面、配图或分镜。
type Asset struct {
	ID      string `json:"id"`
	IdeaID  string `json:"-"`
	Kind    string `json:"kind"`
	No      int    `json:"no"`
	Caption string `json:"caption"`
	Anchor  string `json:"anchor"`
	Shot    string `json:"shot"`
	Visual  string `json:"visual"`
	Voice   string `json:"voice"`
	Seconds int    `json:"seconds"`
	Prompt  string `json:"prompt"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	ImageID string `json:"image_id"`
}

// Settings 是插件设置（不含密钥）。
type Settings struct {
	DraftFormat    string      `json:"draft_format"`
	ImageCount     int         `json:"image_count"`
	ShotCount      int         `json:"shot_count"`
	GenerateImages bool        `json:"generate_images"`
	ActiveStyleID  string      `json:"active_style_id"`
	Image          ImageConfig `json:"image"`
}

// ImageConfig 是 OpenAI 兼容图像接口的配置；Key 单独存在 host.secrets。
type ImageConfig struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	Size    string `json:"size"`
	Quality string `json:"quality"`
}

func (c ImageConfig) configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.Model) != ""
}

var statuses = []string{"idea", "outline", "draft", "ready", "published"}

// platforms 支持的平台（只用于标注和排期，不直接发布）。
var platforms = []string{"小红书", "抖音", "B站", "公众号", "视频号", "快手", "微博", "知乎", "TikTok", "YouTube", "Instagram", "X"}

func validStatus(s string) bool {
	for _, x := range statuses {
		if x == s {
			return true
		}
	}
	return false
}

func validPlatform(p string) bool {
	if p == "" {
		return true
	}
	for _, x := range platforms {
		if x == p {
			return true
		}
	}
	return false
}

func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (s *server) loadSettings(ctx context.Context) Settings {
	rows, _ := s.query(ctx, "SELECT key, value FROM kv")
	m := map[string]string{}
	for _, r := range rows {
		m[str(r, "key")] = str(r, "value")
	}
	atoi := func(k string) int { n, _ := strconv.Atoi(m[k]); return n }
	st := Settings{
		DraftFormat:    "script",
		ImageCount:     clampInt(atoi("image_count"), 0, 4, 2),
		ShotCount:      clampInt(atoi("shot_count"), 3, 12, 6),
		GenerateImages: m["generate_images"] != "0",
		ActiveStyleID:  m["active_style_id"],
		Image: ImageConfig{
			BaseURL: m["image_base_url"], Model: m["image_model"], Size: m["image_size"], Quality: m["image_quality"],
		},
	}
	if m["image_count"] == "0" {
		st.ImageCount = 0
	}
	if m["draft_format"] == "article" {
		st.DraftFormat = "article"
	}
	if st.Image.Size == "" {
		st.Image.Size = "1536x1024"
	}
	return st
}

func ideaFromRow(r map[string]any) Idea {
	return Idea{
		ID: str(r, "id"), Title: str(r, "title"), Body: str(r, "body"), URL: str(r, "url"), Source: str(r, "source"),
		Tags: jsonList(str(r, "tags")), Status: str(r, "status"), Platform: str(r, "platform"),
		ScheduledAt: str(r, "scheduled_at"), PublishedAt: str(r, "published_at"), Pinned: intOf(r, "pinned") == 1,
		CreatedAt: str(r, "created_at"), UpdatedAt: str(r, "updated_at"),
	}
}

const ideaCols = "id, title, body, url, source, tags, status, platform, scheduled_at, published_at, pinned, created_at, updated_at"

func (s *server) loadIdea(ctx context.Context, id string) (Idea, error) {
	rows, err := s.query(ctx, "SELECT "+ideaCols+" FROM ideas WHERE id = ?", id)
	if err != nil {
		return Idea{}, err
	}
	if len(rows) == 0 {
		return Idea{}, errNotFound
	}
	return ideaFromRow(rows[0]), nil
}

func prodFromRow(r map[string]any) Production {
	return Production{
		IdeaID: str(r, "idea_id"), Status: str(r, "status"), Stage: str(r, "stage"), Format: str(r, "format"),
		WithImages: intOf(r, "with_images") == 1, Title: str(r, "title"), CoverText: str(r, "cover_text"),
		Article: str(r, "article"), AIArticle: str(r, "ai_article"), Facts: jsonList(str(r, "facts")), Outline: jsonList(str(r, "outline")),
		Characters: str(r, "characters"), StyleName: str(r, "style_name"), Notice: str(r, "notice"), Error: str(r, "error"),
		StartedAt: str(r, "started_at"), FinishedAt: str(r, "finished_at"),
	}
}

func (s *server) loadProduction(ctx context.Context, id string) (*Production, error) {
	rows, err := s.query(ctx, "SELECT * FROM productions WHERE idea_id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	p := prodFromRow(rows[0])
	return &p, nil
}

func assetFromRow(r map[string]any) *Asset {
	return &Asset{
		ID: str(r, "id"), IdeaID: str(r, "idea_id"), Kind: str(r, "kind"), No: intOf(r, "no"), Caption: str(r, "caption"),
		Anchor: str(r, "anchor"), Shot: str(r, "shot"), Visual: str(r, "visual"), Voice: str(r, "voice"),
		Seconds: intOf(r, "seconds"), Prompt: str(r, "prompt"), Status: str(r, "status"), Error: str(r, "error"), ImageID: str(r, "image_id"),
	}
}

const assetCols = "id, idea_id, kind, no, caption, anchor, shot, visual, voice, seconds, prompt, status, error, image_id"

func (s *server) loadAssets(ctx context.Context, id string) ([]*Asset, error) {
	rows, err := s.query(ctx, "SELECT "+assetCols+" FROM assets WHERE idea_id = ? ORDER BY CASE kind WHEN 'cover' THEN 0 WHEN 'image' THEN 1 ELSE 2 END, no", id)
	if err != nil {
		return nil, err
	}
	out := make([]*Asset, 0, len(rows))
	for _, r := range rows {
		out = append(out, assetFromRow(r))
	}
	return out, nil
}

func (s *server) loadAsset(ctx context.Context, ideaID, assetID string) (*Asset, error) {
	rows, err := s.query(ctx, "SELECT "+assetCols+" FROM assets WHERE id = ? AND idea_id = ?", assetID, ideaID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("找不到这格画面")
	}
	return assetFromRow(rows[0]), nil
}

func (s *server) saveAssetText(ctx context.Context, a *Asset) error {
	return s.exec(ctx, `UPDATE assets SET caption = ?, anchor = ?, shot = ?, visual = ?, voice = ?, seconds = ?, prompt = ?, status = ?, error = ?, image_id = ?, updated_at = ? WHERE id = ?`,
		a.Caption, a.Anchor, a.Shot, a.Visual, a.Voice, a.Seconds, a.Prompt, a.Status, a.Error, a.ImageID, s.nowISO(), a.ID)
}

func (s *server) setAssetStatus(ctx context.Context, assetID, status, errText string) error {
	return s.exec(ctx, "UPDATE assets SET status = ?, error = ?, updated_at = ? WHERE id = ?", status, errText, s.nowISO(), assetID)
}

func styleFromRow(r map[string]any) Style {
	return Style{
		ID: str(r, "id"), Name: str(r, "name"), Category: str(r, "category"), Preset: intOf(r, "preset") == 1,
		Summary: str(r, "summary"), Tone: str(r, "tone"), Sentences: str(r, "sentences"), Opening: str(r, "opening"),
		Structure: str(r, "structure"), Logic: str(r, "logic"), Story: str(r, "story"), Flow: str(r, "flow"),
		Words: jsonList(str(r, "words")), Avoid: jsonList(str(r, "avoid")), Samples: jsonList(str(r, "samples")),
		Reference: str(r, "reference"),
	}
}

func (s *server) loadVoice(ctx context.Context, styleID string) voiceInput {
	var v voiceInput
	if styleID != "" {
		if rows, err := s.query(ctx, "SELECT * FROM styles WHERE id = ?", styleID); err == nil && len(rows) > 0 {
			st := styleFromRow(rows[0])
			v.Style = &st
		}
	}
	if rows, err := s.query(ctx, "SELECT id, name, rule, status, source FROM rules WHERE status = 'active' ORDER BY created_at"); err == nil {
		for _, r := range rows {
			v.Rules = append(v.Rules, Rule{ID: str(r, "id"), Name: str(r, "name"), Rule: str(r, "rule"), Status: str(r, "status"), Source: str(r, "source")})
		}
	}
	return v
}

// seedPresets 第一次启用时写入 15 个风格预设（来自上游 src/style-presets.js）。删掉的不会再补回来。
func (s *server) seedPresets(ctx context.Context) error {
	if s.kvGet(ctx, "presets_seeded") == "1" {
		return nil
	}
	raw, err := presetFS.ReadFile("data/style-presets.json")
	if err != nil {
		return err
	}
	var list []struct {
		ID        string   `json:"id"`
		Category  string   `json:"category"`
		Name      string   `json:"name"`
		Summary   string   `json:"summary"`
		Tone      string   `json:"tone"`
		Sentences string   `json:"sentences"`
		Opening   string   `json:"opening"`
		Structure string   `json:"structure"`
		Words     []string `json:"words"`
		Avoid     []string `json:"avoid"`
		Samples   []string `json:"samples"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	now := s.nowISO()
	for _, p := range list {
		if err := s.exec(ctx, `INSERT OR IGNORE INTO styles(id, name, category, preset, summary, tone, sentences, opening, structure, words, avoid, samples, created_at, updated_at)
			VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"preset_"+p.ID, p.Name, p.Category, p.Summary, p.Tone, p.Sentences, p.Opening, p.Structure,
			jsonText(p.Words), jsonText(p.Avoid), jsonText(p.Samples), now, now); err != nil {
			return err
		}
	}
	return s.kvSet(ctx, "presets_seeded", "1")
}

// cleanupImages 删掉这条灵感里既不在画面上、也不在撤销快照里的图片。
func (s *server) cleanupImages(ctx context.Context, ideaID string) {
	imgs, err := s.query(ctx, "SELECT id FROM images WHERE idea_id = ?", ideaID)
	if err != nil || len(imgs) == 0 {
		return
	}
	used := map[string]bool{}
	if rows, err := s.query(ctx, "SELECT image_id FROM assets WHERE idea_id = ?", ideaID); err == nil {
		for _, r := range rows {
			used[str(r, "image_id")] = true
		}
	} else {
		return
	}
	snaps := ""
	if rows, err := s.query(ctx, "SELECT snapshot FROM revisions WHERE idea_id = ?", ideaID); err == nil {
		for _, r := range rows {
			snaps += str(r, "snapshot")
		}
	} else {
		return
	}
	for _, r := range imgs {
		id := str(r, "id")
		if used[id] || strings.Contains(snaps, `"`+id+`"`) {
			continue
		}
		_ = s.exec(ctx, "DELETE FROM images WHERE id = ?", id)
	}
}
