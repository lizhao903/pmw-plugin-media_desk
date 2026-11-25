# 内容工作台（PMW 外部插件）

PMW（一人之下）外部插件，遵循 **PMW Plugin 2.0**（清单 `manifest_version: 2`，宿主插件 API `2.0.0`）。

- 插件 id：`media_desk`
- 版本：`0.1.0`
- 作者：lizhao903
- 许可证：MIT

一句话记灵感；点开一键生成口播脚本、标题、封面大字和六格分镜；左聊右改；拖到日历排期；发布后回填数据复盘；风格页管理写作风格。支持小红书、抖音、B站、公众号、视频号、快手、微博、知乎、TikTok、YouTube、Instagram、X 等平台的标注与排期。可选接入 OpenAI 兼容图像接口画封面、配图和黑白线稿分镜图。

## 能做什么

模块页「内容工作台」，页内用分段切换五个视图：

| 视图 | 内容 |
|---|---|
| 灵感 | 一句话记灵感（`#标签` 自动识别），可加补充和链接；卡片墙按时间排，可搜索 |
| 内容 | 看板（灵感 / 大纲 / 草稿 / 待发布 / 已发布）和表格；表格里直接改状态 |
| 排期 | 月历 + 待排期列表，拖到某一天就排好；每天的灵感邮票、连续记录天数、下一条 |
| 复盘 | 手动录入或粘贴 CSV 回填数据；汇总、30 天趋势、平台分布、表现最好的内容；按数据和改稿习惯给写作规则建议，一键写进规则 |
| 风格 | 15 个博主类型预设；贴自己的稿子让模型提炼风格卡；手动编辑；写作规则（每种风格都遵守） |

点开任意一条灵感进入成稿工作区：

- **一键成稿**：先让模型出策划案（标题、封面大字、事实要点、小标题、封面 / 配图 / 分镜画面描述），再按策划案写口播脚本（一句一行、按气口断句）或文章（自然段）。写作时贴当前风格卡和生效的写作规则。
- **左聊右改**：左边说「标题再短一点」「第 3 镜换成俯拍」，模型只回要改的字段，右边立即更新；每次修改前存快照，可以撤销。
- **直接改**：标题、封面大字、脚本、每一格分镜（景别、秒数、画面、口播、构图描述）都能手动改。
- **拿走就用**：复制脚本、复制分镜脚本、下载图片。
- 平台、排期时间、状态在工作区顶部一处设置；排期和状态联动（排上日历变待发布，取消排期退回草稿）。

今日卡片「今日发布」显示今天排期的内容和接下来几条。设置分节配置默认体裁、配图与分镜数量和生图接口。

## 模型与生图

- **文字**（策划案、脚本、改稿、风格提炼）全部经宿主 `host.agent.run`（权限 `agent:run`），用工作台配置的默认模型服务商。单次最长 240 秒；宿主转发前端请求只等 60 秒，所以成稿、改稿、提炼都做成后台任务，前端每 2 秒轮询。模型不可用时，成稿给一份结构草稿并在页面上写明原因，不会卡住。
- **生图是可选功能**：宿主没有生图接口，用户在设置分节填任意兼容 OpenAI Images API 的服务（`POST {Base URL}/images/generations`，返回 `b64_json` 或 `url` 都支持，相对地址按接口地址补全，同源下载带同一个 Key）。
  - Key 存宿主凭证保险箱 `host.secrets`（scope `media_desk`），不进插件数据库，也不回传前端。
  - 请求经 `host.http.request` 发出，需要用户在「设置 → 插件 → 外部插件」单独授予 `network:any`（图像接口地址由用户填写，不在宿主预置分组里）。
  - 未配置或未授权时，工作区的生图开关置灰并说明原因，文字功能照常可用；配好后可以单张补画。
  - 429 / 5xx 自动等待重试两次；401 / 404 等直接给中文说明，错误信息里的 Key 会被打码。

### 图片存储的取舍

生成的图片以 base64 文本存在插件私有库 `images` 表，前端经插件路由 `/images/<id>` 取回原图。没有选择「只存 URL」，原因：

1. PMW 规范禁止插件前端加载远程图片（资源必须同源），只存 URL 无法直接显示；
2. 多数生图服务返回的临时 URL 一两个小时就失效，存下来的稿子会变成空图；
3. 宿主 `host.store` 参数只接受字符串 / 数字，没有二进制列，所以用 base64 文本。

代价是体积：宿主单条 RPC 消息上限 8 MB，base64 膨胀约 4/3，所以**单张图片解码后限制在 4 MB**（超过时提示换小尺寸或低质量）。重画、撤销记录过期、重新成稿时，没有被画面或撤销快照引用的旧图会被清掉；删除灵感时连同图片一起删除。

## 权限说明

| 权限 | 理由 |
|---|---|
| `agent:run` | 用工作台配置的模型生成脚本、标题、分镜、改稿和提炼风格（会把灵感和稿件内容发给模型服务商） |
| `secrets:media_desk` | 加密保存用户填写的图像接口 Key，仅本插件可读写 |
| `network:any` | 可选生图：访问用户在设置里填写的 OpenAI 兼容图像接口，地址不固定。高风险权限，安装时不授予，需要装后单独申请并二次确认；不授权时文字功能照常可用 |

插件没有用 `net/http` 直连外网，所有出网都经 `host.http.request`。

## 数据模型（私有库）

`migrations/0001_init.sql`：

| 表 | 用途 |
|---|---|
| `ideas` | 灵感 / 内容：标题、补充、链接、标签、状态（idea / outline / draft / ready / published）、平台、排期（本地时间 `YYYY-MM-DDTHH:MM`）、发布时间 |
| `productions` | 每条灵感一份成稿：体裁、标题、封面大字、正文、AI 原稿（用于复盘改稿习惯）、事实要点、小标题、人物外观、提示、错误 |
| `assets` | 画面：封面（cover）、配图（image）、分镜（shot），含景别、秒数、画面、口播、构图描述、生图状态和图片指向 |
| `images` | 生成的图片（base64） |
| `chats` | 工作区左侧对话 |
| `revisions` | 撤销快照（每条灵感最多 20 条） |
| `metrics` | 复盘数据（手动回填） |
| `styles` | 风格卡（含 15 个预设和提炼时保留的原文） |
| `rules` | 写作规则 |
| `kv` | 当前风格、默认体裁、配图 / 分镜数量、生图接口配置（不含 Key） |

## 已知限制 / 等待宿主支持

- **流式输出**：`host.agent.run` 是单轮同步补全，上游「标题和分镜一格格长出来」的流式效果做不到，改成阶段提示（写策划案 / 写正文）+ 结果整体出现。
- **对话里的全局助手**：上游右侧助手可以一句话增删灵感、排期、改规则（结构化 ops）。本版只做成稿工作区里的改稿对话；全局助手需要外部插件的 `provides.agent_tools` 在宿主侧接通后再做。
- **宿主生图接口**：宿主没有生图能力，只能由用户自己配 OpenAI 兼容接口并授予 `network:any`。上游的百炼 / 万相异步协议、订阅生图工具没有移植。
- **单张图片 4 MB 上限**：受宿主单条 RPC 消息 8 MB 限制。
- **生图请求超时**：宿主出网代理单次最长不到 2 分钟（插件按 115 秒请求），高质量大图可能超时，可降低质量或尺寸后重画。

## 非目标

- 直接发布到各平台（工作台另有社交发布项目）。
- 平台数据自动抓取（复盘数据手填或粘贴 CSV）；上游的账号抓取 / 链接抓取风格也因此没有移植，风格提炼改为贴文字。

## 目录结构

```text
pmw-plugin-media_desk/
  plugin.json              清单 v2
  main.go                  RPC 协议层（SDK 模板）
  host.go                  生命周期、host.store / agent.run / http.request / secrets 封装
  server.go                HTTP API 路由（前端 fetch 同源路径）
  produce.go               一键成稿流水线、对话改稿、撤销
  prompts.go               提示词与模型输出解析
  images.go                可选生图（OpenAI 兼容）
  insights.go              复盘汇总与规则提案
  data.go                  私有库读写、风格预设
  data/style-presets.json  风格预设（来自上游）
  migrations/0001_init.sql 私有库表结构
  web/entry.ts             前端入口（ESM，零构建 JS 子集）
  web/entry.js             由 make web 生成
  web/style.css            作用域样式（浅色 / 深色跟随宿主）
  scripts/pmw_plugin.py    SDK 自带的校验 / 打包工具副本
```

## 本地开发

```bash
make build              # 构建当前平台后端二进制（含 make web）
make lint               # gofmt + go vet + validate
go test ./...           # 单元测试（默认不联网）
make build-all          # darwin-arm64 / linux-amd64 / windows-amd64
make pack               # 打当前平台发布包到 dist/，含 SHA256SUMS
```

安装：「设置 → 插件 → 外部插件」选本地目录、tar.gz 或 GitHub 仓库地址；装好后如需生图，在插件设置里单独授予 `network:any`，再到「设置 → 插件 → 内容工作台」填写图像接口。

## 致谢与来源

本插件移植自 [woniuniuniu/dsh-media-desk](https://github.com/woniuniuniu/dsh-media-desk)（DeepSeek Harness 自媒体工作台插件，MIT License，Copyright (c) 2026 woniuniuniu（蜗牛的科技笔记））。只做 PMW 版，没有沿用上游品牌名。

改写和移植的部分：

- 数据模型：上游 `state.json`（items / production / styles / skills / metrics）拆成 PMW 私有库关系表；
- 提示词：策划案、口播脚本 / 文章、对话改稿、风格提炼、画面风格前缀改写自上游 `src/ai.js`、`src/style.js`、`src/images.js`，去掉了流式、英文分支和全局 ops；
- 流水线：一键成稿（策划案 → 正文 + 画面并行）、改稿合并（`applyRevision`）、排期与状态联动、规则提案思路来自上游 `src/produce.js`、`src/ops.js`、`src/learn.js`；
- 风格预设：`data/style-presets.json` 是上游 `src/style-presets.js` 的 15 个中文预设（去掉了 emoji 图标）；
- 界面：五个页面的信息结构参考上游 `client/*.jsx`，按 PMW 外部插件规范用宿主 React 重写，样式全部重写。

上游 MIT 许可声明：

```text
MIT License

Copyright (c) 2026 woniuniuniu (蜗牛的科技笔记)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

由 DeepSeek Harness（dsh）协助完成。
[![](https://img.shields.io/badge/powered_by-dsh-4D6BFE?style=flat-square&logo=deepseek&logoColor=white)](https://github.com/deepseek-ai/deepseek-harness)
