// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

// HTTP API：前端 fetch 同源 /api/plugins/external/media_desk/api/*，宿主转发成 plugin.http.request。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type rawResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &apiError{status: 400, msg: fmt.Sprintf(format, args...)}
}

func (s *server) handleHTTPRequest(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Method  string          `json:"method"`
		Path    string          `json:"path"`
		Query   json.RawMessage `json:"query"`
		BodyB64 string          `json:"body_b64"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rpcErrorf("invalid_params", "HTTP 请求参数不是合法 JSON：%v", err)
	}
	body, _ := base64.StdEncoding.DecodeString(p.BodyB64)
	status, payload := s.route(ctx, strings.ToUpper(p.Method), routePath(p.Path), parseQuery(p.Query), body)
	if rr, ok := payload.(rawResponse); ok {
		return map[string]any{
			"stream": false, "status": rr.Status,
			"headers":  map[string]string{"Content-Type": rr.ContentType, "Cache-Control": "private, max-age=86400"},
			"body_b64": base64.StdEncoding.EncodeToString(rr.Body),
		}, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, rpcErrorf("internal", "编码 HTTP 响应失败：%v", err)
	}
	return map[string]any{
		"stream": false, "status": status,
		"headers":  map[string]string{"Content-Type": "application/json; charset=utf-8"},
		"body_b64": base64.StdEncoding.EncodeToString(encoded),
	}, nil
}

// routePath 把宿主给的 path 规整成 /ideas 这种形式（两种历史格式都认）。
func routePath(raw string) string {
	p := strings.TrimSuffix(strings.TrimSpace(raw), "/")
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	marker := "/" + pluginID + "/api"
	if i := strings.Index(p, marker); i >= 0 {
		p = p[i+len(marker):]
	}
	if rest, ok := strings.CutPrefix(p, "/api"); ok && (rest == "" || rest[0] == '/') {
		p = rest
	}
	if p == "" || p[0] != '/' {
		p = "/" + p
	}
	return p
}

// parseQuery 认原始查询串，也认对象形式。
func parseQuery(raw json.RawMessage) url.Values {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, _ := url.ParseQuery(strings.TrimPrefix(s, "?"))
		return v
	}
	out := url.Values{}
	var m map[string]string
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			out.Set(k, v)
		}
	}
	return out
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// segs 把 /ideas/xxx/assets/yyy/draw 拆成段，并校验 id 段。
func segs(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 1; i < len(parts); i += 2 {
		if !idRe.MatchString(parts[i]) {
			return nil
		}
	}
	return parts
}

func (s *server) route(ctx context.Context, method, path string, q url.Values, body []byte) (int, any) {
	result, err := s.dispatchAPI(ctx, method, path, q, body)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			return ae.status, map[string]any{"error": ae.msg}
		}
		if errors.Is(err, errNotFound) {
			return 404, map[string]any{"error": err.Error()}
		}
		s.log.Warn("接口出错", "method", method, "path", path, "err", err)
		return 500, map[string]any{"error": err.Error()}
	}
	if rr, ok := result.(rawResponse); ok {
		return rr.Status, rr
	}
	if a, ok := result.(accepted); ok {
		return 202, map[string]any(a)
	}
	return 200, result
}

type accepted map[string]any

func decode(body []byte, v any) error {
	if len(body) == 0 {
		return badRequest("请求体为空")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return badRequest("请求体不是合法 JSON")
	}
	return nil
}

func (s *server) dispatchAPI(ctx context.Context, method, path string, q url.Values, body []byte) (any, error) {
	p := segs(path)
	if p == nil {
		return nil, badRequest("路径不合法")
	}
	n := len(p)
	switch {
	case p[0] == "status" && n == 1 && method == "GET":
		return s.apiStatus(ctx), nil
	case p[0] == "ideas" && n == 1 && method == "GET":
		return s.apiIdeas(ctx)
	case p[0] == "ideas" && n == 1 && method == "POST":
		return s.apiCreateIdea(ctx, body)
	case p[0] == "ideas" && n == 2 && method == "GET":
		return s.apiIdeaDetail(ctx, p[1])
	case p[0] == "ideas" && n == 2 && method == "PUT":
		return s.apiUpdateIdea(ctx, p[1], body)
	case p[0] == "ideas" && n == 2 && method == "DELETE":
		return s.apiDeleteIdea(ctx, p[1])
	case p[0] == "ideas" && n == 3 && p[2] == "produce" && method == "POST":
		var in struct {
			Format     string `json:"format"`
			WithImages *bool  `json:"with_images"`
		}
		_ = json.Unmarshal(body, &in)
		if err := s.startProduction(ctx, p[1], in.Format, in.WithImages); err != nil {
			if errors.Is(err, errNotFound) {
				return nil, err
			}
			return nil, &apiError{status: 409, msg: err.Error()}
		}
		return accepted{"ok": true, "status": "running"}, nil
	case p[0] == "ideas" && n == 3 && p[2] == "cancel" && method == "POST":
		return map[string]any{"ok": s.cancelProduction(p[1])}, nil
	case p[0] == "ideas" && n == 3 && p[2] == "chat" && method == "POST":
		var in struct {
			Message string `json:"message"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if err := s.startChat(ctx, p[1], in.Message); err != nil {
			return nil, &apiError{status: 409, msg: err.Error()}
		}
		return accepted{"ok": true}, nil
	case p[0] == "ideas" && n == 3 && p[2] == "undo" && method == "POST":
		label, err := s.undo(ctx, p[1])
		if err != nil {
			return nil, &apiError{status: 409, msg: err.Error()}
		}
		return map[string]any{"ok": true, "label": label}, nil
	case p[0] == "ideas" && n == 3 && p[2] == "production" && method == "PUT":
		return s.apiEditProduction(ctx, p[1], body)
	case p[0] == "ideas" && n == 4 && p[2] == "assets" && method == "PUT":
		return s.apiEditAsset(ctx, p[1], p[3], body)
	case p[0] == "ideas" && n == 5 && p[2] == "assets" && p[4] == "draw" && method == "POST":
		if err := s.redrawAsset(ctx, p[1], p[3]); err != nil {
			return nil, &apiError{status: 409, msg: err.Error()}
		}
		return accepted{"ok": true}, nil
	case p[0] == "images" && n == 2 && method == "GET":
		return s.apiImage(ctx, p[1])
	case p[0] == "today" && n == 1 && method == "GET":
		return s.apiToday(ctx, q.Get("date"))
	case p[0] == "metrics" && n == 1 && method == "GET":
		return s.apiMetrics(ctx)
	case p[0] == "metrics" && n == 1 && method == "POST":
		return s.apiAddMetrics(ctx, body)
	case p[0] == "metrics" && n == 2 && method == "DELETE":
		return map[string]any{"ok": true}, s.exec(ctx, "DELETE FROM metrics WHERE id = ?", p[1])
	case p[0] == "insights" && n == 1 && method == "GET":
		return s.apiInsights(ctx)
	case p[0] == "styles" && n == 1 && method == "GET":
		return s.apiStyles(ctx)
	case p[0] == "styles" && n == 1 && method == "POST":
		return s.apiSaveStyle(ctx, "", body)
	case p[0] == "styles" && n == 2 && p[1] == "learn" && method == "POST":
		return s.apiLearnStyle(ctx, body)
	case p[0] == "styles" && n == 2 && p[1] == "learn" && method == "GET":
		return s.learnSnapshot(), nil
	case p[0] == "styles" && n == 2 && p[1] == "active" && method == "POST":
		var in struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &in)
		return map[string]any{"ok": true}, s.kvSet(ctx, "active_style_id", in.ID)
	case p[0] == "styles" && n == 2 && method == "PUT":
		return s.apiSaveStyle(ctx, p[1], body)
	case p[0] == "styles" && n == 2 && method == "DELETE":
		if s.kvGet(ctx, "active_style_id") == p[1] {
			_ = s.kvSet(ctx, "active_style_id", "")
		}
		return map[string]any{"ok": true}, s.exec(ctx, "DELETE FROM styles WHERE id = ?", p[1])
	case p[0] == "rules" && n == 1 && method == "POST":
		return s.apiSaveRule(ctx, "", body)
	case p[0] == "rules" && n == 2 && method == "PUT":
		return s.apiSaveRule(ctx, p[1], body)
	case p[0] == "rules" && n == 2 && method == "DELETE":
		return map[string]any{"ok": true}, s.exec(ctx, "DELETE FROM rules WHERE id = ?", p[1])
	case p[0] == "settings" && n == 1 && method == "GET":
		return s.apiSettings(ctx), nil
	case p[0] == "settings" && n == 1 && method == "PUT":
		return s.apiSaveSettings(ctx, body)
	}
	return nil, &apiError{status: 404, msg: "未知接口"}
}

// ---------------------------------------------------------------------------
// 灵感 / 内容
// ---------------------------------------------------------------------------

type ideaRow struct {
	Idea
	ProdStatus  string `json:"prod_status"`
	ProdTitle   string `json:"prod_title"`
	CoverText   string `json:"cover_text"`
	CoverImage  string `json:"cover_image"`
	Views       int    `json:"views"`
	Engagement  int    `json:"engagement"`
	MetricCount int    `json:"metric_count"`
}

func (s *server) apiIdeas(ctx context.Context) (any, error) {
	rows, err := s.query(ctx, `SELECT i.id, i.title, i.body, i.url, i.source, i.tags, i.status, i.platform, i.scheduled_at, i.published_at, i.pinned, i.created_at, i.updated_at,
		COALESCE(p.status, '') AS prod_status, COALESCE(p.title, '') AS prod_title, COALESCE(p.cover_text, '') AS cover_text,
		COALESCE((SELECT a.image_id FROM assets a WHERE a.idea_id = i.id AND a.kind = 'cover'), '') AS cover_image,
		COALESCE((SELECT SUM(m.views) FROM metrics m WHERE m.idea_id = i.id), 0) AS views,
		COALESCE((SELECT SUM(m.likes + m.comments + m.shares + m.saves) FROM metrics m WHERE m.idea_id = i.id), 0) AS engagement,
		(SELECT COUNT(*) FROM metrics m WHERE m.idea_id = i.id) AS metric_count
		FROM ideas i LEFT JOIN productions p ON p.idea_id = i.id
		ORDER BY i.pinned DESC, i.created_at DESC LIMIT 2000`)
	if err != nil {
		return nil, err
	}
	out := make([]ideaRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ideaRow{
			Idea: ideaFromRow(r), ProdStatus: str(r, "prod_status"), ProdTitle: str(r, "prod_title"), CoverText: str(r, "cover_text"),
			CoverImage: str(r, "cover_image"), Views: intOf(r, "views"), Engagement: intOf(r, "engagement"), MetricCount: intOf(r, "metric_count"),
		})
	}
	return map[string]any{"ideas": out, "platforms": platforms}, nil
}

type ideaInput struct {
	Title       *string   `json:"title"`
	Body        *string   `json:"body"`
	URL         *string   `json:"url"`
	Source      *string   `json:"source"`
	Tags        *[]string `json:"tags"`
	Status      *string   `json:"status"`
	Platform    *string   `json:"platform"`
	ScheduledAt *string   `json:"scheduled_at"`
	Pinned      *bool     `json:"pinned"`
}

var schedRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$`)

func cleanTags(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = cut(strings.TrimPrefix(strings.TrimSpace(t), "#"), 20)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
		if len(out) >= 12 {
			break
		}
	}
	return out
}

// applyInput 把白名单字段落到灵感上；排期和状态联动沿用上游 applySchedule / applyStatus。
func applyInput(idea *Idea, in ideaInput, produced bool, now string) error {
	if in.Title != nil {
		idea.Title = cut(*in.Title, 200)
	}
	if in.Body != nil {
		idea.Body = cut(*in.Body, 20000)
	}
	if in.URL != nil {
		u := strings.TrimSpace(*in.URL)
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return badRequest("链接要以 http:// 或 https:// 开头")
		}
		idea.URL = cut(u, 1000)
	}
	if in.Source != nil {
		idea.Source = cut(*in.Source, 100)
	}
	if in.Tags != nil {
		idea.Tags = cleanTags(*in.Tags)
	}
	if in.Platform != nil {
		if !validPlatform(*in.Platform) {
			return badRequest("不支持的平台：%s", *in.Platform)
		}
		idea.Platform = *in.Platform
	}
	if in.Pinned != nil {
		idea.Pinned = *in.Pinned
	}
	if in.ScheduledAt != nil {
		when := strings.TrimSpace(*in.ScheduledAt)
		if when != "" && !schedRe.MatchString(when) {
			return badRequest("排期时间格式应为 YYYY-MM-DDTHH:MM")
		}
		idea.ScheduledAt = when
		if idea.Status != "published" {
			switch {
			case when != "" && (produced || idea.Status == "draft" || idea.Status == "ready"):
				idea.Status = "ready"
			case when != "":
				idea.Status = "outline"
			case idea.Status == "ready":
				idea.Status = "draft"
			case idea.Status == "outline" && !produced:
				idea.Status = "idea"
			}
		}
	}
	if in.Status != nil {
		if !validStatus(*in.Status) {
			return badRequest("状态无效")
		}
		idea.Status = *in.Status
		if idea.Status == "published" && idea.PublishedAt == "" {
			idea.PublishedAt = now
		}
		if idea.Status != "published" {
			idea.PublishedAt = ""
		}
	}
	if strings.TrimSpace(idea.Title) == "" {
		idea.Title = cut(idea.Body, 28)
	}
	if strings.TrimSpace(idea.Title) == "" {
		return badRequest("灵感内容为空")
	}
	return nil
}

func (s *server) saveIdea(ctx context.Context, idea Idea, insert bool) error {
	if insert {
		return s.exec(ctx, "INSERT INTO ideas("+ideaCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			idea.ID, idea.Title, idea.Body, idea.URL, idea.Source, jsonText(idea.Tags), idea.Status, idea.Platform,
			idea.ScheduledAt, idea.PublishedAt, boolInt(idea.Pinned), idea.CreatedAt, idea.UpdatedAt)
	}
	return s.exec(ctx, `UPDATE ideas SET title = ?, body = ?, url = ?, source = ?, tags = ?, status = ?, platform = ?, scheduled_at = ?, published_at = ?, pinned = ?, updated_at = ? WHERE id = ?`,
		idea.Title, idea.Body, idea.URL, idea.Source, jsonText(idea.Tags), idea.Status, idea.Platform,
		idea.ScheduledAt, idea.PublishedAt, boolInt(idea.Pinned), idea.UpdatedAt, idea.ID)
}

func (s *server) apiCreateIdea(ctx context.Context, body []byte) (any, error) {
	var in ideaInput
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	now := s.nowISO()
	idea := Idea{ID: newID("it"), Status: "idea", Tags: []string{}, CreatedAt: now, UpdatedAt: now}
	if err := applyInput(&idea, in, false, now); err != nil {
		return nil, err
	}
	if err := s.saveIdea(ctx, idea, true); err != nil {
		return nil, err
	}
	return map[string]any{"idea": idea}, nil
}

func (s *server) apiUpdateIdea(ctx context.Context, id string, body []byte) (any, error) {
	var in ideaInput
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	idea, err := s.loadIdea(ctx, id)
	if err != nil {
		return nil, err
	}
	prod, _ := s.loadProduction(ctx, id)
	now := s.nowISO()
	if err := applyInput(&idea, in, prod != nil && prod.Status == "done", now); err != nil {
		return nil, err
	}
	idea.UpdatedAt = now
	if err := s.saveIdea(ctx, idea, false); err != nil {
		return nil, err
	}
	return map[string]any{"idea": idea}, nil
}

func (s *server) apiDeleteIdea(ctx context.Context, id string) (any, error) {
	s.cancelProduction(id)
	for _, sql := range []string{
		"DELETE FROM assets WHERE idea_id = ?", "DELETE FROM images WHERE idea_id = ?", "DELETE FROM chats WHERE idea_id = ?",
		"DELETE FROM revisions WHERE idea_id = ?", "DELETE FROM productions WHERE idea_id = ?", "DELETE FROM metrics WHERE idea_id = ?",
		"DELETE FROM ideas WHERE id = ?",
	} {
		if err := s.exec(ctx, sql, id); err != nil {
			return nil, err
		}
	}
	return map[string]any{"ok": true}, nil
}

func (s *server) apiIdeaDetail(ctx context.Context, id string) (any, error) {
	idea, err := s.loadIdea(ctx, id)
	if err != nil {
		return nil, err
	}
	prod, err := s.loadProduction(ctx, id)
	if err != nil {
		return nil, err
	}
	assets, err := s.loadAssets(ctx, id)
	if err != nil {
		return nil, err
	}
	chats := []map[string]any{}
	if rows, err := s.query(ctx, "SELECT id, role, text, created_at FROM chats WHERE idea_id = ? ORDER BY id DESC LIMIT 60", id); err == nil {
		for i := len(rows) - 1; i >= 0; i-- {
			chats = append(chats, map[string]any{"id": intOf(rows[i], "id"), "role": str(rows[i], "role"), "text": str(rows[i], "text"), "created_at": str(rows[i], "created_at")})
		}
	}
	undo := map[string]any{"count": 0, "label": ""}
	if rows, err := s.query(ctx, "SELECT COUNT(*) AS n, (SELECT label FROM revisions WHERE idea_id = ? ORDER BY id DESC LIMIT 1) AS label FROM revisions WHERE idea_id = ?", id, id); err == nil && len(rows) > 0 {
		undo = map[string]any{"count": intOf(rows[0], "n"), "label": str(rows[0], "label")}
	}
	return map[string]any{
		"idea": idea, "production": prod, "assets": assets, "chats": chats, "undo": undo,
		"producing": s.isProducing(id), "chatting": s.isChatting(id),
	}, nil
}

func (s *server) apiEditProduction(ctx context.Context, id string, body []byte) (any, error) {
	var in struct {
		Title     *string `json:"title"`
		CoverText *string `json:"cover_text"`
		Article   *string `json:"article"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	prod, err := s.loadProduction(ctx, id)
	if err != nil {
		return nil, err
	}
	if prod == nil || prod.Status == "running" {
		return nil, &apiError{status: 409, msg: "成稿还没完成，等它写完再改"}
	}
	assets, _ := s.loadAssets(ctx, id)
	before := *prod
	if in.Title != nil {
		prod.Title = cut(*in.Title, 200)
	}
	if in.CoverText != nil {
		prod.CoverText = cut(*in.CoverText, 40)
	}
	if in.Article != nil {
		prod.Article = cut(*in.Article, 200000)
	}
	if prod.Title == before.Title && prod.CoverText == before.CoverText && prod.Article == before.Article {
		return map[string]any{"ok": true}, nil
	}
	if err := s.pushRevision(ctx, id, "手动修改", &before, assets); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, s.exec(ctx, "UPDATE productions SET title = ?, cover_text = ?, article = ?, updated_at = ? WHERE idea_id = ?", prod.Title, prod.CoverText, prod.Article, s.nowISO(), id)
}

func (s *server) apiEditAsset(ctx context.Context, ideaID, assetID string, body []byte) (any, error) {
	var in struct {
		Caption *string `json:"caption"`
		Shot    *string `json:"shot"`
		Visual  *string `json:"visual"`
		Voice   *string `json:"voice"`
		Seconds *int    `json:"seconds"`
		Prompt  *string `json:"prompt"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	prod, err := s.loadProduction(ctx, ideaID)
	if err != nil || prod == nil {
		return nil, &apiError{status: 409, msg: "还没有成稿"}
	}
	assets, err := s.loadAssets(ctx, ideaID)
	if err != nil {
		return nil, err
	}
	var target *Asset
	for _, a := range assets {
		if a.ID == assetID {
			target = a
		}
	}
	if target == nil {
		return nil, &apiError{status: 404, msg: "找不到这格画面"}
	}
	if err := s.pushRevision(ctx, ideaID, "手动修改画面", prod, cloneAssets(assets)); err != nil {
		return nil, err
	}
	if in.Caption != nil {
		target.Caption = cut(*in.Caption, 60)
	}
	if in.Shot != nil {
		target.Shot = cut(*in.Shot, 12)
	}
	if in.Visual != nil {
		target.Visual = cut(*in.Visual, 200)
	}
	if in.Voice != nil {
		target.Voice = cut(*in.Voice, 200)
	}
	if in.Seconds != nil {
		target.Seconds = clampSeconds(*in.Seconds)
	}
	if in.Prompt != nil {
		target.Prompt = cut(*in.Prompt, 1200)
	}
	return map[string]any{"ok": true, "asset": target}, s.saveAssetText(ctx, target)
}

func (s *server) apiImage(ctx context.Context, id string) (any, error) {
	rows, err := s.query(ctx, "SELECT mime, data_b64 FROM images WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &apiError{status: 404, msg: "图片不存在"}
	}
	data, err := base64.StdEncoding.DecodeString(str(rows[0], "data_b64"))
	if err != nil {
		return nil, errors.New("图片数据损坏")
	}
	return rawResponse{Status: 200, ContentType: str(rows[0], "mime"), Body: data}, nil
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// apiToday 今日卡片：今天排期的内容 + 接下来 7 天的数量。date 由浏览器给本地日期。
func (s *server) apiToday(ctx context.Context, date string) (any, error) {
	if !dateRe.MatchString(date) {
		date = s.now().Format("2006-01-02")
	}
	day, _ := time.Parse("2006-01-02", date)
	end := day.AddDate(0, 0, 8).Format("2006-01-02")
	rows, err := s.query(ctx, `SELECT i.id, i.title, i.status, i.platform, i.scheduled_at, COALESCE(p.title, '') AS prod_title, COALESCE(p.status, '') AS prod_status
		FROM ideas i LEFT JOIN productions p ON p.idea_id = i.id
		WHERE i.scheduled_at >= ? AND i.scheduled_at < ? ORDER BY i.scheduled_at`, date, end)
	if err != nil {
		return nil, err
	}
	today := []map[string]any{}
	upcoming := []map[string]any{}
	for _, r := range rows {
		item := map[string]any{
			"id": str(r, "id"), "title": str(r, "title"), "prod_title": str(r, "prod_title"), "prod_status": str(r, "prod_status"),
			"status": str(r, "status"), "platform": str(r, "platform"), "scheduled_at": str(r, "scheduled_at"),
		}
		if strings.HasPrefix(str(r, "scheduled_at"), date) {
			today = append(today, item)
		} else if len(upcoming) < 3 {
			upcoming = append(upcoming, item)
		}
	}
	return map[string]any{"date": date, "today": today, "upcoming": upcoming}, nil
}

// ---------------------------------------------------------------------------
// 复盘
// ---------------------------------------------------------------------------

func (s *server) loadMetrics(ctx context.Context) ([]Metric, error) {
	rows, err := s.query(ctx, `SELECT m.id, m.idea_id, COALESCE(p.title, i.title, '') AS title, m.platform, m.date, m.views, m.likes, m.comments, m.shares, m.saves
		FROM metrics m LEFT JOIN ideas i ON i.id = m.idea_id LEFT JOIN productions p ON p.idea_id = m.idea_id AND p.title != ''
		ORDER BY m.date DESC, m.created_at DESC LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	out := make([]Metric, 0, len(rows))
	for _, r := range rows {
		out = append(out, Metric{
			ID: str(r, "id"), IdeaID: str(r, "idea_id"), Title: str(r, "title"), Platform: str(r, "platform"), Date: str(r, "date"),
			Views: intOf(r, "views"), Likes: intOf(r, "likes"), Comments: intOf(r, "comments"), Shares: intOf(r, "shares"), Saves: intOf(r, "saves"),
		})
	}
	return out, nil
}

func (s *server) apiMetrics(ctx context.Context) (any, error) {
	list, err := s.loadMetrics(ctx)
	if err != nil {
		return nil, err
	}
	if len(list) > 300 {
		list = list[:300]
	}
	return map[string]any{"metrics": list}, nil
}

type metricInput struct {
	IdeaID   string `json:"idea_id"`
	Platform string `json:"platform"`
	Date     string `json:"date"`
	Views    int    `json:"views"`
	Likes    int    `json:"likes"`
	Comments int    `json:"comments"`
	Shares   int    `json:"shares"`
	Saves    int    `json:"saves"`
}

func nonNeg(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// apiAddMetrics 录一条，或者批量导入（{"items":[...]}）。
func (s *server) apiAddMetrics(ctx context.Context, body []byte) (any, error) {
	var in struct {
		metricInput
		Items []metricInput `json:"items"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	items := in.Items
	if len(items) == 0 {
		items = []metricInput{in.metricInput}
	}
	if len(items) > 500 {
		return nil, badRequest("一次最多导入 500 条")
	}
	for i, m := range items {
		if strings.TrimSpace(m.Platform) == "" || !validPlatform(m.Platform) {
			return nil, badRequest("第 %d 条：平台不对（%s）", i+1, m.Platform)
		}
		if !dateRe.MatchString(m.Date) {
			return nil, badRequest("第 %d 条：日期格式应为 YYYY-MM-DD", i+1)
		}
		if m.IdeaID != "" && !idRe.MatchString(m.IdeaID) {
			return nil, badRequest("第 %d 条：内容 id 不合法", i+1)
		}
	}
	now := s.nowISO()
	for _, m := range items {
		if err := s.exec(ctx, `INSERT INTO metrics(id, idea_id, platform, date, views, likes, comments, shares, saves, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID("m"), m.IdeaID, m.Platform, m.Date, nonNeg(m.Views), nonNeg(m.Likes), nonNeg(m.Comments), nonNeg(m.Shares), nonNeg(m.Saves), now); err != nil {
			return nil, err
		}
	}
	return map[string]any{"ok": true, "added": len(items)}, nil
}

func (s *server) apiInsights(ctx context.Context) (any, error) {
	metrics, err := s.loadMetrics(ctx)
	if err != nil {
		return nil, err
	}
	hours := map[string]int{}
	if rows, err := s.query(ctx, "SELECT id, scheduled_at, published_at FROM ideas WHERE status = 'published'"); err == nil {
		for _, r := range rows {
			when := str(r, "scheduled_at")
			if when == "" {
				if t, err := time.Parse(time.RFC3339, str(r, "published_at")); err == nil {
					hours[str(r, "id")] = t.Local().Hour()
				}
				continue
			}
			if t, err := time.Parse("2006-01-02T15:04", when); err == nil {
				hours[str(r, "id")] = t.Hour()
			}
		}
	}
	var edits []editSignal
	if rows, err := s.query(ctx, "SELECT article, ai_article FROM productions WHERE ai_article != '' AND article != ai_article"); err == nil {
		for _, r := range rows {
			edits = append(edits, editSignal{Before: charCount(str(r, "ai_article")), After: charCount(str(r, "article"))})
		}
	}
	ins := buildInsights(metrics, hours, edits, s.now())
	// 已经写进规则的不再提
	if rows, err := s.query(ctx, "SELECT name FROM rules"); err == nil {
		have := map[string]bool{}
		for _, r := range rows {
			have[str(r, "name")] = true
		}
		kept := []Proposal{}
		for _, p := range ins.Proposals {
			if !have[p.Name] {
				kept = append(kept, p)
			}
		}
		ins.Proposals = kept
	}
	return ins, nil
}

// ---------------------------------------------------------------------------
// 风格与规则
// ---------------------------------------------------------------------------

func (s *server) apiStyles(ctx context.Context) (any, error) {
	rows, err := s.query(ctx, "SELECT * FROM styles ORDER BY preset ASC, created_at DESC")
	if err != nil {
		return nil, err
	}
	styles := make([]Style, 0, len(rows))
	for _, r := range rows {
		st := styleFromRow(r)
		st.Reference = "" // 原文只在写稿时用，不回传
		styles = append(styles, st)
	}
	rules := []Rule{}
	if rr, err := s.query(ctx, "SELECT id, name, rule, status, source FROM rules ORDER BY created_at DESC"); err == nil {
		for _, r := range rr {
			rules = append(rules, Rule{ID: str(r, "id"), Name: str(r, "name"), Rule: str(r, "rule"), Status: str(r, "status"), Source: str(r, "source")})
		}
	}
	return map[string]any{"styles": styles, "rules": rules, "active_style_id": s.kvGet(ctx, "active_style_id")}, nil
}

func cleanList(in []string, count, n int) []string {
	out := []string{}
	for _, x := range in {
		if t := cut(x, n); t != "" {
			out = append(out, t)
			if len(out) >= count {
				break
			}
		}
	}
	return out
}

func (s *server) insertStyle(ctx context.Context, st Style) error {
	now := s.nowISO()
	return s.exec(ctx, `INSERT INTO styles(id, name, category, preset, summary, tone, sentences, opening, structure, logic, story, flow, words, avoid, samples, reference, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		st.ID, st.Name, st.Category, st.Summary, st.Tone, st.Sentences, st.Opening, st.Structure, st.Logic, st.Story, st.Flow,
		jsonText(st.Words), jsonText(st.Avoid), jsonText(st.Samples), cut(st.Reference, referenceMax), now, now)
}

func (s *server) apiSaveStyle(ctx context.Context, id string, body []byte) (any, error) {
	var in Style
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	st := Style{
		Name: cut(in.Name, 20), Category: cut(in.Category, 20), Summary: cut(in.Summary, 80), Tone: cut(in.Tone, 200),
		Sentences: cut(in.Sentences, 200), Opening: cut(in.Opening, 200), Structure: cut(in.Structure, 200),
		Logic: cut(in.Logic, 200), Story: cut(in.Story, 200), Flow: cut(in.Flow, 200),
		Words: cleanList(in.Words, 8, 20), Avoid: cleanList(in.Avoid, 5, 40), Samples: cleanList(in.Samples, 6, 160),
	}
	if st.Name == "" {
		return nil, badRequest("风格需要名字")
	}
	if id == "" {
		st.ID = newID("st")
		if st.Category == "" {
			st.Category = "我的风格"
		}
		if err := s.insertStyle(ctx, st); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "id": st.ID}, nil
	}
	return map[string]any{"ok": true, "id": id}, s.exec(ctx, `UPDATE styles SET name = ?, summary = ?, tone = ?, sentences = ?, opening = ?, structure = ?, logic = ?, story = ?, flow = ?, words = ?, avoid = ?, samples = ?, updated_at = ? WHERE id = ?`,
		st.Name, st.Summary, st.Tone, st.Sentences, st.Opening, st.Structure, st.Logic, st.Story, st.Flow,
		jsonText(st.Words), jsonText(st.Avoid), jsonText(st.Samples), s.nowISO(), id)
}

type learnJob struct {
	Running bool   `json:"running"`
	Error   string `json:"error"`
	StyleID string `json:"style_id"`
	Name    string `json:"name"`
}

func (s *server) learnSnapshot() learnJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.learn
}

// apiLearnStyle 贴几篇自己的稿子，让模型提炼成风格卡（后台任务）。
func (s *server) apiLearnStyle(ctx context.Context, body []byte) (any, error) {
	var in struct {
		Text string `json:"text"`
		Name string `json:"name"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(in.Text)
	if len([]rune(text)) < 80 {
		return nil, badRequest("内容太少了，至少贴 80 字，越多越准")
	}
	s.mu.Lock()
	if s.learn.Running {
		s.mu.Unlock()
		return nil, &apiError{status: 409, msg: "正在提炼上一份风格"}
	}
	s.learn = learnJob{Running: true}
	s.mu.Unlock()
	go func() {
		job := learnJob{}
		sys, user := stylePrompts(text, in.Name)
		out, err := s.runAgent(s.bgCtx(), sys, user)
		if err == nil {
			var raw map[string]any
			raw, err = extractJSON(out)
			if err == nil {
				st := normalizeStyle(raw, in.Name)
				st.ID = newID("st")
				st.Category = "我的风格"
				st.Reference = text
				if err = s.insertStyle(context.WithoutCancel(s.bgCtx()), st); err == nil {
					job.StyleID, job.Name = st.ID, st.Name
				}
			}
		}
		if err != nil {
			job.Error = err.Error()
		}
		s.mu.Lock()
		s.learn = job
		s.mu.Unlock()
	}()
	return accepted{"ok": true}, nil
}

func (s *server) apiSaveRule(ctx context.Context, id string, body []byte) (any, error) {
	var in struct {
		Name   *string `json:"name"`
		Rule   *string `json:"rule"`
		Status *string `json:"status"`
		Source string  `json:"source"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "paused" {
		return nil, badRequest("规则状态只能是 active 或 paused")
	}
	if id == "" {
		if in.Name == nil || in.Rule == nil || cut(*in.Name, 40) == "" || cut(*in.Rule, 400) == "" {
			return nil, badRequest("规则需要名字和内容")
		}
		status := "active"
		if in.Status != nil {
			status = *in.Status
		}
		rid := newID("r")
		return map[string]any{"ok": true, "id": rid}, s.exec(ctx, "INSERT INTO rules(id, name, rule, status, source, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			rid, cut(*in.Name, 40), cut(*in.Rule, 400), status, cut(in.Source, 40), s.nowISO())
	}
	rows, err := s.query(ctx, "SELECT name, rule, status FROM rules WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &apiError{status: 404, msg: "找不到这条规则"}
	}
	name, rule, status := str(rows[0], "name"), str(rows[0], "rule"), str(rows[0], "status")
	if in.Name != nil && cut(*in.Name, 40) != "" {
		name = cut(*in.Name, 40)
	}
	if in.Rule != nil && cut(*in.Rule, 400) != "" {
		rule = cut(*in.Rule, 400)
	}
	if in.Status != nil {
		status = *in.Status
	}
	return map[string]any{"ok": true}, s.exec(ctx, "UPDATE rules SET name = ?, rule = ?, status = ? WHERE id = ?", name, rule, status, id)
}

// ---------------------------------------------------------------------------
// 设置
// ---------------------------------------------------------------------------

func (s *server) apiStatus(ctx context.Context) map[string]any {
	st := s.loadSettings(ctx)
	key, _ := s.secretGet(ctx)
	known, granted := s.networkGranted()
	return map[string]any{
		"image_configured": st.Image.configured(), "image_has_key": key != "",
		"network_known": known, "network_granted": granted,
		"draft_format": st.DraftFormat, "generate_images": st.GenerateImages, "active_style_id": st.ActiveStyleID,
	}
}

func (s *server) apiSettings(ctx context.Context) map[string]any {
	st := s.loadSettings(ctx)
	key, keyErr := s.secretGet(ctx)
	known, granted := s.networkGranted()
	out := map[string]any{
		"settings": st, "image_has_key": key != "", "network_known": known, "network_granted": granted, "platforms": platforms,
	}
	if keyErr != nil {
		out["secret_error"] = "读取 Key 失败：" + keyErr.Error()
	}
	return out
}

func (s *server) apiSaveSettings(ctx context.Context, body []byte) (any, error) {
	var in struct {
		DraftFormat    *string `json:"draft_format"`
		ImageCount     *int    `json:"image_count"`
		ShotCount      *int    `json:"shot_count"`
		GenerateImages *bool   `json:"generate_images"`
		BaseURL        *string `json:"image_base_url"`
		Model          *string `json:"image_model"`
		Size           *string `json:"image_size"`
		Quality        *string `json:"image_quality"`
		Key            *string `json:"image_key"`
		ClearKey       bool    `json:"clear_key"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	set := func(k, v string) error { return s.kvSet(ctx, k, v) }
	if in.DraftFormat != nil {
		f := "script"
		if *in.DraftFormat == "article" {
			f = "article"
		}
		if err := set("draft_format", f); err != nil {
			return nil, err
		}
	}
	if in.ImageCount != nil {
		v := *in.ImageCount
		if v < 0 || v > 4 {
			return nil, badRequest("配图数量是 0 到 4 张")
		}
		_ = set("image_count", fmt.Sprint(v))
	}
	if in.ShotCount != nil {
		v := *in.ShotCount
		if v < 3 || v > 12 {
			return nil, badRequest("分镜数量是 3 到 12 格")
		}
		_ = set("shot_count", fmt.Sprint(v))
	}
	if in.GenerateImages != nil {
		_ = set("generate_images", fmt.Sprint(boolInt(*in.GenerateImages)))
	}
	if in.BaseURL != nil {
		v := strings.TrimSpace(*in.BaseURL)
		if v != "" {
			if _, err := imageEndpoint(v); err != nil {
				return nil, badRequest("%s", err.Error())
			}
		}
		_ = set("image_base_url", cut(v, 300))
	}
	if in.Model != nil {
		_ = set("image_model", cut(*in.Model, 100))
	}
	if in.Size != nil {
		_ = set("image_size", cut(*in.Size, 20))
	}
	if in.Quality != nil {
		_ = set("image_quality", cut(*in.Quality, 20))
	}
	if in.ClearKey {
		if err := s.secretSet(ctx, ""); err != nil {
			return nil, fmt.Errorf("删除 Key 失败：%v", err)
		}
	} else if in.Key != nil && strings.TrimSpace(*in.Key) != "" {
		if err := s.secretSet(ctx, *in.Key); err != nil {
			return nil, fmt.Errorf("保存 Key 失败：%v", err)
		}
	}
	return s.apiSettings(ctx), nil
}
