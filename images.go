// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

// 可选生图：任何兼容 OpenAI Images API（POST {base}/images/generations）的服务都能用，
// 由用户在设置里填地址、模型和 Key（Key 存 host.secrets）。请求经 host.http.request 发出，
// 需要用户单独授予 network:any；没授权或没配置时，文字功能照常可用。
// 解析与错误说明改写自上游 src/images.js（MIT）。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// maxImageBytes 单张图片上限。宿主单条 RPC 消息 8 MB，base64 膨胀 4/3，
// 存库和回传前端都要过一次 RPC，所以解码后限制在 4 MB 以内。
const maxImageBytes = 4 << 20

var retryWaits = []time.Duration{6 * time.Second, 14 * time.Second}

func encodeB64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// sniffImage 只认真实图片，返回 MIME。
func sniffImage(b []byte) string {
	switch {
	case len(b) > 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G':
		return "image/png"
	case len(b) > 3 && b[0] == 0xff && b[1] == 0xd8:
		return "image/jpeg"
	case len(b) > 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case len(b) > 6 && string(b[0:3]) == "GIF":
		return "image/gif"
	}
	return ""
}

// imageEndpoint 用户可能贴完整端点，也可能只贴到 /v1。
func imageEndpoint(base string) (string, error) {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(b)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("图像接口地址要以 http:// 或 https:// 开头")
	}
	if strings.HasSuffix(b, "/images/generations") {
		return b, nil
	}
	return b + "/images/generations", nil
}

func redact(text, key string) string {
	t := cut(text, 300)
	if key != "" {
		t = strings.ReplaceAll(t, key, "***")
	}
	return t
}

type imgHTTPError struct {
	status int
	msg    string
}

func (e *imgHTTPError) Error() string { return e.msg }

func explainStatus(status int, detail string) string {
	switch {
	case status == 401 || status == 403:
		return fmt.Sprintf("Key 无效或没有这个模型的权限（%d）", status)
	case status == 404:
		return "接口地址或模型不对（404）"
	case status == 429:
		return "请求太频繁或额度不足（429）"
	case detail != "":
		return fmt.Sprintf("生图服务返回 %d：%s", status, detail)
	}
	return fmt.Sprintf("生图服务返回 %d", status)
}

// imageURLCandidate 把返回里的图片地址拼成可下载的绝对地址（可能是相对路径）。
func imageURLCandidate(raw, base string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false
	}
	bu, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/") + "/")
	if err != nil {
		return "", false
	}
	u, err := bu.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return u.String(), true
}

func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

// parseImageResponse 读出 b64_json，或者给出要下载的地址。OpenAI 系放在 data[0]，部分国内服务放在 images[0]。
func parseImageResponse(body []byte) ([]byte, string, error) {
	var data struct {
		Data   []map[string]any `json:"data"`
		Images []map[string]any `json:"images"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, "", errors.New("生图服务返回的不是 JSON")
	}
	var entry map[string]any
	if len(data.Data) > 0 {
		entry = data.Data[0]
	} else if len(data.Images) > 0 {
		entry = data.Images[0]
	}
	if entry == nil {
		return nil, "", errors.New("生图服务没有返回图片")
	}
	if b64, _ := entry["b64_json"].(string); b64 != "" {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, "", errors.New("生图服务返回的图片编码不对")
		}
		return raw, "", nil
	}
	if u, _ := entry["url"].(string); u != "" {
		return nil, u, nil
	}
	return nil, "", errors.New("生图服务没有返回图片")
}

func checkImage(raw []byte) (string, error) {
	if len(raw) > maxImageBytes {
		return "", fmt.Errorf("图片超过 %d MB，换小一点的尺寸或质量", maxImageBytes>>20)
	}
	mime := sniffImage(raw)
	if mime == "" {
		return "", errors.New("返回的内容不是图片")
	}
	return mime, nil
}

// generateImage 生成一张图；限流或 5xx 时等一会儿再试，最多再试两次。
func (s *server) generateImage(ctx context.Context, cfg ImageConfig, key, prompt string) ([]byte, string, error) {
	var last error
	for attempt := 0; ; attempt++ {
		data, mime, err := s.requestImage(ctx, cfg, key, prompt)
		if err == nil {
			return data, mime, nil
		}
		last = err
		var he *imgHTTPError
		retry := errors.As(err, &he) && (he.status == 429 || he.status >= 500)
		if !retry || attempt >= len(retryWaits) || ctx.Err() != nil {
			return nil, "", last
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(retryWaits[attempt]):
		}
	}
}

func (s *server) requestImage(ctx context.Context, cfg ImageConfig, key, prompt string) ([]byte, string, error) {
	endpoint, err := imageEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, "", err
	}
	body := map[string]any{"model": strings.TrimSpace(cfg.Model), "prompt": prompt, "n": 1}
	if v := strings.TrimSpace(cfg.Size); v != "" {
		body["size"] = v
	}
	if v := strings.TrimSpace(cfg.Quality); v != "" {
		body["quality"] = v
	}
	payload, _ := json.Marshal(body)
	headers := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + key}
	res, err := s.httpDo(ctx, "POST", endpoint, headers, payload)
	if err != nil {
		return nil, "", err
	}
	if res.Status < 200 || res.Status >= 300 {
		var e struct {
			Error   any    `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(res.body, &e)
		detail := e.Message
		if m, ok := e.Error.(map[string]any); ok {
			if msg, ok := m["message"].(string); ok {
				detail = msg
			}
		} else if msg, ok := e.Error.(string); ok {
			detail = msg
		}
		return nil, "", &imgHTTPError{status: res.Status, msg: explainStatus(res.Status, redact(detail, key))}
	}
	raw, link, err := parseImageResponse(res.body)
	if err != nil {
		return nil, "", err
	}
	if link != "" {
		href, ok := imageURLCandidate(link, cfg.BaseURL)
		if !ok {
			return nil, "", fmt.Errorf("生图服务返回的图片地址无法识别：%s", cut(link, 120))
		}
		h := map[string]string{}
		// 跟接口同源的地址是用户自己的服务，下载也带上同一个 Key
		if sameOrigin(href, endpoint) {
			h["Authorization"] = "Bearer " + key
		}
		dl, err := s.httpDo(ctx, "GET", href, h, nil)
		if err != nil {
			return nil, "", err
		}
		if dl.Status < 200 || dl.Status >= 300 {
			return nil, "", &imgHTTPError{status: dl.Status, msg: fmt.Sprintf("下载图片失败（%d）", dl.Status)}
		}
		raw = dl.body
	}
	mime, err := checkImage(raw)
	if err != nil {
		return nil, "", err
	}
	return bytes.Clone(raw), mime, nil
}
