// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

// 联网测试（开发用，默认跳过）：直连真实的 OpenAI 兼容图像接口，验证请求格式和响应解析。
// 只在 go test 里用标准库直连，不经过 PMW，也不进插件运行时（运行时出网一律走 host.http.request）。
//
//	LIVE=1 MD_IMAGE_BASE_URL=https://api.openai.com/v1 MD_IMAGE_MODEL=gpt-image-1 MD_IMAGE_KEY=sk-... go test -run Live -v

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// liveHost 把 host.http.request 换成真实请求，其余方法不支持。
type liveHost struct{}

func (liveHost) call(ctx context.Context, method string, params, out any) error {
	if method != "host.http.request" {
		return rpcErrorf("method_not_found", "联网测试只模拟 host.http.request")
	}
	raw, _ := json.Marshal(params)
	var p struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		BodyB64 string            `json:"body_b64"`
	}
	_ = json.Unmarshal(raw, &p)
	body, _ := base64.StdEncoding.DecodeString(p.BodyB64)
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	b, _ := json.Marshal(map[string]any{"status": res.StatusCode, "body_b64": base64.StdEncoding.EncodeToString(data)})
	return json.Unmarshal(b, out)
}

func TestLiveImageGeneration(t *testing.T) {
	if os.Getenv("LIVE") != "1" || os.Getenv("MD_IMAGE_KEY") == "" {
		t.Skip("设置 LIVE=1 和 MD_IMAGE_BASE_URL / MD_IMAGE_MODEL / MD_IMAGE_KEY 后才跑")
	}
	s := newTestServer(&fakeHost{})
	s.host = liveHost{}
	cfg := ImageConfig{BaseURL: os.Getenv("MD_IMAGE_BASE_URL"), Model: os.Getenv("MD_IMAGE_MODEL"), Size: os.Getenv("MD_IMAGE_SIZE")}
	data, mime, err := s.generateImage(context.Background(), cfg, os.Getenv("MD_IMAGE_KEY"), imagePrompt("shot", "a person writing on a night bus", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("生成成功：%s，%d 字节", mime, len(data))
}
