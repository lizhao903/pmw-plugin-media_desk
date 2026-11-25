// powered by dsh - https://github.com/deepseek-ai/deepseek-harness

/**
 * 内容工作台插件前端入口（清单 v2 / 宿主 API 2.0.0）。
 * 只用 JS 语法子集（类型走 JSDoc），零构建即可作为 ESM 运行（make web 复制成 entry.js）。
 * 数据走同源 /api/plugins/external/media_desk/api/*，由宿主转发到插件后端。
 * 一个模块页（灵感 / 内容 / 排期 / 复盘 / 风格五个视图 + 成稿工作区）、一张今日卡片、一个设置分节。
 * 界面交互参考 dsh-media-desk（MIT，woniuniuniu）。
 */
import { Component, createElement as h, useEffect, useRef, useState } from "react";

const PLUGIN_ID = "media_desk";
const PLUGIN_NAME = "内容工作台";
const API_RANGE = ">=2.0.0 <3.0.0";
const API_BASE = "/api/plugins/external/" + PLUGIN_ID + "/api";

const STATUS = [["idea", "灵感"], ["outline", "大纲"], ["draft", "草稿"], ["ready", "待发布"], ["published", "已发布"]];
const STATUS_LABEL = {};
STATUS.forEach(function (s) { STATUS_LABEL[s[0]] = s[1]; });
const DEFAULT_PLATFORMS = ["小红书", "抖音", "B站", "公众号", "视频号", "快手", "微博", "知乎", "TikTok", "YouTube", "Instagram", "X"];
const VIEWS = [["ideas", "灵感"], ["content", "内容"], ["schedule", "排期"], ["insights", "复盘"], ["style", "风格"]];
const WEEK = ["一", "二", "三", "四", "五", "六", "日"];

/** @type {Array<{dispose?: () => void}>} */
const disposables = [];
let CTX = null;

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

/** @param {string} path @param {RequestInit} [init] @returns {Promise<any>} */
async function api(path, init) {
  const res = await fetch(API_BASE + path, init);
  const data = await res.json().catch(function () { return {}; });
  if (!res.ok) throw new Error((data && data.error) || ("HTTP " + res.status));
  return data;
}
function send(path, method, body) {
  return api(path, { method: method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body || {}) });
}
function errText(err) { return String((err && err.message) || err); }
function imgURL(id) { return API_BASE + "/images/" + encodeURIComponent(id); }
function pad2(n) { return String(n).padStart(2, "0"); }
function ymd(d) { return d.getFullYear() + "-" + pad2(d.getMonth() + 1) + "-" + pad2(d.getDate()); }
function isNarrow() { return typeof window !== "undefined" && window.matchMedia && window.matchMedia("(max-width: 640px)").matches; }
function fmtWhen(s) {
  if (!s) return "未排期";
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(s);
  return m ? Number(m[2]) + "月" + Number(m[3]) + "日 " + m[4] + ":" + m[5] : s;
}
function fmtNum(n) {
  n = Number(n) || 0;
  return n >= 10000 ? (n / 10000).toFixed(1) + " 万" : String(n);
}
function pct(v) { return ((Number(v) || 0) * 100).toFixed(1) + "%"; }
function displayTitle(it) { return (it && (it.prod_title || it.title)) || "未命名"; }
async function copyText(text) {
  try { await navigator.clipboard.writeText(text); return true; } catch (e) { return false; }
}

// ---------------------------------------------------------------------------
// 主题：跟随宿主 <html data-theme>，没有再跟系统
// ---------------------------------------------------------------------------

function hostTheme() {
  const t = document.documentElement.getAttribute("data-theme");
  if (t === "dark" || t === "light") return t;
  return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
function useTheme() {
  const [theme, setTheme] = useState(hostTheme());
  useEffect(function () {
    const f = function () { setTheme(hostTheme()); };
    const ob = new MutationObserver(f);
    ob.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    const mq = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
    if (mq && mq.addEventListener) mq.addEventListener("change", f);
    return function () { ob.disconnect(); if (mq && mq.removeEventListener) mq.removeEventListener("change", f); };
  }, []);
  return theme;
}
function ThemeRoot(props) {
  const theme = useTheme();
  return h("div", { className: "md-root", "data-md-theme": theme }, props.children);
}

class ErrorBoundary extends Component {
  constructor(props) { super(props); this.state = { error: null }; }
  static getDerivedStateFromError(error) { return { error: error }; }
  componentDidCatch(error, info) { console.error("[pmw-plugin] 组件异常", PLUGIN_ID, error, info); }
  render() {
    if (this.state.error) {
      return h("div", { className: "md-error", role: "alert" }, h("strong", null, "插件界面出错了"),
        h("p", null, String((this.state.error && this.state.error.message) || this.state.error)));
    }
    return this.props.children;
  }
}
function guarded(Inner) {
  return function Guarded(props) { return h(ThemeRoot, null, h(ErrorBoundary, null, h(Inner, props))); };
}

/** 标题行：宿主有 ctx.ui 时放进顶栏，否则页面内画。actions: [{ label, onClick, disabled, primary, title }] */
function TitleBar(props) {
  const ui = CTX && CTX.ui;
  const actions = (props.actions || []).filter(Boolean);
  if (ui && ui.PageTitle && ui.Button) {
    return h(ui.PageTitle, {
      parent: props.parent, desc: props.desc, tabs: props.tabs,
      actions: actions.length ? h("span", { style: { display: "inline-flex", gap: "8px", alignItems: "center" } }, actions.map(function (a) {
        return h(ui.Button, { key: a.label, size: "sm", variant: a.primary ? "primary" : "ghost", disabled: a.disabled, title: a.title, onClick: a.onClick }, a.label);
      })) : null,
    }, props.title);
  }
  return h("header", { className: "md-header" },
    h("div", null, h("h2", null, props.parent ? props.parent + " / " + props.title : props.title),
      props.desc ? h("p", { className: "md-hint" }, props.desc) : null),
    props.tabs || null,
    h("div", { className: "md-bar" }, actions.map(function (a) {
      return h("button", { key: a.label, className: "md-btn" + (a.primary ? " md-primary" : ""), type: "button", disabled: a.disabled, title: a.title, onClick: a.onClick }, a.label);
    })));
}

/** 分段按钮组（可能渲染进宿主顶栏、在插件样式作用域之外，所以用行内样式） */
const SEG_WRAP = { display: "inline-flex", alignSelf: "flex-start", flexWrap: "wrap", gap: "2px", padding: "3px", borderRadius: "8px", background: "var(--md-seg-bg, var(--hover, rgba(127,127,127,0.12)))" };
function segBtn(on) {
  return {
    padding: "3px 12px", border: 0, borderRadius: "6px", background: on ? "var(--md-seg-on, var(--panel, #fff))" : "transparent",
    color: on ? "inherit" : "var(--md-muted, var(--muted, inherit))", font: "inherit", fontSize: "13px", fontWeight: on ? 600 : 400,
    cursor: "pointer", opacity: on ? 1 : 0.8, boxShadow: on ? "0 1px 2px rgba(0,0,0,0.08)" : "none",
  };
}
function Seg(props) {
  return h("div", { style: SEG_WRAP, role: "radiogroup", "aria-label": props.label }, props.options.map(function (o) {
    return h("button", { key: o[0], type: "button", role: "radio", "aria-checked": props.value === o[0], style: segBtn(props.value === o[0]),
      onClick: function () { props.onChange(o[0]); } }, o[1]);
  }));
}

function Field(props) {
  return h("label", { className: "md-field" + (props.wide ? " md-wide" : "") }, h("span", null, props.label), props.children);
}
function Note(props) {
  return props.text ? h("p", { className: "md-note" + (props.kind ? " md-note-" + props.kind : ""), role: props.kind === "bad" ? "alert" : "status" }, props.text) : null;
}
function Empty(props) {
  return h("div", { className: "md-empty" }, h("strong", null, props.title), props.text ? h("p", null, props.text) : null, props.children || null);
}
function StatusPill(props) {
  return h("span", { className: "md-pill md-s-" + props.status }, STATUS_LABEL[props.status] || props.status);
}

/** 生图是否可用：后端状态 + 宿主权限。 */
function imageState(st) {
  if (!st) return { ok: false, reason: "" };
  const perm = st.network_known ? st.network_granted : Boolean(CTX && CTX.permissions && CTX.permissions.has && CTX.permissions.has("network:any"));
  if (!st.image_configured || !st.image_has_key) return { ok: false, reason: "还没配置生图接口：到「设置 → 插件 → 内容工作台」填写 OpenAI 兼容图像接口的地址、模型和 Key。文字功能照常可用。" };
  if (!perm) return { ok: false, reason: "插件还没有 network:any 联网权限，生图不可用：到「设置 → 插件 → 外部插件」给内容工作台单独授权。文字功能照常可用。" };
  return { ok: true, reason: "" };
}

// ---------------------------------------------------------------------------
// 模块页
// ---------------------------------------------------------------------------

function ModulePage() {
  const [view, setView] = useState("ideas");
  const [ideas, setIdeas] = useState([]);
  const [platforms, setPlatforms] = useState(DEFAULT_PLATFORMS);
  const [hint, setHint] = useState("正在加载…");
  const [open, setOpen] = useState("");
  const [status, setStatus] = useState(null);

  async function load() {
    try {
      const data = await api("/ideas");
      setIdeas(Array.isArray(data.ideas) ? data.ideas : []);
      if (Array.isArray(data.platforms)) setPlatforms(data.platforms);
      setHint("");
    } catch (err) { setHint("读取失败：" + errText(err)); }
  }
  function loadStatus() { api("/status").then(setStatus).catch(function () {}); }
  useEffect(function () { load(); loadStatus(); }, []);

  if (open) {
    return h(Studio, { id: open, platforms: platforms, status: status, onBack: function () { setOpen(""); load(); }, onStatus: loadStatus });
  }
  const narrow = isNarrow();
  const seg = h(Seg, { label: "视图", value: view, onChange: setView, options: VIEWS });
  const produced = ideas.filter(function (i) { return i.prod_status === "done"; }).length;
  const published = ideas.filter(function (i) { return i.status === "published"; }).length;
  const common = { ideas: ideas, platforms: platforms, reload: load, onOpen: setOpen };
  return h("div", { className: "md-page" },
    h(TitleBar, { title: PLUGIN_NAME, desc: ideas.length + " 条灵感 · " + produced + " 篇已成稿 · " + published + " 篇已发布", tabs: narrow ? null : seg }),
    narrow ? seg : null,
    hint ? h("p", { className: "md-hint" }, hint) : null,
    view === "ideas" ? h(IdeasView, common)
      : view === "content" ? h(ContentView, common)
      : view === "schedule" ? h(ScheduleView, common)
      : view === "insights" ? h(InsightsView, common)
      : h(StyleView, null));
}

// ---------------------------------------------------------------------------
// 灵感墙
// ---------------------------------------------------------------------------

function IdeaCard(props) {
  const it = props.idea;
  return h("button", { type: "button", className: "md-card md-idea", onClick: function () { props.onOpen(it.id); } },
    it.cover_image ? h("img", { className: "md-idea-img", src: imgURL(it.cover_image), alt: "" }) : null,
    h("div", { className: "md-idea-top" }, h(StatusPill, { status: it.status }),
      it.prod_status === "done" ? h("span", { className: "md-tag md-tag-ok" }, "已成稿") : it.prod_status === "running" ? h("span", { className: "md-tag" }, "成稿中") : null,
      it.platform ? h("span", { className: "md-tag" }, it.platform) : null),
    h("strong", { className: "md-idea-title" }, displayTitle(it)),
    it.body ? h("p", { className: "md-idea-body" }, it.body) : null,
    h("div", { className: "md-idea-meta" },
      (it.tags || []).slice(0, 4).map(function (t) { return h("span", { key: t, className: "md-hash" }, "#" + t); }),
      it.scheduled_at ? h("span", { className: "md-hint" }, fmtWhen(it.scheduled_at)) : null));
}

function IdeasView(props) {
  const [text, setText] = useState("");
  const [body, setBody] = useState("");
  const [more, setMore] = useState(false);
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");

  async function add(e) {
    if (e) e.preventDefault();
    if (!text.trim() && !body.trim()) return;
    setBusy(true);
    try {
      const tags = (text.match(/#([^\s#]+)/g) || []).map(function (t) { return t.slice(1); });
      const title = text.replace(/#[^\s#]+/g, "").trim();
      const url = (body.match(/https?:\/\/\S+/) || [""])[0];
      await send("/ideas", "POST", { title: title, body: body.trim(), tags: tags, url: url });
      setText(""); setBody(""); setMore(false); setMsg("记下了。点开卡片可以一键成稿。");
      props.reload();
    } catch (err) { setMsg("没记上：" + errText(err)); } finally { setBusy(false); }
  }
  const kw = q.trim().toLowerCase();
  const list = props.ideas.filter(function (it) {
    if (!kw) return true;
    return [it.title, it.prod_title, it.body, (it.tags || []).join(" "), it.platform].join(" ").toLowerCase().indexOf(kw) >= 0;
  });
  return h("div", { className: "md-stack" },
    h("form", { className: "md-capture", onSubmit: add },
      h("input", { className: "md-capture-input", value: text, disabled: busy, placeholder: "一句话记下灵感，回车保存（#标签 会自动识别）",
        onChange: function (e) { setText(e.target.value); } }),
      more ? h("textarea", { className: "md-textarea", rows: 3, value: body, disabled: busy, placeholder: "补充：素材、链接、想到的画面…",
        onChange: function (e) { setBody(e.target.value); } }) : null,
      h("div", { className: "md-bar" },
        h("button", { className: "md-btn md-primary", type: "submit", disabled: busy || (!text.trim() && !body.trim()) }, busy ? "保存中…" : "记下"),
        h("button", { className: "md-btn", type: "button", onClick: function () { setMore(!more); } }, more ? "收起补充" : "加补充"),
        h("input", { className: "md-search", value: q, placeholder: "搜索灵感", onChange: function (e) { setQ(e.target.value); } }))),
    h(Note, { text: msg }),
    list.length
      ? h("div", { className: "md-grid" }, list.map(function (it) { return h(IdeaCard, { key: it.id, idea: it, onOpen: props.onOpen }); }))
      : h(Empty, { title: props.ideas.length ? "没有匹配的灵感" : "还没有灵感", text: props.ideas.length ? "换个关键词试试。" : "在上面写一句话，回车就记下了。点开任何一条，一键生成口播脚本、标题和六格分镜。" }));
}

// ---------------------------------------------------------------------------
// 内容管理：看板 / 表格
// ---------------------------------------------------------------------------

function ContentView(props) {
  const [mode, setMode] = useState("board");
  const [q, setQ] = useState("");
  const [msg, setMsg] = useState("");
  async function setStatus(id, status) {
    try { await send("/ideas/" + id, "PUT", { status: status }); props.reload(); } catch (err) { setMsg("改状态失败：" + errText(err)); }
  }
  const kw = q.trim().toLowerCase();
  const list = props.ideas.filter(function (it) {
    return !kw || [displayTitle(it), (it.tags || []).join(" "), it.platform].join(" ").toLowerCase().indexOf(kw) >= 0;
  });
  return h("div", { className: "md-stack" },
    h("div", { className: "md-bar" },
      h(Seg, { label: "显示方式", value: mode, onChange: setMode, options: [["board", "看板"], ["table", "表格"]] }),
      h("input", { className: "md-search", value: q, placeholder: "搜索标题、标签、平台", onChange: function (e) { setQ(e.target.value); } })),
    h(Note, { text: msg, kind: "bad" }),
    !list.length ? h(Empty, { title: props.ideas.length ? "没有匹配的内容" : "还没有内容", text: props.ideas.length ? "换个关键词试试。" : "到灵感页记下第一条。" })
      : mode === "board"
      ? h("div", { className: "md-board" }, STATUS.map(function (col) {
          const rows = list.filter(function (it) { return it.status === col[0]; });
          return h("section", { key: col[0], className: "md-col" },
            h("h4", null, col[1], h("span", { className: "md-hint" }, " " + rows.length)),
            rows.map(function (it) {
              return h("div", { key: it.id, className: "md-card md-mini", role: "button", tabIndex: 0,
                onClick: function () { props.onOpen(it.id); }, onKeyDown: function (e) { if (e.key === "Enter") props.onOpen(it.id); } },
                h("strong", null, displayTitle(it)),
                h("div", { className: "md-idea-meta" },
                  it.platform ? h("span", { className: "md-tag" }, it.platform) : null,
                  it.scheduled_at ? h("span", { className: "md-hint" }, fmtWhen(it.scheduled_at)) : null,
                  it.prod_status === "done" ? h("span", { className: "md-tag md-tag-ok" }, "已成稿") : null));
            }));
        }))
      : h("div", { className: "md-table-wrap" }, h("table", { className: "md-table" },
          h("thead", null, h("tr", null, ["内容", "状态", "平台", "排期", "数据", "记录于"].map(function (c) { return h("th", { key: c }, c); }))),
          h("tbody", null, list.map(function (it) {
            return h("tr", { key: it.id },
              h("td", null, h("button", { type: "button", className: "md-link", onClick: function () { props.onOpen(it.id); } }, displayTitle(it))),
              h("td", null, h("select", { className: "md-select", value: it.status, "aria-label": "修改状态", onChange: function (e) { setStatus(it.id, e.target.value); } },
                STATUS.map(function (s) { return h("option", { key: s[0], value: s[0] }, s[1]); }))),
              h("td", null, it.platform || "-"),
              h("td", null, it.scheduled_at ? fmtWhen(it.scheduled_at) : "-"),
              h("td", null, it.metric_count ? fmtNum(it.views) + " 播放 / " + fmtNum(it.engagement) + " 互动" : "-"),
              h("td", null, String(it.created_at || "").slice(0, 10)));
          })))));
}

// ---------------------------------------------------------------------------
// 排期：月历 + 待排期列表，拖到某一天就排好
// ---------------------------------------------------------------------------

function monthCells(year, month) {
  const first = new Date(year, month, 1);
  const offset = (first.getDay() + 6) % 7;
  const start = new Date(year, month, 1 - offset);
  const cells = [];
  for (let i = 0; i < 42; i++) cells.push(new Date(start.getFullYear(), start.getMonth(), start.getDate() + i));
  // 6 行里最后一行整行都不在本月时去掉
  if (cells[35].getMonth() !== month) cells.length = 35;
  if (cells[28].getMonth() !== month) cells.length = 28;
  return cells;
}

function ScheduleView(props) {
  const now = new Date();
  const [ym, setYm] = useState([now.getFullYear(), now.getMonth()]);
  const [msg, setMsg] = useState("");
  const [over, setOver] = useState("");
  const [pick, setPick] = useState("");
  const today = ymd(now);
  const cells = monthCells(ym[0], ym[1]);
  const byDay = {};
  props.ideas.forEach(function (it) {
    if (!it.scheduled_at) return;
    const d = it.scheduled_at.slice(0, 10);
    (byDay[d] = byDay[d] || []).push(it);
  });
  Object.keys(byDay).forEach(function (k) { byDay[k].sort(function (a, b) { return a.scheduled_at < b.scheduled_at ? -1 : 1; }); });
  const ideaDays = {};
  props.ideas.forEach(function (it) { const d = String(it.created_at || "").slice(0, 10); ideaDays[d] = (ideaDays[d] || 0) + 1; });
  const pending = props.ideas.filter(function (it) { return !it.scheduled_at && it.status !== "published" && (it.prod_status === "done" || it.status === "draft" || it.status === "ready"); });
  const loose = props.ideas.filter(function (it) { return !it.scheduled_at && it.status !== "published" && pending.indexOf(it) < 0; }).slice(0, 8);
  const upcoming = props.ideas.filter(function (it) { return it.scheduled_at && it.scheduled_at >= today && it.status !== "published"; })
    .sort(function (a, b) { return a.scheduled_at < b.scheduled_at ? -1 : 1; });
  const next = upcoming[0];
  // 连续记录：从今天往前，每天都有新灵感
  let streak = 0;
  for (let i = 0; i < 365; i++) {
    const d = ymd(new Date(now.getFullYear(), now.getMonth(), now.getDate() - i));
    if (ideaDays[d]) streak++; else if (i > 0) break;
  }
  const monthKey = ym[0] + "-" + pad2(ym[1] + 1);
  const monthIdeas = props.ideas.filter(function (it) { return String(it.created_at || "").slice(0, 7) === monthKey; }).length;
  const monthPub = props.ideas.filter(function (it) { return it.status === "published" && String(it.scheduled_at || it.published_at || "").slice(0, 7) === monthKey; }).length;

  async function schedule(id, day) {
    const it = props.ideas.find(function (x) { return x.id === id; });
    if (!it) return;
    const time = it.scheduled_at ? it.scheduled_at.slice(11, 16) : "20:00";
    try {
      await send("/ideas/" + id, "PUT", { scheduled_at: day ? day + "T" + time : "" });
      setMsg(day ? "《" + displayTitle(it) + "》排到 " + fmtWhen(day + "T" + time) : "已取消《" + displayTitle(it) + "》的排期");
      setPick("");
      props.reload();
    } catch (err) { setMsg("排期失败：" + errText(err)); }
  }
  function dragProps(it) {
    return { draggable: true, onDragStart: function (e) { e.dataTransfer.setData("text/plain", it.id); e.dataTransfer.effectAllowed = "move"; } };
  }
  function shift(n) { const d = new Date(ym[0], ym[1] + n, 1); setYm([d.getFullYear(), d.getMonth()]); }

  return h("div", { className: "md-schedule" },
    h("div", { className: "md-stack" },
      h("div", { className: "md-bar md-cal-head" },
        h("button", { className: "md-btn", type: "button", onClick: function () { shift(-1); } }, "上个月"),
        h("strong", null, ym[0] + " 年 " + (ym[1] + 1) + " 月"),
        h("button", { className: "md-btn", type: "button", onClick: function () { setYm([now.getFullYear(), now.getMonth()]); } }, "今天"),
        h("button", { className: "md-btn", type: "button", onClick: function () { shift(1); } }, "下个月"),
        h("span", { className: "md-hint" }, "连续记录 " + streak + " 天 · 本月灵感 " + monthIdeas + " 条 · 本月发布 " + monthPub + " 条")),
      pick ? h(Note, { text: "已选中一条内容，点日历上的某一天把它排过去；再点一次列表里的它取消选择。" }) : null,
      h("div", { className: "md-cal" },
        WEEK.map(function (w) { return h("div", { key: w, className: "md-cal-week" }, "周" + w); }),
        cells.map(function (d) {
          const key = ymd(d);
          const items = byDay[key] || [];
          const cls = "md-cal-cell" + (d.getMonth() !== ym[1] ? " md-out" : "") + (key === today ? " md-today" : "") + (over === key ? " md-over" : "") + (pick ? " md-pickable" : "");
          return h("div", { key: key, className: cls,
            onDragOver: function (e) { e.preventDefault(); setOver(key); },
            onDragLeave: function () { setOver(""); },
            onDrop: function (e) { e.preventDefault(); setOver(""); const id = e.dataTransfer.getData("text/plain"); if (id) schedule(id, key); },
            onClick: function () { if (pick) schedule(pick, key); } },
            h("div", { className: "md-cal-day" }, h("span", null, d.getDate()), ideaDays[key] ? h("span", { className: "md-stamp", title: "这天记了 " + ideaDays[key] + " 条灵感" }, ideaDays[key]) : null),
            items.map(function (it) {
              return h("div", Object.assign({ key: it.id, className: "md-chip md-s-" + it.status, title: displayTitle(it) + "（" + fmtWhen(it.scheduled_at) + "）",
                onClick: function (e) { e.stopPropagation(); props.onOpen(it.id); } }, dragProps(it)),
                h("span", { className: "md-chip-time" }, it.scheduled_at.slice(11, 16)), displayTitle(it));
            }));
        })),
      h(Note, { text: msg })),
    h("aside", { className: "md-side" },
      h("section", { className: "md-card md-panel" }, h("h4", null, "下一条"),
        next ? h("div", null, h("button", { type: "button", className: "md-link", onClick: function () { props.onOpen(next.id); } }, displayTitle(next)),
          h("p", { className: "md-hint" }, fmtWhen(next.scheduled_at) + (next.platform ? " · " + next.platform : "") + " · " + (STATUS_LABEL[next.status] || ""))) : h("p", { className: "md-hint" }, "还没有排期。")),
      h("section", { className: "md-card md-panel" }, h("h4", null, "待排期", h("span", { className: "md-hint" }, " " + pending.length)),
        pending.length ? null : h("p", { className: "md-hint" }, "做好稿子后，从这里拖到日历上。"),
        pending.concat(loose).map(function (it, i) {
          return h("div", Object.assign({ key: it.id, className: "md-pending" + (pick === it.id ? " md-picked" : "") + (i >= pending.length ? " md-loose" : ""), role: "button", tabIndex: 0,
            onClick: function () { setPick(pick === it.id ? "" : it.id); }, onKeyDown: function (e) { if (e.key === "Enter") setPick(pick === it.id ? "" : it.id); } }, dragProps(it)),
            h("strong", null, displayTitle(it)),
            h("span", { className: "md-hint" }, i >= pending.length ? "灵感" : (it.prod_status === "done" ? "已成稿" : STATUS_LABEL[it.status])));
        }),
        h("p", { className: "md-hint" }, "拖到日历上排期；手机上先点一条，再点日期。")),
      upcoming.length > 1 ? h("section", { className: "md-card md-panel" }, h("h4", null, "接下来"),
        upcoming.slice(1, 6).map(function (it) {
          return h("div", { key: it.id, className: "md-upnext" },
            h("button", { type: "button", className: "md-link", onClick: function () { props.onOpen(it.id); } }, displayTitle(it)),
            h("span", { className: "md-hint" }, fmtWhen(it.scheduled_at)),
            h("button", { type: "button", className: "md-btn md-xs", onClick: function () { schedule(it.id, ""); } }, "取消排期"));
        })) : null));
}

// ---------------------------------------------------------------------------
// 复盘
// ---------------------------------------------------------------------------

function parseCsv(text, platforms) {
  const out = [];
  const errors = [];
  String(text).split(/\r?\n/).forEach(function (line, i) {
    const t = line.trim();
    if (!t) return;
    const c = t.split(/[,，\t]/).map(function (x) { return x.trim(); });
    if (c.length < 3 || !/^\d{4}-\d{2}-\d{2}$/.test(c[0]) || platforms.indexOf(c[1]) < 0) { errors.push(i + 1); return; }
    const n = function (k) { return Math.max(0, parseInt(c[k] || "0", 10) || 0); };
    out.push({ date: c[0], platform: c[1], views: n(2), likes: n(3), comments: n(4), shares: n(5), saves: n(6) });
  });
  return { items: out, errors: errors };
}

function Bars(props) {
  const max = Math.max.apply(null, props.rows.map(function (r) { return r.value; }).concat([1]));
  return h("div", { className: "md-bars" }, props.rows.map(function (r) {
    return h("div", { key: r.label, className: "md-bar-row" },
      h("span", { className: "md-bar-label" }, r.label),
      h("span", { className: "md-bar-track" }, h("span", { className: "md-bar-fill", style: { width: Math.max(2, r.value / max * 100) + "%" } })),
      h("span", { className: "md-bar-val" }, r.text));
  }));
}

function Trend(props) {
  const rows = props.rows || [];
  const max = Math.max.apply(null, rows.map(function (r) { return r[props.metric]; }).concat([1]));
  const w = 600, ht = 120, bw = w / Math.max(rows.length, 1);
  return h("svg", { className: "md-trend", viewBox: "0 0 " + w + " " + (ht + 18), role: "img", "aria-label": props.label, preserveAspectRatio: "none" },
    rows.map(function (r, i) {
      const v = r[props.metric];
      const bh = v ? Math.max(2, v / max * ht) : 0;
      return h("g", { key: r.date },
        h("rect", { x: i * bw + 1, y: ht - bh, width: Math.max(1, bw - 2), height: bh, rx: 2, className: "md-trend-bar" }, h("title", null, r.date + "：" + v)),
        i % 5 === 0 ? h("text", { x: i * bw + 1, y: ht + 14, className: "md-trend-text" }, r.date.slice(5)) : null);
    }));
}

function InsightsView(props) {
  const [data, setData] = useState(null);
  const [metrics, setMetrics] = useState([]);
  const [msg, setMsg] = useState("");
  const [form, setForm] = useState({ idea_id: "", platform: props.platforms[0] || "小红书", date: ymd(new Date()), views: "", likes: "", comments: "", shares: "", saves: "" });
  const [csv, setCsv] = useState("");
  const [showCsv, setShowCsv] = useState(false);
  const [metric, setMetric] = useState("views");

  async function load() {
    try {
      const [ins, ms] = await Promise.all([api("/insights"), api("/metrics")]);
      setData(ins); setMetrics(ms.metrics || []);
    } catch (err) { setMsg("读取失败：" + errText(err)); }
  }
  useEffect(function () { load(); }, []);
  function upd(k, v) { setForm(Object.assign({}, form, (function () { const o = {}; o[k] = v; return o; })())); }
  function onPickIdea(id) {
    const it = props.ideas.find(function (x) { return x.id === id; });
    setForm(Object.assign({}, form, { idea_id: id, platform: (it && it.platform) || form.platform }));
  }
  async function add(e) {
    e.preventDefault();
    const n = function (k) { return Math.max(0, parseInt(form[k] || "0", 10) || 0); };
    try {
      await send("/metrics", "POST", { idea_id: form.idea_id, platform: form.platform, date: form.date, views: n("views"), likes: n("likes"), comments: n("comments"), shares: n("shares"), saves: n("saves") });
      setMsg("记下了一条数据。");
      setForm(Object.assign({}, form, { views: "", likes: "", comments: "", shares: "", saves: "" }));
      load(); props.reload();
    } catch (err) { setMsg("录入失败：" + errText(err)); }
  }
  async function importCsv() {
    const r = parseCsv(csv, props.platforms);
    if (!r.items.length) { setMsg("没有可导入的行。每行：日期,平台,播放,点赞,评论,转发,收藏"); return; }
    try {
      await send("/metrics", "POST", { items: r.items });
      setMsg("导入 " + r.items.length + " 条数据" + (r.errors.length ? "，第 " + r.errors.join("、") + " 行格式不对已跳过" : "") + "。");
      setCsv(""); setShowCsv(false); load();
    } catch (err) { setMsg("导入失败：" + errText(err)); }
  }
  async function del(id) {
    try { await api("/metrics/" + id, { method: "DELETE" }); load(); props.reload(); } catch (err) { setMsg("删除失败：" + errText(err)); }
  }
  async function adopt(p) {
    try { await send("/rules", "POST", { name: p.name, rule: p.rule, source: "数据复盘" }); setMsg("已写进写作规则「" + p.name + "」，之后成稿都会遵守。"); load(); }
    catch (err) { setMsg("写入失败：" + errText(err)); }
  }
  const published = props.ideas.filter(function (it) { return it.status === "published" || it.status === "ready"; });
  const choices = published.length ? published : props.ideas;
  const hasData = data && data.posts > 0;
  const hasViews = data && data.views > 0;
  return h("div", { className: "md-stack" },
    h("form", { className: "md-card md-panel md-form", onSubmit: add },
      h("h4", null, "录入一条数据"),
      h("div", { className: "md-form-grid" },
        h(Field, { label: "对应内容", wide: true }, h("select", { className: "md-select", value: form.idea_id, onChange: function (e) { onPickIdea(e.target.value); } },
          h("option", { value: "" }, "不关联内容"),
          choices.map(function (it) { return h("option", { key: it.id, value: it.id }, displayTitle(it)); }))),
        h(Field, { label: "平台" }, h("select", { className: "md-select", value: form.platform, onChange: function (e) { upd("platform", e.target.value); } },
          props.platforms.map(function (p) { return h("option", { key: p, value: p }, p); }))),
        h(Field, { label: "日期" }, h("input", { type: "date", value: form.date, onChange: function (e) { upd("date", e.target.value); } })),
        [["views", "播放 / 阅读"], ["likes", "点赞"], ["comments", "评论"], ["shares", "转发"], ["saves", "收藏"]].map(function (f) {
          return h(Field, { key: f[0], label: f[1] }, h("input", { type: "number", min: 0, inputMode: "numeric", value: form[f[0]], onChange: function (e) { upd(f[0], e.target.value); } }));
        })),
      h("div", { className: "md-bar" },
        h("button", { className: "md-btn md-primary", type: "submit" }, "录入数据"),
        h("button", { className: "md-btn", type: "button", onClick: function () { setShowCsv(!showCsv); } }, showCsv ? "收起" : "粘贴 CSV")),
      showCsv ? h("div", { className: "md-stack" },
        h("textarea", { className: "md-textarea", rows: 4, value: csv, placeholder: "每行一条：日期,平台,播放,点赞,评论,转发,收藏\n2026-11-20,小红书,3200,120,18,40,66", onChange: function (e) { setCsv(e.target.value); } }),
        h("button", { className: "md-btn", type: "button", onClick: importCsv }, "导入")) : null,
      h("p", { className: "md-hint" }, "数据需要手动回填：工作台不抓取各平台的后台数据。")),
    h(Note, { text: msg }),
    !hasData ? h(Empty, { title: "还没有数据", text: "发布之后，把数据录进来，这里就开始替你复盘。" }) : h("div", { className: "md-stack" },
      h("div", { className: "md-kpis" },
        [["内容", data.posts + " 条"], [hasViews ? "播放" : "互动", fmtNum(hasViews ? data.views : data.engagement)], ["总互动", fmtNum(data.engagement)], ["互动率", hasViews ? pct(data.rate) : "-"]].map(function (k) {
          return h("div", { key: k[0], className: "md-card md-kpi" }, h("span", { className: "md-hint" }, k[0]), h("strong", null, k[1]));
        })),
      data.top && data.top[0] ? h(Note, { text: "表现最好的是《" + data.top[0].title + "》：" + fmtNum(data.top[0].views) + " 播放，" + fmtNum(data.top[0].engagement) + " 互动。" }) : null,
      h("div", { className: "md-two" },
        h("section", { className: "md-card md-panel" },
          h("div", { className: "md-bar" }, h("h4", null, "最近 30 天"),
            h(Seg, { label: "指标", value: metric, onChange: setMetric, options: [["views", "播放"], ["engagement", "互动"]] })),
          h(Trend, { rows: data.daily, metric: metric, label: "每日" + (metric === "views" ? "播放" : "互动") + "趋势" }),
          h("p", { className: "md-hint" }, "按录入时填的日期统计。")),
        h("section", { className: "md-card md-panel" }, h("h4", null, "平台分布"),
          h(Bars, { rows: data.platforms.map(function (p) {
            const v = hasViews ? p.views : p.engagement;
            return { label: p.platform, value: v, text: fmtNum(v) + (hasViews ? " 播放" : " 互动") + " · " + p.n + " 条" };
          }) }))),
      h("div", { className: "md-two" },
        h("section", { className: "md-card md-panel" }, h("h4", null, "表现最好的内容"),
          data.top.length ? data.top.map(function (t, i) {
            return h("div", { key: t.idea_id, className: "md-upnext" },
              h("span", { className: "md-rank" }, i + 1),
              h("button", { type: "button", className: "md-link", onClick: function () { props.onOpen(t.idea_id); } }, t.title || "未命名"),
              h("span", { className: "md-hint" }, fmtNum(t.views) + " 播放 · " + fmtNum(t.engagement) + " 互动" + (t.views ? " · " + pct(t.rate) : "")));
          }) : h("p", { className: "md-hint" }, "数据关联到内容后，这里会排出名次。")),
        h("section", { className: "md-card md-panel" }, h("h4", null, "给你的建议"),
          data.proposals.length ? data.proposals.map(function (p) {
            return h("div", { key: p.name, className: "md-proposal" },
              h("strong", null, p.name), h("p", null, p.rule), h("p", { className: "md-hint" }, p.evidence),
              h("button", { className: "md-btn md-xs", type: "button", onClick: function () { adopt(p); } }, "写进规则"));
          }) : h("p", { className: "md-hint" }, "数据还不够：多几篇内容、多两个平台或时段，这里会给出值得坚持的做法。")))),
    metrics.length ? h("details", { className: "md-card md-panel" }, h("summary", null, "全部数据（" + metrics.length + " 条）"),
      h("div", { className: "md-table-wrap" }, h("table", { className: "md-table" },
        h("thead", null, h("tr", null, ["日期", "平台", "内容", "播放", "点赞", "评论", "转发", "收藏", ""].map(function (c, i) { return h("th", { key: i }, c); }))),
        h("tbody", null, metrics.map(function (m) {
          return h("tr", { key: m.id }, h("td", null, m.date), h("td", null, m.platform), h("td", null, m.title || "-"),
            h("td", null, m.views), h("td", null, m.likes), h("td", null, m.comments), h("td", null, m.shares), h("td", null, m.saves),
            h("td", null, h("button", { className: "md-btn md-xs", type: "button", onClick: function () { del(m.id); } }, "删除")));
        }))))) : null);
}

// ---------------------------------------------------------------------------
// 风格
// ---------------------------------------------------------------------------

const STYLE_FIELDS = [["summary", "一句话概括"], ["tone", "语气"], ["sentences", "句式"], ["opening", "开头"], ["structure", "结构"], ["logic", "逻辑"], ["story", "讲述"], ["flow", "衔接"]];

function StyleEditor(props) {
  const st = props.style || {};
  const [v, setV] = useState({
    name: st.name || "", summary: st.summary || "", tone: st.tone || "", sentences: st.sentences || "", opening: st.opening || "",
    structure: st.structure || "", logic: st.logic || "", story: st.story || "", flow: st.flow || "",
    words: (st.words || []).join("、"), avoid: (st.avoid || []).join("、"), samples: (st.samples || []).join("\n"),
  });
  const [msg, setMsg] = useState("");
  function upd(k, val) { const o = Object.assign({}, v); o[k] = val; setV(o); }
  async function save(e) {
    e.preventDefault();
    const split = function (s, re) { return s.split(re).map(function (x) { return x.trim(); }).filter(Boolean); };
    const body = Object.assign({}, v, { words: split(v.words, /[、,，]/), avoid: split(v.avoid, /[、,，]/), samples: split(v.samples, /\n/) });
    try { await send(st.id ? "/styles/" + st.id : "/styles", st.id ? "PUT" : "POST", body); props.onDone(); }
    catch (err) { setMsg("保存失败：" + errText(err)); }
  }
  return h("form", { className: "md-card md-panel md-form", onSubmit: save },
    h("h4", null, st.id ? "编辑「" + st.name + "」" : "手动添加风格"),
    h("div", { className: "md-form-grid" },
      h(Field, { label: "名字" }, h("input", { value: v.name, maxLength: 20, onChange: function (e) { upd("name", e.target.value); } })),
      STYLE_FIELDS.map(function (f) { return h(Field, { key: f[0], label: f[1], wide: true }, h("input", { value: v[f[0]], onChange: function (e) { upd(f[0], e.target.value); } })); }),
      h(Field, { label: "常用词（顿号分隔）", wide: true }, h("input", { value: v.words, onChange: function (e) { upd("words", e.target.value); } })),
      h(Field, { label: "绝不要（顿号分隔）", wide: true }, h("input", { value: v.avoid, onChange: function (e) { upd("avoid", e.target.value); } })),
      h(Field, { label: "样句（一行一句）", wide: true }, h("textarea", { className: "md-textarea", rows: 3, value: v.samples, onChange: function (e) { upd("samples", e.target.value); } }))),
    h(Note, { text: msg, kind: "bad" }),
    h("div", { className: "md-bar" }, h("button", { className: "md-btn md-primary", type: "submit" }, "保存"),
      h("button", { className: "md-btn", type: "button", onClick: props.onDone }, "取消")));
}

function StyleView() {
  const [data, setData] = useState(null);
  const [cat, setCat] = useState("all");
  const [msg, setMsg] = useState("");
  const [mode, setMode] = useState("");
  const [editing, setEditing] = useState(null);
  const [learnText, setLearnText] = useState("");
  const [learnName, setLearnName] = useState("");
  const [learning, setLearning] = useState(false);
  const [rule, setRule] = useState({ name: "", rule: "" });

  async function load() { try { setData(await api("/styles")); } catch (err) { setMsg("读取失败：" + errText(err)); } }
  useEffect(function () { load(); api("/styles/learn").then(function (j) { if (j.running) setLearning(true); }).catch(function () {}); }, []);
  useEffect(function () {
    if (!learning) return undefined;
    const t = setInterval(function () {
      api("/styles/learn").then(function (j) {
        if (j.running) return;
        setLearning(false);
        if (j.error) setMsg("提炼失败：" + j.error);
        else { setMsg("提炼好了「" + j.name + "」，点「用这个写」就按它写。"); setMode(""); setLearnText(""); setLearnName(""); }
        load();
      }).catch(function () {});
    }, 2000);
    return function () { clearInterval(t); };
  }, [learning]);

  async function use(id) {
    try { await send("/styles/active", "POST", { id: id }); load(); setMsg(id ? "之后的成稿按这个风格写。" : "不再使用特定风格。"); } catch (err) { setMsg(errText(err)); }
  }
  async function del(st) {
    if (!window.confirm("删除风格「" + st.name + "」？")) return;
    try { await api("/styles/" + st.id, { method: "DELETE" }); load(); } catch (err) { setMsg("删除失败：" + errText(err)); }
  }
  async function learn() {
    try { await send("/styles/learn", "POST", { text: learnText, name: learnName }); setLearning(true); setMsg("正在提炼风格，大约需要几十秒…"); }
    catch (err) { setMsg("提炼失败：" + errText(err)); }
  }
  async function addRule(e) {
    e.preventDefault();
    try { await send("/rules", "POST", { name: rule.name, rule: rule.rule, source: "手动" }); setRule({ name: "", rule: "" }); load(); }
    catch (err) { setMsg("添加规则失败：" + errText(err)); }
  }
  async function toggleRule(r) { try { await send("/rules/" + r.id, "PUT", { status: r.status === "active" ? "paused" : "active" }); load(); } catch (err) { setMsg(errText(err)); } }
  async function delRule(r) {
    if (!window.confirm("删除规则「" + r.name + "」？")) return;
    try { await api("/rules/" + r.id, { method: "DELETE" }); load(); } catch (err) { setMsg(errText(err)); }
  }
  if (!data) return h("p", { className: "md-hint" }, msg || "正在加载…");
  const styles = data.styles || [];
  const active = styles.find(function (s) { return s.id === data.active_style_id; });
  const cats = [];
  styles.forEach(function (s) { if (s.category && cats.indexOf(s.category) < 0) cats.push(s.category); });
  const shown = styles.filter(function (s) { return cat === "all" || s.category === cat; });
  return h("div", { className: "md-stack" },
    h("div", { className: "md-card md-panel md-active-style" },
      h("div", null, h("span", { className: "md-hint" }, "现在按"), h("strong", null, active ? " " + active.name + " " : " 默认写法 "), h("span", { className: "md-hint" }, "的风格写。"),
        active && active.summary ? h("p", { className: "md-hint" }, active.summary) : null),
      h("div", { className: "md-bar" },
        active ? h("button", { className: "md-btn", type: "button", onClick: function () { use(""); } }, "不用了") : null,
        h("button", { className: "md-btn md-primary", type: "button", onClick: function () { setMode(mode === "learn" ? "" : "learn"); } }, "添加我的风格"),
        h("button", { className: "md-btn", type: "button", onClick: function () { setEditing({}); setMode("edit"); } }, "手动添加"))),
    h(Note, { text: msg }),
    mode === "learn" ? h("div", { className: "md-card md-panel md-form" },
      h("h4", null, "从自己的稿子提炼风格"),
      h("textarea", { className: "md-textarea", rows: 8, value: learnText, disabled: learning, placeholder: "贴几篇你写过的稿子或口播文字，越多越准（至少 80 字）。", onChange: function (e) { setLearnText(e.target.value); } }),
      h(Field, { label: "名字（可选）" }, h("input", { value: learnName, maxLength: 20, disabled: learning, placeholder: "比如：我的口播风格", onChange: function (e) { setLearnName(e.target.value); } })),
      h("div", { className: "md-bar" },
        h("button", { className: "md-btn md-primary", type: "button", disabled: learning || learnText.trim().length < 80, onClick: learn }, learning ? "正在提炼…" : "提炼风格"),
        h("button", { className: "md-btn", type: "button", onClick: function () { setMode(""); } }, "取消")),
      h("p", { className: "md-hint" }, "提炼用的是工作台配置的模型（会把这段文字发给模型服务商）；原文会留一份，写正文时用来贴近你的断句和口癖。")) : null,
    mode === "edit" && editing ? h(StyleEditor, { style: editing, onDone: function () { setMode(""); setEditing(null); load(); } }) : null,
    h(Seg, { label: "博主类型", value: cat, onChange: setCat, options: [["all", "全部"]].concat(cats.map(function (c) { return [c, c]; })) }),
    h("div", { className: "md-grid" }, shown.map(function (st) {
      const on = st.id === data.active_style_id;
      return h("div", { key: st.id, className: "md-card md-style" + (on ? " md-on" : "") },
        h("div", { className: "md-idea-top" }, h("strong", null, st.name), h("span", { className: "md-tag" }, st.category || "我的风格"), on ? h("span", { className: "md-tag md-tag-ok" }, "正在使用") : null),
        st.summary ? h("p", null, st.summary) : null,
        st.tone ? h("p", { className: "md-hint" }, "语气：" + st.tone) : null,
        st.samples && st.samples[0] ? h("blockquote", { className: "md-quote" }, st.samples[0]) : null,
        h("div", { className: "md-bar" },
          on ? null : h("button", { className: "md-btn md-primary md-xs", type: "button", onClick: function () { use(st.id); } }, "用这个写"),
          h("button", { className: "md-btn md-xs", type: "button", onClick: function () { setEditing(st); setMode("edit"); } }, "编辑"),
          h("button", { className: "md-btn md-xs", type: "button", onClick: function () { del(st); } }, "删除")));
    })),
    h("section", { className: "md-card md-panel" },
      h("h4", null, "写作规则", h("span", { className: "md-hint" }, " 每种风格都会遵守")),
      (data.rules || []).length ? data.rules.map(function (r) {
        return h("div", { key: r.id, className: "md-rule" + (r.status === "active" ? "" : " md-paused") },
          h("div", null, h("strong", null, r.name), h("p", { className: "md-hint" }, r.rule + (r.source ? " · 来自" + r.source : ""))),
          h("div", { className: "md-bar" },
            h("button", { className: "md-btn md-xs", type: "button", onClick: function () { toggleRule(r); } }, r.status === "active" ? "停用" : "启用"),
            h("button", { className: "md-btn md-xs", type: "button", onClick: function () { delRule(r); } }, "删除")));
      }) : h("p", { className: "md-hint" }, "比如「标题不超过 20 个字」「开头 50 字内给一个具体数字」。复盘页的建议也可以一键写进来。"),
      h("form", { className: "md-rule-form", onSubmit: addRule },
        h("input", { value: rule.name, maxLength: 40, placeholder: "规则名，如：开头先给数字", onChange: function (e) { setRule({ name: e.target.value, rule: rule.rule }); } }),
        h("input", { value: rule.rule, maxLength: 400, placeholder: "具体要求", onChange: function (e) { setRule({ name: rule.name, rule: e.target.value }); } }),
        h("button", { className: "md-btn", type: "submit", disabled: !rule.name.trim() || !rule.rule.trim() }, "添加规则"))));
}

// ---------------------------------------------------------------------------
// 成稿工作区：左聊右改
// ---------------------------------------------------------------------------

const STAGE_TEXT = { plan: "正在写策划案：标题、要点、封面和分镜…", writing: "策划案定了，正在写正文，画面同时开工…" };
const QUICK = ["标题再短一点", "语气轻松一点", "开场再抓人一点", "封面大字更有冲击力"];

function Plate(props) {
  const a = props.asset;
  if (!a) return null;
  const busy = a.status === "running" || a.status === "pending";
  return h("div", { className: "md-plate md-plate-" + a.kind + (busy ? " md-busy" : "") },
    a.image_id ? h("img", { src: imgURL(a.image_id), alt: a.caption || a.visual || "画面", loading: "lazy", onClick: function () { props.onView && props.onView(a); } })
      : h("div", { className: "md-plate-empty" }, props.overlay || null),
    a.image_id && props.overlay ? h("div", { className: "md-plate-overlay" }, props.overlay) : null,
    h("div", { className: "md-plate-actions" },
      busy ? h("span", { className: "md-plate-state" }, a.status === "pending" ? "排队中" : "正在画…")
        : props.canDraw ? h("button", { type: "button", className: "md-btn md-xs", onClick: function () { props.onDraw(a); } }, a.image_id ? "重画" : a.status === "failed" ? "没画成，重画" : "画这张") : null,
      a.image_id ? h("a", { className: "md-btn md-xs", href: imgURL(a.image_id), download: (a.kind + "-" + (a.no || 0)) + ".png" }, "下载") : null),
    a.status === "failed" && a.error ? h("p", { className: "md-plate-err", title: a.error }, a.error) : null);
}

function ArticleView(props) {
  const lines = String(props.text || "").split("\n");
  return h("div", { className: "md-article" + (props.format === "article" ? " md-prose" : "") }, lines.map(function (l, i) {
    if (/^##\s+/.test(l)) return h("h4", { key: i }, l.replace(/^##\s+/, ""));
    if (/^>\s?/.test(l)) return h("p", { key: i, className: "md-quote" }, l.replace(/^>\s?/, ""));
    if (!l.trim()) return h("div", { key: i, className: "md-gap" });
    return h("p", { key: i }, l.replace(/\*\*(.+?)\*\*/g, "$1"));
  }));
}

function ShotCard(props) {
  const a = props.asset;
  const [edit, setEdit] = useState(false);
  const [v, setV] = useState({ shot: a.shot, seconds: a.seconds, visual: a.visual, voice: a.voice, prompt: a.prompt });
  useEffect(function () { if (!edit) setV({ shot: a.shot, seconds: a.seconds, visual: a.visual, voice: a.voice, prompt: a.prompt }); }, [a.shot, a.seconds, a.visual, a.voice, a.prompt, edit]);
  async function save() {
    try { await send("/ideas/" + props.ideaId + "/assets/" + a.id, "PUT", { shot: v.shot, seconds: Number(v.seconds) || 5, visual: v.visual, voice: v.voice, prompt: v.prompt }); setEdit(false); props.onChanged(); }
    catch (err) { props.onError("保存失败：" + errText(err)); }
  }
  function upd(k, val) { const o = Object.assign({}, v); o[k] = val; setV(o); }
  return h("div", { className: "md-shot" },
    h(Plate, { asset: a, canDraw: props.canDraw, onDraw: props.onDraw, onView: props.onView }),
    h("div", { className: "md-shot-head" }, h("strong", null, "第 " + a.no + " 镜"), h("span", { className: "md-tag" }, a.shot || "中景"), h("span", { className: "md-hint" }, (a.seconds || 5) + " 秒"),
      h("button", { type: "button", className: "md-link md-xs", onClick: function () { setEdit(!edit); } }, edit ? "收起" : "修改")),
    edit ? h("div", { className: "md-stack" },
      h("div", { className: "md-bar" },
        h("input", { value: v.shot, maxLength: 12, "aria-label": "景别", style: { width: "6em" }, onChange: function (e) { upd("shot", e.target.value); } }),
        h("input", { type: "number", min: 1, max: 60, value: v.seconds, "aria-label": "秒数", style: { width: "5em" }, onChange: function (e) { upd("seconds", e.target.value); } })),
      h("textarea", { className: "md-textarea", rows: 2, value: v.visual, placeholder: "画面", onChange: function (e) { upd("visual", e.target.value); } }),
      h("textarea", { className: "md-textarea", rows: 2, value: v.voice, placeholder: "口播", onChange: function (e) { upd("voice", e.target.value); } }),
      h("textarea", { className: "md-textarea", rows: 2, value: v.prompt, placeholder: "构图描述（英文，生图用）", onChange: function (e) { upd("prompt", e.target.value); } }),
      h("button", { type: "button", className: "md-btn md-xs md-primary", onClick: save }, "保存")) : h("div", null,
      h("p", null, h("span", { className: "md-hint" }, "画面："), a.visual || "-"),
      h("p", null, h("span", { className: "md-hint" }, "口播："), a.voice || "-")));
}

function Viewer(props) {
  const a = props.asset;
  useEffect(function () {
    const f = function (e) { if (e.key === "Escape") props.onClose(); };
    window.addEventListener("keydown", f);
    return function () { window.removeEventListener("keydown", f); };
  }, []);
  return h("div", { className: "md-viewer", role: "dialog", "aria-label": "查看大图", onClick: props.onClose },
    h("img", { src: imgURL(a.image_id), alt: a.caption || a.visual || "画面", onClick: function (e) { e.stopPropagation(); } }),
    h("button", { type: "button", className: "md-btn", onClick: props.onClose }, "关闭"));
}

function Studio(props) {
  const id = props.id;
  const [d, setD] = useState(null);
  const [msg, setMsg] = useState("");
  const [format, setFormat] = useState("");
  const [draw, setDraw] = useState(null);
  const [styles, setStyles] = useState(null);
  const [input, setInput] = useState("");
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [view, setView] = useState(null);
  const [ideaEdit, setIdeaEdit] = useState(false);
  const [ideaV, setIdeaV] = useState({ title: "", body: "", tags: "" });
  const chatEnd = useRef(null);

  async function load() {
    try { setD(await api("/ideas/" + id)); } catch (err) { setMsg("读取失败：" + errText(err)); }
  }
  function loadStyles() { api("/styles").then(setStyles).catch(function () {}); }
  useEffect(function () { load(); loadStyles(); props.onStatus && props.onStatus(); }, [id]);
  const prod = d && d.production;
  const assets = (d && d.assets) || [];
  const busyAsset = assets.some(function (a) { return a.status === "running" || a.status === "pending"; });
  const live = Boolean(d && (d.producing || d.chatting || busyAsset || (prod && prod.status === "running")));
  useEffect(function () {
    if (!live) return undefined;
    const t = setInterval(load, 2000);
    return function () { clearInterval(t); };
  }, [live]);
  useEffect(function () { if (chatEnd.current && chatEnd.current.scrollIntoView) chatEnd.current.scrollIntoView({ block: "nearest" }); }, [d && d.chats && d.chats.length, d && d.chatting]);

  if (!d) return h("div", { className: "md-page" }, h(TitleBar, { parent: PLUGIN_NAME, title: "成稿", actions: [{ label: "返回", onClick: props.onBack }] }), h("p", { className: "md-hint" }, msg || "正在加载…"));
  const idea = d.idea;
  const img = imageState(props.status);
  const st = props.status || {};
  const fmt = format || (prod && prod.format) || st.draft_format || "script";
  const withImages = draw === null ? (st.generate_images !== false) : draw;
  const running = Boolean(d.producing || (prod && prod.status === "running"));
  const done = Boolean(prod && prod.status === "done");
  const cover = assets.find(function (a) { return a.kind === "cover"; });
  const images = assets.filter(function (a) { return a.kind === "image"; });
  const shots = assets.filter(function (a) { return a.kind === "shot"; });
  const activeStyle = styles && (styles.styles || []).find(function (s) { return s.id === styles.active_style_id; });

  async function produce() {
    if (done && !window.confirm("现在的脚本、封面和分镜会被替换，也会再消耗一次模型额度。重新成稿？")) return;
    try { await send("/ideas/" + id + "/produce", "POST", { format: fmt, with_images: withImages && img.ok }); setMsg(""); load(); }
    catch (err) { setMsg("开始成稿失败：" + errText(err)); }
  }
  async function cancel() { try { await send("/ideas/" + id + "/cancel", "POST", {}); load(); } catch (err) { setMsg(errText(err)); } }
  async function chat(text) {
    const t = (text || input).trim();
    if (!t) return;
    try { await send("/ideas/" + id + "/chat", "POST", { message: t }); setInput(""); load(); } catch (err) { setMsg(errText(err)); }
  }
  async function undo() { try { const r = await send("/ideas/" + id + "/undo", "POST", {}); setMsg("已撤销：" + r.label); load(); } catch (err) { setMsg(errText(err)); } }
  async function drawOne(a) { try { await send("/ideas/" + id + "/assets/" + a.id + "/draw", "POST", {}); load(); } catch (err) { setMsg("重画失败：" + errText(err)); } }
  async function saveProd(patch) { try { await send("/ideas/" + id + "/production", "PUT", patch); load(); } catch (err) { setMsg("保存失败：" + errText(err)); } }
  async function saveIdea(patch) { try { await send("/ideas/" + id, "PUT", patch); load(); } catch (err) { setMsg("保存失败：" + errText(err)); } }
  async function remove() {
    if (!window.confirm("删除《" + idea.title + "》？成稿、图片和数据会一起删掉。")) return;
    try { await api("/ideas/" + id, { method: "DELETE" }); props.onBack(); } catch (err) { setMsg("删除失败：" + errText(err)); }
  }
  async function setStyle(sid) { try { await send("/styles/active", "POST", { id: sid }); loadStyles(); } catch (err) { setMsg(errText(err)); } }
  async function copyArticle() { setMsg((await copyText(prod.article)) ? (prod.format === "article" ? "文章已复制" : "脚本已复制") : "复制失败，请手动选择文字复制"); }
  async function copyShots() {
    const text = shots.map(function (a) { return "第 " + a.no + " 镜 · " + (a.shot || "") + " · " + (a.seconds || 5) + " 秒\n画面：" + a.visual + "\n口播：" + a.voice; }).join("\n\n");
    setMsg((await copyText(text)) ? "分镜脚本已复制" : "复制失败");
  }
  function startIdeaEdit() { setIdeaV({ title: idea.title, body: idea.body, tags: (idea.tags || []).join(" ") }); setIdeaEdit(true); }

  const left = h("aside", { className: "md-studio-left" },
    h("section", { className: "md-card md-panel" },
      ideaEdit ? h("div", { className: "md-stack" },
        h("input", { value: ideaV.title, onChange: function (e) { setIdeaV(Object.assign({}, ideaV, { title: e.target.value })); } }),
        h("textarea", { className: "md-textarea", rows: 4, value: ideaV.body, placeholder: "补充", onChange: function (e) { setIdeaV(Object.assign({}, ideaV, { body: e.target.value })); } }),
        h("input", { value: ideaV.tags, placeholder: "标签，空格分隔", onChange: function (e) { setIdeaV(Object.assign({}, ideaV, { tags: e.target.value })); } }),
        h("div", { className: "md-bar" },
          h("button", { className: "md-btn md-primary md-xs", type: "button", onClick: function () { saveIdea({ title: ideaV.title, body: ideaV.body, tags: ideaV.tags.split(/\s+/).filter(Boolean) }); setIdeaEdit(false); } }, "保存"),
          h("button", { className: "md-btn md-xs", type: "button", onClick: function () { setIdeaEdit(false); } }, "取消")))
        : h("div", null,
          h("div", { className: "md-idea-top" }, h("span", { className: "md-hint" }, "灵感"), h(StatusPill, { status: idea.status }),
            h("button", { type: "button", className: "md-link md-xs", onClick: startIdeaEdit }, "修改灵感")),
          h("strong", { className: "md-idea-title" }, idea.title),
          idea.body ? h("p", { className: "md-idea-body md-open" }, idea.body) : null,
          idea.url ? h("p", { className: "md-hint" }, idea.url) : null,
          (idea.tags || []).length ? h("div", { className: "md-idea-meta" }, idea.tags.map(function (t) { return h("span", { key: t, className: "md-hash" }, "#" + t); })) : null),
      h("div", { className: "md-opts" },
        h(Field, { label: "体裁" }, h(Seg, { label: "体裁", value: fmt, onChange: setFormat, options: [["script", "口播脚本"], ["article", "文章"]] })),
        h(Field, { label: "风格" }, h("select", { className: "md-select", value: (styles && styles.active_style_id) || "", onChange: function (e) { setStyle(e.target.value); } },
          h("option", { value: "" }, "默认写法"),
          ((styles && styles.styles) || []).map(function (s) { return h("option", { key: s.id, value: s.id }, (s.preset ? "" : "我的：") + s.name); }))),
        h("label", { className: "md-check" + (img.ok ? "" : " md-disabled"), title: img.reason },
          h("input", { type: "checkbox", checked: withImages && img.ok, disabled: !img.ok, onChange: function (e) { setDraw(e.target.checked); } }), " 同时生成封面、配图和分镜图")),
      img.ok ? null : h("p", { className: "md-hint" }, img.reason),
      prod && prod.format && prod.format !== fmt && done ? h("p", { className: "md-hint" }, "这篇是按另一种体裁写的，重新成稿后生效。") : null,
      h("div", { className: "md-bar" },
        running ? h("button", { className: "md-btn", type: "button", onClick: cancel }, "取消成稿")
          : h("button", { className: "md-btn md-primary", type: "button", onClick: produce }, done ? "重新成稿" : "一键成稿"),
        d.undo && d.undo.count ? h("button", { className: "md-btn", type: "button", title: "撤销：" + d.undo.label, onClick: undo }, "撤销") : null)),
    h("section", { className: "md-card md-panel md-chat" },
      h("h4", null, "对话改稿"),
      h("div", { className: "md-chat-list" },
        (d.chats || []).length ? d.chats.map(function (c) { return h("div", { key: c.id, className: "md-msg md-msg-" + c.role }, c.text); })
          : h("p", { className: "md-hint" }, done ? "想怎么改，直接说。比如「第 3 镜换成俯拍」「表达再直接一点」。" : "成稿之后，在这里一句话改标题、脚本、封面和分镜。"),
        d.chatting ? h("div", { className: "md-msg md-msg-assistant md-busy" }, "正在改…") : null,
        h("div", { ref: chatEnd })),
      done ? h("div", { className: "md-quick" }, QUICK.map(function (q) { return h("button", { key: q, type: "button", className: "md-btn md-xs", disabled: d.chatting, onClick: function () { chat(q); } }, q); })) : null,
      h("form", { className: "md-chat-form", onSubmit: function (e) { e.preventDefault(); chat(); } },
        h("input", { value: input, disabled: !done || d.chatting, placeholder: done ? "想怎么改，直接说" : "先一键成稿", onChange: function (e) { setInput(e.target.value); } }),
        h("button", { className: "md-btn md-primary", type: "submit", disabled: !done || d.chatting || !input.trim() }, "发送"))));

  let right;
  if (!prod || prod.status === "idle") {
    right = h(Empty, { title: "还没有成稿", text: "选好体裁和风格，点「一键成稿」：标题、口播脚本、封面大字和六格分镜一起出来" + (img.ok ? "，封面、配图和分镜图同时开画。" : "。配好生图接口后还能画封面和分镜图。") });
  } else {
    right = h("div", { className: "md-stack" },
      running ? h("div", { className: "md-card md-progress", role: "status" }, h("span", { className: "md-spinner" }), STAGE_TEXT[prod.stage] || "正在成稿…") : null,
      prod.status === "failed" ? h(Note, { kind: "bad", text: "成稿失败：" + (prod.error || "未知原因") }) : null,
      prod.status === "cancelled" ? h(Note, { text: "这次成稿已取消，可以重新开始。" }) : null,
      prod.notice ? h(Note, { kind: "warn", text: prod.notice }) : null,
      h("section", { className: "md-card md-panel md-hero" },
        h("div", { className: "md-hero-text" },
          h(Field, { label: "标题", wide: true }, h("input", { className: "md-title-input", key: "t-" + prod.title, defaultValue: prod.title, disabled: running,
            onBlur: function (e) { if (e.target.value !== prod.title) saveProd({ title: e.target.value }); } })),
          h(Field, { label: "封面大字", wide: true }, h("input", { key: "c-" + prod.cover_text, defaultValue: prod.cover_text, disabled: running, maxLength: 24,
            onBlur: function (e) { if (e.target.value !== prod.cover_text) saveProd({ cover_text: e.target.value }); } })),
          (prod.facts || []).length ? h("ul", { className: "md-facts" }, prod.facts.map(function (f, i) { return h("li", { key: i }, f); })) : null,
          prod.style_name ? h("p", { className: "md-hint" }, "风格：" + prod.style_name) : null),
        cover ? h("div", { className: "md-hero-cover" }, h(Plate, { asset: cover, canDraw: img.ok && !running, onDraw: drawOne, onView: setView,
          overlay: h("span", { className: "md-cover-text" }, prod.cover_text || prod.title) })) : null),
      h("section", { className: "md-card md-panel" },
        h("div", { className: "md-bar" }, h("h4", null, prod.format === "article" ? "文章" : "口播脚本"),
          h("span", { className: "md-hint" }, String(prod.article || "").replace(/\s/g, "").length + " 字"),
          prod.article && !editing ? h("button", { className: "md-btn md-xs", type: "button", onClick: copyArticle }, prod.format === "article" ? "复制文章" : "复制脚本") : null,
          prod.article && !running ? h("button", { className: "md-btn md-xs", type: "button", onClick: function () { if (editing) { saveProd({ article: draft }); setEditing(false); } else { setDraft(prod.article); setEditing(true); } } }, editing ? "保存" : "直接修改") : null,
          editing ? h("button", { className: "md-btn md-xs", type: "button", onClick: function () { setEditing(false); } }, "取消") : null),
        editing ? h("textarea", { className: "md-textarea md-article-edit", rows: 20, value: draft, onChange: function (e) { setDraft(e.target.value); } })
          : prod.article ? h(ArticleView, { text: prod.article, format: prod.format }) : h("p", { className: "md-hint" }, running ? "正在写…" : "还没有正文。")),
      images.length ? h("section", { className: "md-card md-panel" }, h("h4", null, "配图"),
        h("div", { className: "md-images" }, images.map(function (a) {
          return h("figure", { key: a.id }, h(Plate, { asset: a, canDraw: img.ok && !running, onDraw: drawOne, onView: setView }),
            h("figcaption", { className: "md-hint" }, (a.anchor ? "放在「" + a.anchor + "」后 · " : "") + (a.caption || "")));
        }))) : null,
      shots.length ? h("section", { className: "md-card md-panel" },
        h("div", { className: "md-bar" }, h("h4", null, "分镜头脚本"),
          h("span", { className: "md-hint" }, shots.length + " 镜 · 约 " + shots.reduce(function (s, a) { return s + (a.seconds || 5); }, 0) + " 秒"),
          h("button", { className: "md-btn md-xs", type: "button", onClick: copyShots }, "复制分镜脚本")),
        h("div", { className: "md-shots" }, shots.map(function (a) {
          return h(ShotCard, { key: a.id, asset: a, ideaId: id, canDraw: img.ok && !running, onDraw: drawOne, onView: setView, onChanged: load, onError: setMsg });
        }))) : null);
  }

  const publish = h("section", { className: "md-card md-panel md-publish" },
    h(Field, { label: "平台" }, h("select", { className: "md-select", value: idea.platform || "", onChange: function (e) { saveIdea({ platform: e.target.value }); } },
      h("option", { value: "" }, "未选择"), props.platforms.map(function (p) { return h("option", { key: p, value: p }, p); }))),
    h(Field, { label: "排期" }, h("input", { type: "datetime-local", key: "s-" + idea.scheduled_at, defaultValue: idea.scheduled_at || "",
      onBlur: function (e) { if (e.target.value !== (idea.scheduled_at || "")) saveIdea({ scheduled_at: e.target.value }); } })),
    h(Field, { label: "状态" }, h("select", { className: "md-select", value: idea.status, onChange: function (e) { saveIdea({ status: e.target.value }); } },
      STATUS.map(function (s) { return h("option", { key: s[0], value: s[0] }, s[1]); }))));

  return h("div", { className: "md-page" },
    h(TitleBar, { parent: PLUGIN_NAME, title: (prod && prod.title) || idea.title, actions: [
      { label: "返回", onClick: props.onBack },
      { label: "删除", onClick: remove },
    ] }),
    h(Note, { text: msg }),
    h("div", { className: "md-studio" }, left, h("div", { className: "md-studio-right md-stack" }, publish, right)),
    view && view.image_id ? h(Viewer, { asset: view, onClose: function () { setView(null); } }) : null);
}

// ---------------------------------------------------------------------------
// 今日卡片
// ---------------------------------------------------------------------------

function TodayCard() {
  const [data, setData] = useState(null);
  const [hint, setHint] = useState("正在读取今天的排期…");
  useEffect(function () {
    let alive = true;
    api("/today?date=" + ymd(new Date())).then(function (r) { if (alive) { setData(r); setHint(""); } })
      .catch(function (err) { if (alive) setHint("读取失败：" + errText(err)); });
    return function () { alive = false; };
  }, []);
  const today = (data && data.today) || [];
  const upcoming = (data && data.upcoming) || [];
  return h("div", { className: "md-today" },
    hint ? h("p", { className: "md-hint" }, hint) : null,
    data && !today.length ? h("p", { className: "md-hint" }, "今天没有排期的内容。") : null,
    today.map(function (it) {
      return h("div", { key: it.id, className: "md-today-row" },
        h("span", { className: "md-chip-time" }, String(it.scheduled_at).slice(11, 16)),
        h("strong", null, it.prod_title || it.title),
        h("span", { className: "md-hint" }, (it.platform ? it.platform + " · " : "") + (STATUS_LABEL[it.status] || it.status) + (it.prod_status === "done" ? "" : " · 未成稿")));
    }),
    upcoming.length ? h("p", { className: "md-hint" }, "接下来：" + upcoming.map(function (it) { return fmtWhen(it.scheduled_at) + "《" + (it.prod_title || it.title) + "》"; }).join("；")) : null);
}

// ---------------------------------------------------------------------------
// 设置分节
// ---------------------------------------------------------------------------

function SettingsSection() {
  const [s, setS] = useState(null);
  const [v, setV] = useState(null);
  const [key, setKey] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    try {
      const r = await api("/settings");
      setS(r);
      const st = r.settings || {};
      const im = st.image || {};
      setV({ draft_format: st.draft_format, image_count: st.image_count, shot_count: st.shot_count, generate_images: st.generate_images,
        image_base_url: im.base_url || "", image_model: im.model || "", image_size: im.size || "", image_quality: im.quality || "" });
    } catch (err) { setMsg("读取设置失败：" + errText(err)); }
  }
  useEffect(function () { load(); }, []);
  function upd(k, val) { const o = Object.assign({}, v); o[k] = val; setV(o); }
  async function save(e) {
    if (e) e.preventDefault();
    setBusy(true);
    try {
      const body = Object.assign({}, v, { image_count: Number(v.image_count), shot_count: Number(v.shot_count) });
      if (key.trim()) body.image_key = key.trim();
      const r = await send("/settings", "PUT", body);
      setS(r); setKey(""); setMsg("已保存。");
    } catch (err) { setMsg("保存失败：" + errText(err)); } finally { setBusy(false); }
  }
  async function clearKey() {
    if (!window.confirm("删除保存的图像接口 Key？")) return;
    try { const r = await send("/settings", "PUT", { clear_key: true }); setS(r); setMsg("Key 已删除。"); } catch (err) { setMsg("删除失败：" + errText(err)); }
  }
  if (!v) return h("section", { className: "md-settings" }, h("p", { className: "md-hint" }, msg || "正在读取设置…"));
  const img = imageState({ image_configured: Boolean(v.image_base_url && v.image_model), image_has_key: s.image_has_key, network_known: s.network_known, network_granted: s.network_granted });
  return h("form", { className: "md-settings md-form", onSubmit: save },
    h("h4", null, "成稿"),
    h("div", { className: "md-form-grid" },
      h(Field, { label: "默认体裁" }, h("select", { className: "md-select", value: v.draft_format, onChange: function (e) { upd("draft_format", e.target.value); } },
        h("option", { value: "script" }, "口播脚本（一句一行）"), h("option", { value: "article" }, "文章（自然段）"))),
      h(Field, { label: "配图数量" }, h("input", { type: "number", min: 0, max: 4, value: v.image_count, onChange: function (e) { upd("image_count", e.target.value); } })),
      h(Field, { label: "分镜格数" }, h("input", { type: "number", min: 3, max: 12, value: v.shot_count, onChange: function (e) { upd("shot_count", e.target.value); } }))),
    h("label", { className: "md-check" }, h("input", { type: "checkbox", checked: Boolean(v.generate_images), onChange: function (e) { upd("generate_images", e.target.checked); } }), " 一键成稿时默认同时生图（接口可用时）"),
    h("p", { className: "md-hint" }, "文字（脚本、标题、分镜、改稿、风格提炼）用工作台配置的默认模型，经 host.agent.run 调用，会把灵感和成稿内容发给模型服务商。"),
    h("h4", null, "生图（可选）"),
    h("p", { className: "md-hint" }, "填任意兼容 OpenAI Images API 的服务（POST {地址}/images/generations），比如 https://api.openai.com/v1 + gpt-image-1。不填也能用全部文字功能。"),
    h("div", { className: "md-form-grid" },
      h(Field, { label: "接口地址（Base URL）", wide: true }, h("input", { value: v.image_base_url, placeholder: "https://api.openai.com/v1", onChange: function (e) { upd("image_base_url", e.target.value); } })),
      h(Field, { label: "模型" }, h("input", { value: v.image_model, placeholder: "gpt-image-1", onChange: function (e) { upd("image_model", e.target.value); } })),
      h(Field, { label: "尺寸" }, h("input", { value: v.image_size, placeholder: "1536x1024", onChange: function (e) { upd("image_size", e.target.value); } })),
      h(Field, { label: "质量（可选）" }, h("input", { value: v.image_quality, placeholder: "low / medium / high", onChange: function (e) { upd("image_quality", e.target.value); } })),
      h(Field, { label: s.image_has_key ? "Key（已保存，留空不改）" : "Key", wide: true }, h("input", { type: "password", value: key, autoComplete: "off", placeholder: s.image_has_key ? "已保存" : "sk-...", onChange: function (e) { setKey(e.target.value); } }))),
    h("p", { className: "md-hint" }, "Key 加密存在工作台凭证保险箱（host.secrets），不写进插件数据库，也不会回传到页面。生成的图片存在插件私有库里，单张不超过 4 MB。"),
    s.secret_error ? h(Note, { kind: "bad", text: s.secret_error }) : null,
    img.ok ? h(Note, { text: "生图可用。" }) : h(Note, { kind: "warn", text: img.reason }),
    h("div", { className: "md-bar" },
      h("button", { className: "md-btn md-primary", type: "submit", disabled: busy }, busy ? "保存中…" : "保存"),
      s.image_has_key ? h("button", { className: "md-btn", type: "button", onClick: clearKey }, "删除 Key") : null),
    msg ? h("p", { className: "md-hint", role: "status" }, msg) : null);
}

window.__PMW_PLUGIN__.register({
  id: PLUGIN_ID,
  api: API_RANGE,
  activate(ctx) {
    CTX = ctx;
    disposables.push(
      ctx.slots.register("module", { id: PLUGIN_ID, title: PLUGIN_NAME }, guarded(ModulePage)),
      ctx.slots.register("settings.section", { id: PLUGIN_ID, title: PLUGIN_NAME }, guarded(SettingsSection)),
      ctx.slots.register("today.card", { key: PLUGIN_ID + "_today", title: "今日发布" }, guarded(TodayCard)),
    );
  },
  deactivate() {
    for (const d of disposables) {
      try { if (d && typeof d.dispose === "function") d.dispose(); } catch (err) { console.warn("[pmw-plugin] dispose 失败", err); }
    }
    disposables.length = 0;
    CTX = null;
  },
});
