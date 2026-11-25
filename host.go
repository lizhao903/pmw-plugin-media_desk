// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

// 插件生命周期与宿主能力封装：host.store / host.agent.run / host.http.request / host.secrets。
// 所有出网一律经 host.http.request（需要用户在界面上单独授予 network:any），不直连外网。

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

const secretScope = "media_desk"

// caller 是对宿主发起调用的最小接口；生产用 *rpcConn，单元测试用假实现。
type caller interface {
	call(ctx context.Context, method string, params, out any) error
}

type server struct {
	host      caller
	log       *slog.Logger
	base      context.Context
	cancel    context.CancelFunc
	startedAt time.Time

	mu       sync.Mutex
	jobs     map[string]*prodJob // 正在成稿的灵感
	chats    map[string]*chatJob // 正在改稿的对话
	drawing  map[string]bool     // 正在单独重画的画面
	learn    learnJob            // 风格提炼
	perms    map[string]bool     // 宿主告知的已授予权限
	permsSet bool

	now func() time.Time
}

func newServer(log *slog.Logger, base context.Context, cancel context.CancelFunc) *server {
	return &server{
		log: log, base: base, cancel: cancel, startedAt: time.Now(),
		jobs: map[string]*prodJob{}, chats: map[string]*chatJob{}, drawing: map[string]bool{},
		perms: map[string]bool{}, now: time.Now,
	}
}

func (s *server) handlers() map[string]handlerFunc {
	return map[string]handlerFunc{
		"plugin.activate":           s.handleActivate,
		"plugin.deactivate":         s.handleDeactivate,
		"plugin.health":             s.handleHealth,
		"plugin.http.request":       s.handleHTTPRequest,
		"plugin.permission_changed": s.handlePermissionChanged,
	}
}

func (s *server) handleActivate(ctx context.Context, raw json.RawMessage) (any, error) {
	s.log.Info("plugin.activate", "plugin", pluginID)
	s.updatePerms(raw)
	// activate 不做重活：收尾上次中断的任务、补种风格预设放到后台
	go func() {
		bg, cancel := context.WithTimeout(s.bgCtx(), 30*time.Second)
		defer cancel()
		s.settleInterrupted(bg)
		if err := s.seedPresets(bg); err != nil {
			s.log.Warn("写入风格预设失败（迁移未执行？）", "err", err)
		}
	}()
	return map[string]any{"ok": true}, nil
}

func (s *server) handleDeactivate(_ context.Context, _ json.RawMessage) (any, error) {
	s.log.Info("plugin.deactivate，准备退出")
	if s.cancel != nil {
		s.cancel()
	}
	return map[string]any{"ok": true}, nil
}

func (s *server) handleHealth(_ context.Context, _ json.RawMessage) (any, error) {
	return map[string]any{
		"status": "ok",
		"detail": map[string]any{
			"plugin": pluginID, "version": pluginVer,
			"pid": os.Getpid(), "uptime_ms": time.Since(s.startedAt).Milliseconds(),
		},
	}, nil
}

func (s *server) handlePermissionChanged(_ context.Context, raw json.RawMessage) (any, error) {
	s.updatePerms(raw)
	return nil, nil
}

// updatePerms 记下宿主给的权限集合（activate / permission_changed 都带 permissions）。
// 只用于界面提示，真正的授权由宿主 broker 在调用时判断。
func (s *server) updatePerms(raw json.RawMessage) {
	// plugin.activate 带 permissions（清单声明）和 granted（已授予）；plugin.permission_changed 的 permissions 就是已授予集合
	var p struct {
		Permissions []string  `json:"permissions"`
		Granted     *[]string `json:"granted"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return
	}
	list := p.Permissions
	if p.Granted != nil {
		list = *p.Granted
	} else if list == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.perms = map[string]bool{}
	for _, x := range list {
		s.perms[x] = true
	}
	s.permsSet = true
}

// networkKnown 返回（是否已知，是否已授予 network:any）。
func (s *server) networkGranted() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.permsSet, s.perms["network:any"]
}

func (s *server) bgCtx() context.Context {
	if s.base != nil {
		return s.base
	}
	return context.Background()
}

func (s *server) nowISO() string { return s.now().Format(time.RFC3339) }

func newID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "_" + strings.ToLower(fmt.Sprintf("%x", time.Now().UnixNano()&0xffffffffff)) + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// 私有库
// ---------------------------------------------------------------------------

func (s *server) query(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if args == nil {
		args = []any{}
	}
	var out struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := s.host.call(callCtx, "host.store.query", map[string]any{"sql": sql, "args": args}, &out); err != nil {
		return nil, err
	}
	return out.Rows, nil
}

func (s *server) exec(ctx context.Context, sql string, args ...any) error {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if args == nil {
		args = []any{}
	}
	var out map[string]any
	return s.host.call(callCtx, "host.store.exec", map[string]any{"sql": sql, "args": args}, &out)
}

func (s *server) kvGet(ctx context.Context, key string) string {
	rows, err := s.query(ctx, "SELECT value FROM kv WHERE key = ?", key)
	if err != nil || len(rows) == 0 {
		return ""
	}
	return str(rows[0], "value")
}

func (s *server) kvSet(ctx context.Context, key, value string) error {
	return s.exec(ctx, "INSERT INTO kv(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
}

func str(row map[string]any, key string) string {
	switch v := row[key].(type) {
	case string:
		return v
	case nil:
		return ""
	case float64:
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprint(v)
	}
}

func intOf(row map[string]any, key string) int {
	switch v := row[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func jsonList(raw string) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// 模型：host.agent.run（需要 agent:run；单轮补全，不带工具）
// ---------------------------------------------------------------------------

const agentTimeout = 240 * time.Second

func (s *server) runAgent(ctx context.Context, system, prompt string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, agentTimeout+20*time.Second)
	defer cancel()
	params := map[string]any{"system": system, "prompt": prompt, "timeout_ms": int(agentTimeout / time.Millisecond)}
	var out struct {
		Result  string `json:"result"`
		Summary string `json:"summary"`
	}
	if err := s.host.call(callCtx, "host.agent.run", params, &out); err != nil {
		var rerr *rpcError
		if errors.As(err, &rerr) {
			switch rerr.Code {
			case errorCodeNumbers["permission_denied"]:
				return "", fmt.Errorf("插件还没有调用模型的权限：到「设置 → 插件 → 外部插件」确认 agent:run")
			case errorCodeNumbers["method_not_found"]:
				return "", fmt.Errorf("当前工作台版本不支持插件调用模型（host.agent.run），需要升级工作台")
			}
			if strings.Contains(rerr.Message, "模型") {
				return "", errors.New(rerr.Message)
			}
			return "", fmt.Errorf("模型调用失败：%s", rerr.Message)
		}
		return "", fmt.Errorf("模型调用失败：%v", err)
	}
	text := strings.TrimSpace(out.Result)
	if text == "" {
		text = strings.TrimSpace(out.Summary)
	}
	if text == "" {
		return "", fmt.Errorf("模型返回了空内容")
	}
	return text, nil
}

// ---------------------------------------------------------------------------
// 出网：host.http.request（需要 network:any；非流式、单次响应上限 8 MB）
// ---------------------------------------------------------------------------

// httpTimeout 必须小于宿主上限 2 分钟，否则宿主回落到默认 30 秒。
const httpTimeout = 115 * time.Second

type httpResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_b64"`
	body    []byte
}

var errNoNetwork = errors.New("插件还没有联网权限：生图要访问你填的图像接口，请在「设置 → 插件 → 外部插件」给内容工作台单独授予 network:any")

func (s *server) httpDo(ctx context.Context, method, url string, headers map[string]string, body []byte) (*httpResult, error) {
	params := map[string]any{"method": method, "url": url, "timeout_ms": int(httpTimeout / time.Millisecond)}
	if len(headers) > 0 {
		params["headers"] = headers
	}
	if len(body) > 0 {
		params["body_b64"] = base64.StdEncoding.EncodeToString(body)
	}
	callCtx, cancel := context.WithTimeout(ctx, httpTimeout+10*time.Second)
	defer cancel()
	var out httpResult
	if err := s.host.call(callCtx, "host.http.request", params, &out); err != nil {
		var rerr *rpcError
		if errors.As(err, &rerr) && rerr.Code == errorCodeNumbers["permission_denied"] {
			return nil, errNoNetwork
		}
		return nil, err
	}
	out.body, _ = base64.StdEncoding.DecodeString(out.BodyB64)
	return &out, nil
}

// ---------------------------------------------------------------------------
// 凭证：host.secrets（scope media_desk，只存生图接口的 Key）
// ---------------------------------------------------------------------------

func (s *server) secretGet(ctx context.Context) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out struct {
		Value string `json:"value"`
	}
	if err := s.host.call(callCtx, "host.secrets.get", map[string]any{"scope": secretScope}, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Value), nil
}

func (s *server) secretSet(ctx context.Context, value string) error {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out map[string]any
	if strings.TrimSpace(value) == "" {
		return s.host.call(callCtx, "host.secrets.delete", map[string]any{"scope": secretScope}, &out)
	}
	return s.host.call(callCtx, "host.secrets.set", map[string]any{"scope": secretScope, "value": strings.TrimSpace(value)}, &out)
}
