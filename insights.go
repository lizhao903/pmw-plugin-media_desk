// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

package main

// 复盘：发布后手动回填数据，按平台、内容、日期汇总，并从数据和改稿习惯里提出写作规则建议。
// 规则提案的思路移植自上游 src/learn.js（MIT）：样本够了才提案，不凑数；确认后才写进规则。

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Metric 是一条回填的数据。
type Metric struct {
	ID       string `json:"id"`
	IdeaID   string `json:"idea_id"`
	Title    string `json:"title"`
	Platform string `json:"platform"`
	Date     string `json:"date"`
	Views    int    `json:"views"`
	Likes    int    `json:"likes"`
	Comments int    `json:"comments"`
	Shares   int    `json:"shares"`
	Saves    int    `json:"saves"`
}

func (m Metric) engagement() int { return m.Likes + m.Comments + m.Shares + m.Saves }

type platformStat struct {
	Platform   string `json:"platform"`
	N          int    `json:"n"`
	Views      int    `json:"views"`
	Engagement int    `json:"engagement"`
}

type topItem struct {
	IdeaID     string  `json:"idea_id"`
	Title      string  `json:"title"`
	Platform   string  `json:"platform"`
	Views      int     `json:"views"`
	Engagement int     `json:"engagement"`
	Rate       float64 `json:"rate"`
}

type dayStat struct {
	Date       string `json:"date"`
	Views      int    `json:"views"`
	Engagement int    `json:"engagement"`
}

// Proposal 是一条待确认的写作规则建议。
type Proposal struct {
	Name     string `json:"name"`
	Rule     string `json:"rule"`
	Evidence string `json:"evidence"`
}

// Insights 是复盘页的汇总。
type Insights struct {
	Posts      int            `json:"posts"`
	Views      int            `json:"views"`
	Engagement int            `json:"engagement"`
	Rate       float64        `json:"rate"`
	Platforms  []platformStat `json:"platforms"`
	Top        []topItem      `json:"top"`
	Daily      []dayStat      `json:"daily"`
	Proposals  []Proposal     `json:"proposals"`
}

// editSignal 是一次「AI 初稿 → 你改完」的长度比例。
type editSignal struct {
	Before int
	After  int
}

func rate(views, eng int) float64 {
	if views <= 0 {
		return 0
	}
	return float64(eng) / float64(views)
}

// buildInsights 汇总数据。publishHours 是每条内容的发布小时（idea_id -> hour），edits 是改稿长度信号。
func buildInsights(metrics []Metric, publishHours map[string]int, edits []editSignal, now time.Time) Insights {
	out := Insights{Platforms: []platformStat{}, Top: []topItem{}, Daily: []dayStat{}, Proposals: []Proposal{}}
	posts := map[string]bool{}
	plat := map[string]*platformStat{}
	items := map[string]*topItem{}
	days := map[string]*dayStat{}
	for _, m := range metrics {
		e := m.engagement()
		out.Views += m.Views
		out.Engagement += e
		key := m.IdeaID
		if key == "" {
			key = "m:" + m.ID
		}
		posts[key] = true
		ps := plat[m.Platform]
		if ps == nil {
			ps = &platformStat{Platform: m.Platform}
			plat[m.Platform] = ps
		}
		ps.N++
		ps.Views += m.Views
		ps.Engagement += e
		if m.IdeaID != "" {
			it := items[m.IdeaID]
			if it == nil {
				it = &topItem{IdeaID: m.IdeaID, Title: m.Title, Platform: m.Platform}
				items[m.IdeaID] = it
			}
			it.Views += m.Views
			it.Engagement += e
		}
		d := m.Date
		if len(d) > 10 {
			d = d[:10]
		}
		ds := days[d]
		if ds == nil {
			ds = &dayStat{Date: d}
			days[d] = ds
		}
		ds.Views += m.Views
		ds.Engagement += e
	}
	out.Posts = len(posts)
	out.Rate = rate(out.Views, out.Engagement)
	for _, p := range plat {
		out.Platforms = append(out.Platforms, *p)
	}
	sort.Slice(out.Platforms, func(i, j int) bool {
		if out.Platforms[i].Views != out.Platforms[j].Views {
			return out.Platforms[i].Views > out.Platforms[j].Views
		}
		return out.Platforms[i].Engagement > out.Platforms[j].Engagement
	})
	hasViews := out.Views > 0
	for _, it := range items {
		it.Rate = rate(it.Views, it.Engagement)
		out.Top = append(out.Top, *it)
	}
	sort.Slice(out.Top, func(i, j int) bool {
		if hasViews && out.Top[i].Views != out.Top[j].Views {
			return out.Top[i].Views > out.Top[j].Views
		}
		return out.Top[i].Engagement > out.Top[j].Engagement
	})
	if len(out.Top) > 5 {
		out.Top = out.Top[:5]
	}
	// 最近 30 天，每天一格，没数据补 0
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -29)
	for i := 0; i < 30; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		if ds := days[d]; ds != nil {
			out.Daily = append(out.Daily, *ds)
		} else {
			out.Daily = append(out.Daily, dayStat{Date: d})
		}
	}
	out.Proposals = proposeRules(metrics, publishHours, edits)
	return out
}

func fmtNum(n float64) string {
	v := int(n + 0.5)
	s := fmt.Sprintf("%d", v)
	if v < 10000 {
		return s
	}
	return fmt.Sprintf("%.1f 万", float64(v)/10000)
}

type group struct {
	key string
	n   int
	avg float64
}

func rankGroups(values map[string][]int) []group {
	var gs []group
	for k, vs := range values {
		if k == "" || len(vs) == 0 {
			continue
		}
		sum := 0
		for _, v := range vs {
			sum += v
		}
		gs = append(gs, group{key: k, n: len(vs), avg: float64(sum) / float64(len(vs))})
	}
	sort.Slice(gs, func(i, j int) bool {
		if gs[i].avg != gs[j].avg {
			return gs[i].avg > gs[j].avg
		}
		return gs[i].key < gs[j].key
	})
	return gs
}

// proposeRules 只在样本足够时提案。
func proposeRules(metrics []Metric, publishHours map[string]int, edits []editSignal) []Proposal {
	out := []Proposal{}
	// 每篇内容（按 idea + 平台）的累计数据
	type acc struct {
		views, eng int
		platform   string
	}
	perItem := map[string]*acc{}
	for _, m := range metrics {
		if m.IdeaID == "" {
			continue
		}
		a := perItem[m.IdeaID]
		if a == nil {
			a = &acc{platform: m.Platform}
			perItem[m.IdeaID] = a
		}
		a.views += m.Views
		a.eng += m.engagement()
	}
	hasViews := false
	for _, a := range perItem {
		if a.views > 0 {
			hasViews = true
		}
	}
	unit := "播放"
	score := func(a *acc) int {
		if hasViews {
			return a.views
		}
		return a.eng
	}
	if !hasViews {
		unit = "互动"
	}

	byPlatform := map[string][]int{}
	for _, a := range perItem {
		byPlatform[a.platform] = append(byPlatform[a.platform], score(a))
	}
	if gs := rankGroups(byPlatform); len(gs) >= 2 && gs[0].avg > gs[1].avg*1.3 && gs[0].avg > 0 {
		out = append(out, Proposal{
			Name:     "首发放在" + gs[0].key,
			Rule:     "同一选题优先在" + gs[0].key + "首发，再改写分发到其他平台。",
			Evidence: fmt.Sprintf("%s平均 %s %s，高于第二名%s的 %s", gs[0].key, fmtNum(gs[0].avg), unit, gs[1].key, fmtNum(gs[1].avg)),
		})
	}

	byHour := map[string][]int{}
	for id, a := range perItem {
		if h, ok := publishHours[id]; ok {
			byHour[fmt.Sprintf("%d", h)] = append(byHour[fmt.Sprintf("%d", h)], score(a))
		}
	}
	if gs := rankGroups(byHour); len(gs) >= 2 && gs[0].n >= 2 {
		out = append(out, Proposal{
			Name:     gs[0].key + " 点前后发布",
			Rule:     "默认把内容排在 " + gs[0].key + ":00 前后发布。",
			Evidence: fmt.Sprintf("这个时段 %d 篇平均 %s %s", gs[0].n, fmtNum(gs[0].avg), unit),
		})
	}

	if len(edits) >= 2 {
		sum := 0.0
		for _, e := range edits {
			if e.Before > 0 {
				sum += float64(e.After) / float64(e.Before)
			} else {
				sum++
			}
		}
		avg := sum / float64(len(edits))
		if avg < 0.85 {
			out = append(out, Proposal{Name: "初稿写短一些", Rule: fmt.Sprintf("你通常把 AI 初稿删到原来的 %d%%，以后初稿控制得更紧凑。", int(avg*100+0.5)), Evidence: fmt.Sprintf("来自 %d 次改稿", len(edits))})
		} else if avg > 1.15 {
			out = append(out, Proposal{Name: "初稿写得更充实", Rule: fmt.Sprintf("你通常把 AI 初稿扩写到原来的 %d%%，以后初稿多给细节和例子。", int(avg*100+0.5)), Evidence: fmt.Sprintf("来自 %d 次改稿", len(edits))})
		}
	}
	return out
}

func charCount(s string) int {
	return len([]rune(strings.Join(strings.Fields(s), "")))
}
