// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHost 模拟宿主：记录调用，按方法返回预设结果。
type fakeHost struct {
	mu     sync.Mutex
	calls  []fakeCall
	agent  func(system, prompt string) (string, error)
	http   func(method, url string, headers map[string]string, body []byte) (*httpResult, error)
	query  func(sql string, args []any) []map[string]any
	secret string
}

type fakeCall struct {
	Method string
	Params map[string]any
}

func (f *fakeHost) call(_ context.Context, method string, params, out any) error {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Method: method, Params: p})
	f.mu.Unlock()
	var result any
	switch method {
	case "host.store.query":
		var rows []map[string]any
		if f.query != nil {
			args, _ := p["args"].([]any)
			rows = f.query(p["sql"].(string), args)
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		result = map[string]any{"rows": rows}
	case "host.store.exec":
		result = map[string]any{"rows_affected": 1}
	case "host.agent.run":
		if f.agent == nil {
			return rpcErrorf("unavailable", "没有配置模型服务商")
		}
		text, err := f.agent(str(p, "system"), str(p, "prompt"))
		if err != nil {
			return err
		}
		result = map[string]any{"status": "done", "result": text}
	case "host.http.request":
		if f.http == nil {
			return rpcErrorf("permission_denied", "未授予 network:any")
		}
		hd := map[string]string{}
		if m, ok := p["headers"].(map[string]any); ok {
			for k, v := range m {
				hd[k], _ = v.(string)
			}
		}
		body, _ := base64.StdEncoding.DecodeString(str(p, "body_b64"))
		res, err := f.http(str(p, "method"), str(p, "url"), hd, body)
		if err != nil {
			return err
		}
		result = map[string]any{"status": res.Status, "headers": res.Headers, "body_b64": base64.StdEncoding.EncodeToString(res.body)}
	case "host.secrets.get":
		result = map[string]any{"value": f.secret}
	case "host.secrets.set":
		f.secret = str(p, "value")
		result = map[string]any{"ok": true}
	case "host.secrets.delete":
		f.secret = ""
		result = map[string]any{"ok": true}
	default:
		return rpcErrorf("method_not_found", "未实现 %s", method)
	}
	if out != nil {
		b, _ := json.Marshal(result)
		return json.Unmarshal(b, out)
	}
	return nil
}

func (f *fakeHost) execSQL() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.Method == "host.store.exec" {
			out = append(out, c)
		}
	}
	return out
}

func newTestServer(f *fakeHost) *server {
	s := newServer(slog.New(slog.NewTextHandler(io.Discard, nil)), context.Background(), func() {})
	s.host = f
	s.now = func() time.Time { return time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local) }
	return s
}

const planJSON = "```json\n" + `{"title":"30 秒看懂 Agent","coverText":"它替你干活","facts":["一个等你问，一个替你干"],"outline":["开场","区别","怎么用"],"cover":"a robot holding a checklist","characters":"a young office worker","images":[{"anchor":"区别","caption":"两种助手对比","prompt":"two assistants side by side"},{"anchor":"怎么用","caption":"任务清单","prompt":"a task list"},{"anchor":"多余","caption":"多","prompt":"extra"}],"shots":[{"shot":"特写","visual":"手机屏幕","voice":"你还在问 AI 吗","seconds":3,"prompt":"close-up phone"},{"shot":"全景","visual":"办公室","voice":"它可以替你干","seconds":99,"prompt":"wide office"}]}` + "\n```"

func TestExtractJSONAndNormalizePlan(t *testing.T) {
	raw, err := extractJSON("好的，策划如下：\n" + planJSON + "\n以上。")
	if err != nil {
		t.Fatal(err)
	}
	p := normalizePlan(raw, 2, 6, "灵感")
	if p.Title != "30 秒看懂 Agent" || p.CoverText != "它替你干活" {
		t.Fatalf("标题/封面字不对：%+v", p)
	}
	if len(p.Images) != 2 {
		t.Fatalf("配图应截到 2 张：%d", len(p.Images))
	}
	if len(p.Shots) != 2 || p.Shots[1].Seconds != 60 {
		t.Fatalf("分镜秒数应夹到 60：%+v", p.Shots)
	}
	if _, err := extractJSON("没有 JSON"); err == nil {
		t.Fatal("没有 JSON 应报错")
	}
	// 空字段兜底
	p2 := normalizePlan(map[string]any{}, 1, 1, "一条很长很长很长很长的灵感标题")
	if p2.Title == "" || p2.CoverText == "" || p2.Cover == "" {
		t.Fatalf("兜底字段为空：%+v", p2)
	}
}

func TestPromptsCarryStyleAndRules(t *testing.T) {
	st := &Style{Name: "AI 科技博主", Summary: "讲成比喻", Words: []string{"说白了"}, Reference: "原文样本"}
	v := voiceInput{Style: st, Rules: []Rule{{Name: "开头先给数字", Rule: "50 字内出现数字", Status: "active"}, {Name: "停用", Rule: "x", Status: "paused"}}}
	idea := Idea{Title: "Agent 和聊天机器人的区别", Tags: []string{"AI"}, Platform: "抖音"}
	sys, user := planPrompts(idea, v, 2, 6)
	if !strings.Contains(sys, "images 正好 2 个，shots 正好 6 个") || !strings.Contains(sys, "开头先给数字") || strings.Contains(sys, "停用") {
		t.Fatalf("策划提示词缺少数量或规则：%s", sys)
	}
	if strings.Contains(sys, "原文样本") {
		t.Fatal("策划提示词不该带原文")
	}
	if !strings.Contains(user, "抖音") {
		t.Fatal("素材里应带目标平台")
	}
	plan := Plan{Title: "标题", Outline: []string{"开场", "结尾"}, Facts: []string{"事实一"}}
	sys2, _ := articlePrompts(idea, v, plan, "article")
	if !strings.Contains(sys2, "「## 开场」") || !strings.Contains(sys2, "原文样本") || !strings.Contains(sys2, "自然段") {
		t.Fatalf("正文提示词不对：%s", sys2)
	}
	sys3, _ := articlePrompts(idea, voiceInput{}, plan, "script")
	if !strings.Contains(sys3, "一句话占一行") {
		t.Fatal("口播体裁应要求一句一行")
	}
}

func TestApplyRevision(t *testing.T) {
	p := &Production{Title: "旧标题", CoverText: "旧字", Article: "第一句。\n第二句。"}
	assets := []*Asset{
		{ID: "c", Kind: "cover", Prompt: "old"},
		{ID: "i1", Kind: "image", No: 1, Caption: "图注", Prompt: "img"},
		{ID: "s1", Kind: "shot", No: 1, Shot: "中景", Visual: "v", Voice: "o", Seconds: 5, Prompt: "p"},
	}
	revise := map[string]any{
		"title":     "新标题",
		"edits":     []any{map[string]any{"find": "第二句。", "replace": "改过的第二句。"}, map[string]any{"find": "不存在", "replace": "x"}},
		"cover":     map[string]any{"prompt": "new cover"},
		"images":    []any{map[string]any{"no": float64(1), "caption": "新图注"}, map[string]any{"no": float64(9), "prompt": "越界"}},
		"shots":     []any{map[string]any{"no": float64(1), "voice": "新口播", "seconds": float64(0), "prompt": "new shot"}},
		"coverText": "旧字",
	}
	receipts, errs, redraw := applyRevision(p, assets, revise, true)
	if p.Title != "新标题" || !strings.Contains(p.Article, "改过的第二句") {
		t.Fatalf("标题或脚本没改：%+v", p)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "没找到") {
		t.Fatalf("应报一处没找到：%v", errs)
	}
	if assets[0].Prompt != "new cover" || assets[1].Caption != "新图注" || assets[2].Voice != "新口播" || assets[2].Seconds != 1 {
		t.Fatalf("画面没改对：%+v %+v %+v", assets[0], assets[1], assets[2])
	}
	if len(redraw) != 2 {
		t.Fatalf("应重画封面和第 1 镜：%d", len(redraw))
	}
	if len(receipts) < 4 {
		t.Fatalf("回执太少：%v", receipts)
	}
	// 整篇重写优先于 edits，且去掉一级标题
	p2 := &Production{Article: "x"}
	applyRevision(p2, nil, map[string]any{"article": "# 标题\n新的全文", "edits": []any{map[string]any{"find": "x", "replace": "y"}}}, false)
	if p2.Article != "新的全文" {
		t.Fatalf("整篇重写不对：%q", p2.Article)
	}
}

func TestApplyInputSchedule(t *testing.T) {
	when := "2026-11-26T20:00"
	empty := ""
	idea := Idea{Title: "x", Status: "idea"}
	if err := applyInput(&idea, ideaInput{ScheduledAt: &when}, false, "now"); err != nil || idea.Status != "outline" {
		t.Fatalf("未成稿排期应变大纲：%v %s", err, idea.Status)
	}
	idea2 := Idea{Title: "x", Status: "draft"}
	_ = applyInput(&idea2, ideaInput{ScheduledAt: &when}, true, "now")
	if idea2.Status != "ready" {
		t.Fatalf("已成稿排期应变待发布：%s", idea2.Status)
	}
	_ = applyInput(&idea2, ideaInput{ScheduledAt: &empty}, true, "now")
	if idea2.Status != "draft" || idea2.ScheduledAt != "" {
		t.Fatalf("取消排期应退回草稿：%+v", idea2)
	}
	pub := "published"
	_ = applyInput(&idea2, ideaInput{Status: &pub}, true, "T")
	if idea2.PublishedAt != "T" {
		t.Fatal("发布应记下时间")
	}
	bad := "明天"
	if err := applyInput(&idea2, ideaInput{ScheduledAt: &bad}, true, "T"); err == nil {
		t.Fatal("非法排期时间应报错")
	}
	plat := "火星台"
	if err := applyInput(&idea2, ideaInput{Platform: &plat}, true, "T"); err == nil {
		t.Fatal("未知平台应报错")
	}
	body := "只写了正文没有标题的灵感"
	idea3 := Idea{Status: "idea"}
	if err := applyInput(&idea3, ideaInput{Body: &body}, false, "T"); err != nil || idea3.Title == "" {
		t.Fatalf("没有标题时用正文补：%v %+v", err, idea3)
	}
}

func TestRoutePathAndSegs(t *testing.T) {
	cases := map[string]string{
		"/ideas": "/ideas", "/ideas/it_1/": "/ideas/it_1", "/plugins/external/media_desk/api/today": "/today",
		"/api/settings": "/settings", "/": "/",
	}
	for in, want := range cases {
		if got := routePath(in); got != want {
			t.Errorf("routePath(%q)=%q 应为 %q", in, got, want)
		}
	}
	if segs("/ideas/../x") != nil || segs("/ideas/a b") != nil {
		t.Fatal("非法 id 段应拒绝")
	}
	if p := segs("/ideas/it_1/assets/as_2/draw"); len(p) != 5 {
		t.Fatalf("拆段不对：%v", p)
	}
}

func TestRunAgentErrors(t *testing.T) {
	f := &fakeHost{agent: func(_, _ string) (string, error) { return "", rpcErrorf("permission_denied", "x") }}
	s := newTestServer(f)
	if _, err := s.runAgent(context.Background(), "s", "p"); err == nil || !strings.Contains(err.Error(), "agent:run") {
		t.Fatalf("权限错误要提示 agent:run：%v", err)
	}
	f.agent = func(_, _ string) (string, error) { return "", rpcErrorf("method_not_found", "x") }
	if _, err := s.runAgent(context.Background(), "s", "p"); err == nil || !strings.Contains(err.Error(), "升级") {
		t.Fatalf("旧宿主要提示升级：%v", err)
	}
	f.agent = func(_, _ string) (string, error) { return "  好的  ", nil }
	if out, err := s.runAgent(context.Background(), "s", "p"); err != nil || out != "好的" {
		t.Fatalf("正常返回不对：%q %v", out, err)
	}
	// 参数里带 system 和超时
	last := f.calls[len(f.calls)-1]
	if last.Params["system"] != "s" || last.Params["timeout_ms"].(float64) != 240000 {
		t.Fatalf("参数不对：%v", last.Params)
	}
}

var pngBytes = append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, make([]byte, 32)...)

func TestGenerateImageB64AndURL(t *testing.T) {
	f := &fakeHost{}
	s := newTestServer(f)
	cfg := ImageConfig{BaseURL: "https://img.example.com/v1/", Model: "gpt-image-1", Size: "1024x1024"}
	f.http = func(method, url string, headers map[string]string, body []byte) (*httpResult, error) {
		if url != "https://img.example.com/v1/images/generations" || headers["Authorization"] != "Bearer sk-1" {
			t.Fatalf("请求地址或鉴权不对：%s %v", url, headers)
		}
		var b map[string]any
		_ = json.Unmarshal(body, &b)
		if b["model"] != "gpt-image-1" || b["size"] != "1024x1024" {
			t.Fatalf("请求体不对：%v", b)
		}
		resp, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(pngBytes)}}})
		return &httpResult{Status: 200, body: resp}, nil
	}
	data, mime, err := s.generateImage(context.Background(), cfg, "sk-1", "a cat")
	if err != nil || mime != "image/png" || len(data) != len(pngBytes) {
		t.Fatalf("b64 解析失败：%v %s", err, mime)
	}
	// 返回相对地址：同源下载带 Key
	var got []string
	f.http = func(method, url string, headers map[string]string, body []byte) (*httpResult, error) {
		got = append(got, method+" "+url+" "+headers["Authorization"])
		if method == "POST" {
			return &httpResult{Status: 200, body: []byte(`{"data":[{"url":"/files/abc.png"}]}`)}, nil
		}
		return &httpResult{Status: 200, body: pngBytes}, nil
	}
	if _, _, err := s.generateImage(context.Background(), cfg, "sk-1", "a cat"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != "GET https://img.example.com/files/abc.png Bearer sk-1" {
		t.Fatalf("下载地址不对：%v", got)
	}
}

func TestGenerateImageErrors(t *testing.T) {
	retryWaits = []time.Duration{time.Millisecond, time.Millisecond}
	f := &fakeHost{}
	s := newTestServer(f)
	cfg := ImageConfig{BaseURL: "https://img.example.com/v1", Model: "m"}
	n := 0
	f.http = func(string, string, map[string]string, []byte) (*httpResult, error) {
		n++
		if n < 3 {
			return &httpResult{Status: 429, body: []byte(`{"error":{"message":"slow down sk-secret"}}`)}, nil
		}
		return &httpResult{Status: 200, body: []byte(`{"images":[{"b64_json":"` + base64.StdEncoding.EncodeToString(pngBytes) + `"}]}`)}, nil
	}
	if _, _, err := s.generateImage(context.Background(), cfg, "sk-secret", "p"); err != nil || n != 3 {
		t.Fatalf("429 应重试后成功：%v n=%d", err, n)
	}
	f.http = func(string, string, map[string]string, []byte) (*httpResult, error) {
		return &httpResult{Status: 401, body: []byte(`{"error":{"message":"bad key sk-secret"}}`)}, nil
	}
	_, _, err := s.generateImage(context.Background(), cfg, "sk-secret", "p")
	if err == nil || !strings.Contains(err.Error(), "Key 无效") || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("401 提示不对或泄露了 Key：%v", err)
	}
	f.http = func(string, string, map[string]string, []byte) (*httpResult, error) {
		return &httpResult{Status: 200, body: []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte("<html>")) + `"}]}`)}, nil
	}
	if _, _, err := s.generateImage(context.Background(), cfg, "k", "p"); err == nil || !strings.Contains(err.Error(), "不是图片") {
		t.Fatalf("非图片应拒绝：%v", err)
	}
	// 没授权 network:any
	f.http = nil
	if _, _, err := s.generateImage(context.Background(), cfg, "k", "p"); err != errNoNetwork {
		t.Fatalf("应提示联网权限：%v", err)
	}
	if _, err := imageEndpoint("ftp://x"); err == nil {
		t.Fatal("非 http 地址应拒绝")
	}
	if e, _ := imageEndpoint("https://a.com/v1/images/generations/"); e != "https://a.com/v1/images/generations" {
		t.Fatalf("完整端点不该重复拼接：%s", e)
	}
	big := append([]byte{0xff, 0xd8}, make([]byte, maxImageBytes)...)
	if _, err := checkImage(big); err == nil {
		t.Fatal("超过上限应拒绝")
	}
}

func findExec(calls []fakeCall, prefix string) []fakeCall {
	var out []fakeCall
	for _, c := range calls {
		if strings.HasPrefix(strings.TrimSpace(c.Params["sql"].(string)), prefix) {
			out = append(out, c)
		}
	}
	return out
}

func TestRunProductionTextOnly(t *testing.T) {
	step := 0
	f := &fakeHost{agent: func(system, prompt string) (string, error) {
		step++
		if step == 1 {
			if !strings.Contains(system, "分镜师") {
				t.Fatalf("第一次应调策划：%s", system)
			}
			return planJSON, nil
		}
		if !strings.Contains(system, "「## 区别」") {
			t.Fatalf("正文应沿用策划小标题：%s", system)
		}
		return "# 多余的标题\n开场第一句。\n\n## 开场\n正文。", nil
	}}
	s := newTestServer(f)
	idea := Idea{ID: "it_1", Title: "Agent 和聊天机器人", Status: "idea"}
	st := Settings{ImageCount: 2, ShotCount: 6, DraftFormat: "script"}
	s.runProduction(context.Background(), idea, st, voiceInput{}, "script", false)
	execs := f.execSQL()
	if n := len(findExec(execs, "INSERT INTO assets")); n != 1+2+6 {
		t.Fatalf("应插入 1 封面 + 2 配图 + 6 分镜（缺的用模板补）：%d", n)
	}
	done := findExec(execs, "UPDATE productions SET status = 'done'")
	if len(done) != 1 {
		t.Fatal("应把成稿标成完成")
	}
	args := done[0].Params["args"].([]any)
	if article := args[0].(string); strings.Contains(article, "多余的标题") || !strings.HasPrefix(article, "开场第一句") {
		t.Fatalf("正文应去掉一级标题：%q", article)
	}
	if notice := args[2].(string); notice != "" {
		t.Fatalf("正常成稿不该有提示：%q", notice)
	}
	if len(findExec(execs, "UPDATE ideas SET status")) != 1 {
		t.Fatal("应把灵感推进到草稿")
	}
}

func TestRunProductionModelUnavailable(t *testing.T) {
	f := &fakeHost{} // agent 为空：模型不可用
	s := newTestServer(f)
	idea := Idea{ID: "it_2", Title: "没有模型也要能走完", Body: "补充说明"}
	s.runProduction(context.Background(), idea, Settings{ImageCount: 1, ShotCount: 3}, voiceInput{}, "script", true)
	execs := f.execSQL()
	done := findExec(execs, "UPDATE productions SET status = 'done'")
	if len(done) != 1 {
		t.Fatal("模型不可用也应完成并给结构草稿")
	}
	args := done[0].Params["args"].([]any)
	if !strings.Contains(args[0].(string), "结构草稿") || !strings.Contains(args[2].(string), "没有连上文字模型") || !strings.Contains(args[2].(string), "生图接口") {
		t.Fatalf("降级提示不对：%v", args)
	}
	// 只调一次模型（策划失败后正文不再等一次超时）
	agentCalls := 0
	for _, c := range f.calls {
		if c.Method == "host.agent.run" {
			agentCalls++
		}
	}
	if agentCalls != 1 {
		t.Fatalf("模型调用次数应为 1：%d", agentCalls)
	}
}

func TestRunChatAppliesRevision(t *testing.T) {
	f := &fakeHost{
		agent: func(system, prompt string) (string, error) {
			if !strings.Contains(prompt, "用户说：标题短一点") || !strings.Contains(prompt, `"title":"很长很长的旧标题"`) {
				t.Fatalf("改稿提示词不对：%s", prompt)
			}
			return `{"reply":"标题改短了","revise":{"title":"短标题","shots":[{"no":1,"voice":"新口播"}]}}`, nil
		},
		query: func(sql string, args []any) []map[string]any {
			switch {
			case strings.HasPrefix(sql, "SELECT * FROM productions"):
				return []map[string]any{{"idea_id": "it_1", "status": "done", "format": "script", "title": "很长很长的旧标题", "article": "正文", "facts": "[]", "outline": "[]"}}
			case strings.HasPrefix(sql, "SELECT id, idea_id, kind"):
				return []map[string]any{
					{"id": "c", "idea_id": "it_1", "kind": "cover", "no": float64(0), "prompt": "cover"},
					{"id": "s1", "idea_id": "it_1", "kind": "shot", "no": float64(1), "voice": "旧口播", "seconds": float64(5)},
				}
			}
			return nil
		},
	}
	s := newTestServer(f)
	reply := s.runChat(context.Background(), "it_1", "标题短一点")
	if !strings.Contains(reply, "标题改短了") || !strings.Contains(reply, "标题改成《短标题》") || !strings.Contains(reply, "改了第 1 镜的文案") {
		t.Fatalf("回复不对：%s", reply)
	}
	execs := f.execSQL()
	rev := findExec(execs, "INSERT INTO revisions")
	if len(rev) != 1 || !strings.Contains(rev[0].Params["args"].([]any)[2].(string), "很长很长的旧标题") {
		t.Fatal("改之前应存旧版快照")
	}
	up := findExec(execs, "UPDATE productions SET title")
	if len(up) != 1 || up[0].Params["args"].([]any)[0] != "短标题" {
		t.Fatal("应写回新标题")
	}
	saved := findExec(execs, "UPDATE assets SET caption")
	ok := false
	for _, c := range saved {
		if c.Params["args"].([]any)[4] == "新口播" {
			ok = true
		}
	}
	if !ok {
		t.Fatal("应写回分镜口播")
	}
}

func TestInsightsAndProposals(t *testing.T) {
	ms := []Metric{
		{ID: "1", IdeaID: "a", Title: "A", Platform: "小红书", Date: "2026-11-20", Views: 10000, Likes: 500},
		{ID: "2", IdeaID: "b", Title: "B", Platform: "小红书", Date: "2026-11-21", Views: 8000, Likes: 300},
		{ID: "3", IdeaID: "c", Title: "C", Platform: "抖音", Date: "2026-11-22", Views: 2000, Likes: 50},
		{ID: "4", IdeaID: "", Platform: "B站", Date: "2026-10-01", Views: 100},
	}
	hours := map[string]int{"a": 20, "b": 20, "c": 9}
	edits := []editSignal{{Before: 1000, After: 600}, {Before: 1000, After: 700}}
	ins := buildInsights(ms, hours, edits, time.Date(2026, 11, 25, 12, 0, 0, 0, time.Local))
	if ins.Posts != 4 || ins.Views != 20100 || ins.Engagement != 850 {
		t.Fatalf("汇总不对：%+v", ins)
	}
	if ins.Platforms[0].Platform != "小红书" || ins.Top[0].Title != "A" {
		t.Fatalf("排序不对：%+v %+v", ins.Platforms, ins.Top)
	}
	if len(ins.Daily) != 30 || ins.Daily[29].Date != "2026-11-25" || ins.Daily[26].Views != 2000 {
		t.Fatalf("每日趋势不对：%+v", ins.Daily[25:])
	}
	names := []string{}
	for _, p := range ins.Proposals {
		names = append(names, p.Name)
	}
	joined := strings.Join(names, "|")
	for _, want := range []string{"首发放在小红书", "20 点前后发布", "初稿写短一些"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少提案 %s：%v", want, names)
		}
	}
	// 样本不够不提案
	if p := proposeRules(ms[:1], nil, edits[:1]); len(p) != 0 {
		t.Fatalf("样本不够不该提案：%v", p)
	}
}

func TestSeedPresets(t *testing.T) {
	f := &fakeHost{}
	s := newTestServer(f)
	if err := s.seedPresets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(findExec(f.execSQL(), "INSERT OR IGNORE INTO styles")); n != 15 {
		t.Fatalf("应写入 15 个预设：%d", n)
	}
	f2 := &fakeHost{query: func(sql string, _ []any) []map[string]any {
		if strings.Contains(sql, "FROM kv") {
			return []map[string]any{{"value": "1"}}
		}
		return nil
	}}
	s2 := newTestServer(f2)
	_ = s2.seedPresets(context.Background())
	if len(f2.execSQL()) != 0 {
		t.Fatal("写过一次就不再写")
	}
}

func TestSettingsKeyGoesToSecrets(t *testing.T) {
	f := &fakeHost{}
	s := newTestServer(f)
	_, err := s.apiSaveSettings(context.Background(), []byte(`{"image_base_url":"https://api.example.com/v1","image_model":"m","image_key":" sk-abc "}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.secret != "sk-abc" {
		t.Fatalf("Key 应存进 host.secrets：%q", f.secret)
	}
	for _, c := range f.execSQL() {
		if strings.Contains(jsonText(c.Params), "sk-abc") {
			t.Fatal("Key 不应写进私有库")
		}
	}
	if _, err := s.apiSaveSettings(context.Background(), []byte(`{"clear_key":true}`)); err != nil || f.secret != "" {
		t.Fatalf("清除 Key 失败：%v", err)
	}
	if _, err := s.apiSaveSettings(context.Background(), []byte(`{"image_base_url":"file:///etc"}`)); err == nil {
		t.Fatal("非法地址应拒绝")
	}
}

func TestHTTPRequestEnvelope(t *testing.T) {
	f := &fakeHost{}
	s := newTestServer(f)
	raw, _ := json.Marshal(map[string]any{"method": "GET", "path": "/nope", "query": ""})
	out, err := s.handleHTTPRequest(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["status"] != 404 {
		t.Fatalf("未知接口应 404：%v", m)
	}
	raw, _ = json.Marshal(map[string]any{"method": "POST", "path": "/ideas", "body_b64": base64.StdEncoding.EncodeToString([]byte(`{"title":"一句话灵感","tags":["#AI","AI",""]}`))})
	out, _ = s.handleHTTPRequest(context.Background(), raw)
	m = out.(map[string]any)
	body, _ := base64.StdEncoding.DecodeString(m["body_b64"].(string))
	if m["status"] != 200 || !strings.Contains(string(body), `"tags":["AI"]`) {
		t.Fatalf("新建灵感不对：%v %s", m["status"], body)
	}
}

func TestUpdatePerms(t *testing.T) {
	s := newTestServer(&fakeHost{})
	s.updatePerms(json.RawMessage(`{"permissions":["agent:run","network:any"],"granted":["agent:run"]}`))
	if known, granted := s.networkGranted(); !known || granted {
		t.Fatal("activate 应按 granted 判断，network:any 未授予")
	}
	s.updatePerms(json.RawMessage(`{"permissions":["agent:run","network:any"]}`))
	if _, granted := s.networkGranted(); !granted {
		t.Fatal("permission_changed 的 permissions 是已授予集合")
	}
}
