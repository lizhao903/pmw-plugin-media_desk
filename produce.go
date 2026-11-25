// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

// 一键成稿流水线（移植自上游 src/produce.js）：策划案 → 口播脚本 / 文章 + 封面、配图、分镜图同时开工。
// 宿主转发前端请求只等 60 秒，模型可能要几分钟，所以做成后台任务：POST 发起、前端轮询详情。
// 关掉页面不会中断；插件停用时任务取消，下次启用时把没做完的标成「被中断」。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const imageConcurrency = 2

type prodJob struct {
	cancel context.CancelFunc
}

type chatJob struct {
	startedAt string
}

func (s *server) isProducing(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id] != nil
}

func (s *server) isChatting(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chats[id] != nil
}

// imageReady 生图是否可用：配置齐全、有 Key。联网权限在真正请求时由宿主判断。
func (s *server) imageReady(ctx context.Context, cfg ImageConfig) (string, bool) {
	if !cfg.configured() {
		return "", false
	}
	key, err := s.secretGet(ctx)
	if err != nil || key == "" {
		return "", false
	}
	return key, true
}

// startProduction 发起一键成稿；已有成稿会被替换。
func (s *server) startProduction(ctx context.Context, id, format string, withImages *bool) error {
	idea, err := s.loadIdea(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.jobs[id] != nil {
		s.mu.Unlock()
		return errors.New("这条灵感正在成稿")
	}
	jobCtx, cancel := context.WithCancel(s.bgCtx())
	s.jobs[id] = &prodJob{cancel: cancel}
	s.mu.Unlock()

	st := s.loadSettings(ctx)
	if format != "article" && format != "script" {
		format = st.DraftFormat
	}
	draw := st.GenerateImages
	if withImages != nil {
		draw = *withImages
	}
	voice := s.loadVoice(ctx, st.ActiveStyleID)
	styleName := ""
	if voice.Style != nil {
		styleName = voice.Style.Name
	}
	now := s.nowISO()
	fail := func(e error) error {
		s.mu.Lock()
		delete(s.jobs, id)
		s.mu.Unlock()
		cancel()
		return e
	}
	// 重新成稿：旧画面、撤销记录一起清掉（对话保留）
	if err := s.exec(ctx, "DELETE FROM assets WHERE idea_id = ?", id); err != nil {
		return fail(err)
	}
	_ = s.exec(ctx, "DELETE FROM revisions WHERE idea_id = ?", id)
	_ = s.exec(ctx, "DELETE FROM images WHERE idea_id = ?", id)
	if err := s.exec(ctx, `INSERT INTO productions(idea_id, status, stage, format, with_images, title, cover_text, article, ai_article, facts, outline, characters, style_name, notice, error, started_at, finished_at, updated_at)
		VALUES (?, 'running', 'plan', ?, ?, ?, '', '', '', '[]', '[]', '', ?, '', '', ?, '', ?)
		ON CONFLICT(idea_id) DO UPDATE SET status = 'running', stage = 'plan', format = excluded.format, with_images = excluded.with_images,
		title = excluded.title, cover_text = '', article = '', ai_article = '', facts = '[]', outline = '[]', characters = '',
		style_name = excluded.style_name, notice = '', error = '', started_at = excluded.started_at, finished_at = '', updated_at = excluded.updated_at`,
		id, format, boolInt(draw), idea.Title, styleName, now, now); err != nil {
		return fail(err)
	}
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.jobs, id)
			s.mu.Unlock()
			cancel()
		}()
		s.runProduction(jobCtx, idea, st, voice, format, draw)
	}()
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *server) cancelProduction(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[id]; j != nil {
		j.cancel()
		return true
	}
	return false
}

func (s *server) runProduction(ctx context.Context, idea Idea, st Settings, voice voiceInput, format string, draw bool) {
	// 落库用不随任务取消的上下文，取消后也能把状态写回去
	db := context.WithoutCancel(ctx)
	id := idea.ID
	backup := fallbackPlan(idea, st.ImageCount, st.ShotCount)

	var plan Plan
	var modelErr error
	sys, user := planPrompts(idea, voice, st.ImageCount, st.ShotCount)
	text, err := s.runAgent(ctx, sys, user)
	if err == nil {
		var raw map[string]any
		raw, err = extractJSON(text)
		if err == nil {
			plan = normalizePlan(raw, st.ImageCount, st.ShotCount, idea.Title)
		}
	}
	if ctx.Err() != nil {
		s.finishCancelled(db, id)
		return
	}
	if err != nil {
		modelErr = err
		plan = backup
	}
	// 模型漏掉配图或分镜时用模板补上
	if len(plan.Images) < st.ImageCount {
		plan.Images = append(plan.Images, backup.Images[len(plan.Images):st.ImageCount]...)
	}
	if len(plan.Shots) < st.ShotCount {
		plan.Shots = append(plan.Shots, backup.Shots[len(plan.Shots):st.ShotCount]...)
	}

	key, ready := "", false
	notice := ""
	if draw {
		key, ready = s.imageReady(db, st.Image)
		if !ready {
			notice = "还没配置生图接口，这次只出文字版；在设置里填好图像接口后可以单张补画。"
		} else if known, granted := s.networkGranted(); known && !granted {
			ready = false
			notice = "插件还没有 network:any 联网权限，这次只出文字版；授权后可以单张补画。"
		}
	}
	initial := "idle"
	if ready {
		initial = "pending"
	}
	if err := s.exec(db, `UPDATE productions SET stage = 'writing', title = ?, cover_text = ?, facts = ?, outline = ?, characters = ?, notice = ?, updated_at = ? WHERE idea_id = ?`,
		plan.Title, plan.CoverText, jsonText(plan.Facts), jsonText(plan.Outline), plan.Characters, notice, s.nowISO(), id); err != nil {
		s.finishFailed(db, id, err)
		return
	}
	assets := planAssets(id, plan, initial)
	for _, a := range assets {
		if err := s.exec(db, `INSERT INTO assets(id, idea_id, kind, no, caption, anchor, shot, visual, voice, seconds, prompt, status, error, image_id, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?)`,
			a.ID, id, a.Kind, a.No, a.Caption, a.Anchor, a.Shot, a.Visual, a.Voice, a.Seconds, a.Prompt, a.Status, s.nowISO()); err != nil {
			s.finishFailed(db, id, err)
			return
		}
	}

	var wg sync.WaitGroup
	var article string
	var articleErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		if modelErr != nil {
			// 策划案都没连上模型，正文就不再等一次超时
			articleErr = modelErr
			return
		}
		sys, user := articlePrompts(idea, voice, plan, format)
		out, err := s.runAgent(ctx, sys, user)
		if err != nil {
			articleErr = err
			return
		}
		article = stripTitle(out)
		if article == "" {
			articleErr = errors.New("模型返回了空内容")
		}
	}()
	if ready {
		queue := make(chan *Asset, len(assets))
		for _, a := range assets {
			queue <- a
		}
		close(queue)
		for i := 0; i < imageConcurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for a := range queue {
					if ctx.Err() != nil {
						return
					}
					s.renderAsset(ctx, st.Image, key, a, plan.Characters)
				}
			}()
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		s.finishCancelled(db, id)
		return
	}
	if articleErr != nil {
		article = fallbackArticle(idea)
		msg := "没有连上文字模型（" + articleErr.Error() + "），先给你一份结构草稿，可以直接改，也可以稍后重新成稿。"
		if notice != "" {
			msg += notice
		}
		notice = msg
	}
	ai := article
	if articleErr != nil {
		ai = ""
	}
	_ = s.exec(db, `UPDATE productions SET status = 'done', stage = 'done', article = ?, ai_article = ?, notice = ?, error = '', finished_at = ?, updated_at = ? WHERE idea_id = ?`,
		article, ai, notice, s.nowISO(), s.nowISO(), id)
	// 灵感、大纲状态推进到草稿（已排期的推进到待发布）
	next := "draft"
	if idea.ScheduledAt != "" {
		next = "ready"
	}
	_ = s.exec(db, "UPDATE ideas SET status = ?, updated_at = ? WHERE id = ? AND status IN ('idea', 'outline')", next, s.nowISO(), id)
}

func (s *server) finishCancelled(ctx context.Context, id string) {
	_ = s.exec(ctx, "UPDATE productions SET status = 'cancelled', stage = 'done', error = '', updated_at = ? WHERE idea_id = ?", s.nowISO(), id)
	_ = s.exec(ctx, "UPDATE assets SET status = 'idle' WHERE idea_id = ? AND status IN ('pending', 'running')", id)
}

func (s *server) finishFailed(ctx context.Context, id string, err error) {
	_ = s.exec(ctx, "UPDATE productions SET status = 'failed', stage = 'done', error = ?, updated_at = ? WHERE idea_id = ?", err.Error(), s.nowISO(), id)
	_ = s.exec(ctx, "UPDATE assets SET status = 'idle' WHERE idea_id = ? AND status IN ('pending', 'running')", id)
}

// settleInterrupted 插件重启后，没人在跑的「进行中」标成中断，卡片不会一直转圈。
func (s *server) settleInterrupted(ctx context.Context) {
	rows, err := s.query(ctx, "SELECT idea_id FROM productions WHERE status = 'running'")
	if err != nil {
		return
	}
	for _, r := range rows {
		id := str(r, "idea_id")
		if s.isProducing(id) {
			continue
		}
		_ = s.exec(ctx, "UPDATE productions SET status = 'failed', stage = 'done', error = '上次成稿被中断了，可以重新开始', updated_at = ? WHERE idea_id = ?", s.nowISO(), id)
	}
	_ = s.exec(ctx, "UPDATE assets SET status = 'idle' WHERE status IN ('pending', 'running')")
}

func planAssets(ideaID string, plan Plan, status string) []*Asset {
	out := []*Asset{{ID: newID("as"), IdeaID: ideaID, Kind: "cover", No: 0, Prompt: plan.Cover, Status: status}}
	for i, img := range plan.Images {
		out = append(out, &Asset{ID: newID("as"), IdeaID: ideaID, Kind: "image", No: i + 1, Caption: img.Caption, Anchor: img.Anchor, Prompt: img.Prompt, Status: status})
	}
	for i, sh := range plan.Shots {
		out = append(out, &Asset{ID: newID("as"), IdeaID: ideaID, Kind: "shot", No: i + 1, Shot: sh.Shot, Visual: sh.Visual, Voice: sh.Voice, Seconds: sh.Seconds, Prompt: sh.Prompt, Status: status})
	}
	return out
}

// renderAsset 画一格：成功存图并把画面指过去，失败记原因。
func (s *server) renderAsset(ctx context.Context, cfg ImageConfig, key string, a *Asset, characters string) {
	db := context.WithoutCancel(ctx)
	_ = s.setAssetStatus(db, a.ID, "running", "")
	data, mime, err := s.generateImage(ctx, cfg, key, imagePrompt(a.Kind, a.Prompt, a.Visual, characters))
	if ctx.Err() != nil {
		_ = s.setAssetStatus(db, a.ID, "idle", "")
		return
	}
	if err != nil {
		_ = s.setAssetStatus(db, a.ID, "failed", err.Error())
		return
	}
	imgID := newID("img")
	if err := s.exec(db, "INSERT INTO images(id, idea_id, mime, bytes, data_b64, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		imgID, a.IdeaID, mime, len(data), encodeB64(data), s.nowISO()); err != nil {
		_ = s.setAssetStatus(db, a.ID, "failed", "保存图片失败："+err.Error())
		return
	}
	_ = s.exec(db, "UPDATE assets SET status = 'done', error = '', image_id = ?, updated_at = ? WHERE id = ?", imgID, s.nowISO(), a.ID)
	s.cleanupImages(db, a.IdeaID)
}

// redrawAsset 单独重画一格。
func (s *server) redrawAsset(ctx context.Context, ideaID, assetID string) error {
	a, err := s.loadAsset(ctx, ideaID, assetID)
	if err != nil {
		return err
	}
	st := s.loadSettings(ctx)
	key, ready := s.imageReady(ctx, st.Image)
	if !ready {
		return errors.New("还没配置生图接口：到设置里填写图像接口地址、模型和 Key")
	}
	if known, granted := s.networkGranted(); known && !granted {
		return errNoNetwork
	}
	s.mu.Lock()
	if s.drawing[assetID] {
		s.mu.Unlock()
		return errors.New("这格正在画")
	}
	s.drawing[assetID] = true
	s.mu.Unlock()
	prod, _ := s.loadProduction(ctx, ideaID)
	characters := ""
	if prod != nil {
		characters = prod.Characters
	}
	_ = s.setAssetStatus(ctx, assetID, "running", "")
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.drawing, assetID)
			s.mu.Unlock()
		}()
		s.renderAsset(s.bgCtx(), st.Image, key, a, characters)
	}()
	return nil
}

// ---------------------------------------------------------------------------
// 左聊右改
// ---------------------------------------------------------------------------

// applyRevision 把对话里的修改落到成稿上（移植自上游 applyRevision）。
// 模型输出不可信：只认这几个字段，序号越界的直接丢弃。返回回执、错误和需要重画的画面。
func applyRevision(p *Production, assets []*Asset, revise map[string]any, canDraw bool) (receipts, errs []string, redraw []*Asset) {
	redrawText := func(what string) string {
		if canDraw {
			return "重画" + what
		}
		return "更新了" + what + "的画面描述（配好生图接口后可以重画）"
	}
	if t := anyText(revise["title"], 80); t != "" && t != p.Title {
		p.Title = t
		receipts = append(receipts, "标题改成《"+t+"》")
	}
	if t := anyText(revise["coverText"], 24); t != "" && t != p.CoverText {
		p.CoverText = t
		receipts = append(receipts, "封面大字改成「"+t+"」")
	}
	if art, ok := revise["article"].(string); ok && strings.TrimSpace(art) != "" {
		p.Article = cut(stripTitle(art), 200000)
		receipts = append(receipts, "重写了脚本")
	} else if edits, ok := revise["edits"].([]any); ok {
		changed := 0
		for i, x := range edits {
			if i >= 20 {
				break
			}
			m, _ := x.(map[string]any)
			find, _ := m["find"].(string)
			repl, ok := m["replace"].(string)
			if find == "" || !ok {
				continue
			}
			if strings.Contains(p.Article, find) {
				p.Article = strings.Replace(p.Article, find, repl, 1)
				changed++
			} else {
				errs = append(errs, "脚本里没找到「"+cut(find, 16)+"」")
			}
		}
		if changed > 0 {
			receipts = append(receipts, fmt.Sprintf("改了脚本的 %d 处", changed))
		}
	}
	byKind := func(kind string, no int) *Asset {
		for _, a := range assets {
			if a.Kind == kind && a.No == no {
				return a
			}
		}
		return nil
	}
	if cover, ok := revise["cover"].(map[string]any); ok {
		if pr := anyText(cover["prompt"], 1200); pr != "" {
			if a := byKind("cover", 0); a != nil {
				a.Prompt = pr
				redraw = append(redraw, a)
				receipts = append(receipts, redrawText("封面"))
			}
		}
	}
	if imgs, ok := revise["images"].([]any); ok {
		for i, x := range imgs {
			if i >= 8 {
				break
			}
			m, _ := x.(map[string]any)
			no := anyInt(m["no"], 0)
			a := byKind("image", no)
			if a == nil {
				continue
			}
			if c := anyText(m["caption"], 40); c != "" {
				a.Caption = c
			}
			if pr := anyText(m["prompt"], 1200); pr != "" {
				a.Prompt = pr
				redraw = append(redraw, a)
				receipts = append(receipts, redrawText(fmt.Sprintf("配图 %d", no)))
			} else {
				receipts = append(receipts, fmt.Sprintf("改了配图 %d 的图注", no))
			}
		}
	}
	if shots, ok := revise["shots"].([]any); ok {
		for i, x := range shots {
			if i >= 24 {
				break
			}
			m, _ := x.(map[string]any)
			no := anyInt(m["no"], 0)
			a := byKind("shot", no)
			if a == nil {
				continue
			}
			if v := anyText(m["shot"], 12); v != "" {
				a.Shot = v
			}
			if v := anyText(m["visual"], 200); v != "" {
				a.Visual = v
			}
			if v := anyText(m["voice"], 200); v != "" {
				a.Voice = v
			}
			if sec, ok := m["seconds"].(float64); ok {
				a.Seconds = clampSeconds(int(sec + 0.5))
			}
			if pr := anyText(m["prompt"], 1200); pr != "" {
				a.Prompt = pr
				redraw = append(redraw, a)
				receipts = append(receipts, redrawText(fmt.Sprintf("第 %d 镜", no)))
			} else {
				receipts = append(receipts, fmt.Sprintf("改了第 %d 镜的文案", no))
			}
		}
	}
	return receipts, errs, redraw
}

func productionBrief(p *Production, assets []*Asset) ProductionBrief {
	b := ProductionBrief{Title: p.Title, Format: p.Format, CoverText: p.CoverText, Article: p.Article, Images: []map[string]any{}, Shots: []map[string]any{}}
	for _, a := range assets {
		switch a.Kind {
		case "cover":
			b.Cover = a.Prompt
		case "image":
			b.Images = append(b.Images, map[string]any{"no": a.No, "caption": a.Caption, "prompt": a.Prompt})
		case "shot":
			b.Shots = append(b.Shots, map[string]any{"no": a.No, "shot": a.Shot, "visual": a.Visual, "voice": a.Voice, "seconds": a.Seconds, "prompt": a.Prompt})
		}
	}
	return b
}

// snapshot 撤销用：成稿文字 + 每格画面的文字和图片指向。
type snapshot struct {
	Title     string   `json:"title"`
	CoverText string   `json:"cover_text"`
	Article   string   `json:"article"`
	Assets    []*Asset `json:"assets"`
}

const maxRevisions = 20

func (s *server) pushRevision(ctx context.Context, ideaID, label string, p *Production, assets []*Asset) error {
	snap := snapshot{Title: p.Title, CoverText: p.CoverText, Article: p.Article, Assets: assets}
	raw, _ := json.Marshal(snap)
	if err := s.exec(ctx, "INSERT INTO revisions(idea_id, label, snapshot, created_at) VALUES (?, ?, ?, ?)", ideaID, label, string(raw), s.nowISO()); err != nil {
		return err
	}
	_ = s.exec(ctx, `DELETE FROM revisions WHERE idea_id = ? AND id NOT IN (SELECT id FROM revisions WHERE idea_id = ? ORDER BY id DESC LIMIT ?)`, ideaID, ideaID, maxRevisions)
	return nil
}

// undo 撤回最近一次修改，返回这次撤回的说明。
func (s *server) undo(ctx context.Context, ideaID string) (string, error) {
	rows, err := s.query(ctx, "SELECT id, label, snapshot FROM revisions WHERE idea_id = ? ORDER BY id DESC LIMIT 1", ideaID)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", errors.New("没有可以撤销的修改")
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(str(rows[0], "snapshot")), &snap); err != nil {
		return "", errors.New("撤销记录损坏")
	}
	if err := s.exec(ctx, "UPDATE productions SET title = ?, cover_text = ?, article = ?, updated_at = ? WHERE idea_id = ?", snap.Title, snap.CoverText, snap.Article, s.nowISO(), ideaID); err != nil {
		return "", err
	}
	for _, a := range snap.Assets {
		if a.ImageID != "" {
			if r, _ := s.query(ctx, "SELECT id FROM images WHERE id = ?", a.ImageID); len(r) == 0 {
				a.ImageID = ""
			}
		}
		if a.Status == "running" || a.Status == "pending" {
			a.Status = "idle"
		}
		if a.ImageID != "" {
			a.Status = "done"
		}
		_ = s.saveAssetText(ctx, a)
	}
	_ = s.exec(ctx, "DELETE FROM revisions WHERE id = ?", intOf(rows[0], "id"))
	s.cleanupImages(ctx, ideaID)
	return str(rows[0], "label"), nil
}

// startChat 记下用户的话，后台调模型改稿。
func (s *server) startChat(ctx context.Context, ideaID, message string) error {
	message = cut(message, 2000)
	if message == "" {
		return errors.New("说点什么再发送")
	}
	prod, err := s.loadProduction(ctx, ideaID)
	if err != nil {
		return err
	}
	if prod == nil || prod.Status != "done" {
		return errors.New("先一键成稿，再让助手改")
	}
	s.mu.Lock()
	if s.chats[ideaID] != nil {
		s.mu.Unlock()
		return errors.New("助手还在改上一句")
	}
	s.chats[ideaID] = &chatJob{startedAt: s.nowISO()}
	s.mu.Unlock()
	if err := s.exec(ctx, "INSERT INTO chats(idea_id, role, text, created_at) VALUES (?, 'user', ?, ?)", ideaID, message, s.nowISO()); err != nil {
		s.mu.Lock()
		delete(s.chats, ideaID)
		s.mu.Unlock()
		return err
	}
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.chats, ideaID)
			s.mu.Unlock()
		}()
		reply := s.runChat(s.bgCtx(), ideaID, message)
		_ = s.exec(context.WithoutCancel(s.bgCtx()), "INSERT INTO chats(idea_id, role, text, created_at) VALUES (?, 'assistant', ?, ?)", ideaID, reply, s.nowISO())
	}()
	return nil
}

func (s *server) runChat(ctx context.Context, ideaID, message string) string {
	db := context.WithoutCancel(ctx)
	prod, err := s.loadProduction(db, ideaID)
	if err != nil || prod == nil {
		return "改稿失败：读不到成稿"
	}
	assets, err := s.loadAssets(db, ideaID)
	if err != nil {
		return "改稿失败：读不到画面"
	}
	st := s.loadSettings(db)
	voice := s.loadVoice(db, st.ActiveStyleID)
	var history []map[string]string
	if rows, err := s.query(db, "SELECT role, text FROM chats WHERE idea_id = ? ORDER BY id DESC LIMIT 9", ideaID); err == nil {
		// 最新一条是刚发的这句，不重复放进历史
		for i := len(rows) - 1; i >= 1; i-- {
			history = append(history, map[string]string{"role": str(rows[i], "role"), "text": cut(str(rows[i], "text"), 400)})
		}
	}
	sys, user := revisePrompts(productionBrief(prod, assets), voice, history, message)
	text, err := s.runAgent(ctx, sys, user)
	if err != nil {
		return "改稿失败：" + err.Error()
	}
	raw, err := extractJSON(text)
	if err != nil {
		// 模型没按格式回，就把原话当回复
		return cut(text, 600)
	}
	reply := anyText(raw["reply"], 300)
	revise, _ := raw["revise"].(map[string]any)
	if len(revise) == 0 {
		if reply == "" {
			reply = "这次没有需要改的地方。"
		}
		return reply
	}
	// 改之前存快照，可以撤销
	before := cloneAssets(assets)
	beforeProd := *prod
	key, ready := s.imageReady(db, st.Image)
	if known, granted := s.networkGranted(); known && !granted {
		ready = false
	}
	receipts, errs, redraw := applyRevision(prod, assets, revise, ready)
	if len(receipts) == 0 {
		if reply == "" {
			reply = "没改成。"
		}
		if len(errs) > 0 {
			reply += "（" + strings.Join(errs, "；") + "）"
		}
		return reply
	}
	if err := s.pushRevision(db, ideaID, cut("对话："+message, 40), &beforeProd, before); err != nil {
		return "改稿失败：保存撤销记录出错（" + err.Error() + "）"
	}
	if err := s.exec(db, "UPDATE productions SET title = ?, cover_text = ?, article = ?, updated_at = ? WHERE idea_id = ?", prod.Title, prod.CoverText, prod.Article, s.nowISO(), ideaID); err != nil {
		return "改稿失败：" + err.Error()
	}
	for _, a := range assets {
		_ = s.saveAssetText(db, a)
	}
	if ready {
		for _, a := range redraw {
			a := a
			_ = s.setAssetStatus(db, a.ID, "pending", "")
			go s.renderAsset(s.bgCtx(), st.Image, key, a, prod.Characters)
		}
	}
	out := reply
	if out == "" {
		out = "改好了。"
	}
	out += "\n" + strings.Join(receipts, "；")
	if len(errs) > 0 {
		out += "\n" + strings.Join(errs, "；")
	}
	return out
}

func cloneAssets(in []*Asset) []*Asset {
	out := make([]*Asset, len(in))
	for i, a := range in {
		c := *a
		out[i] = &c
	}
	return out
}
