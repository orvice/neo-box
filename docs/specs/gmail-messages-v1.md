# Google Workspace Gmail 邮件 v1：只读标签、搜索、查看与附件下载

## Problem Statement

管理员排查离职员工邮箱、核实某封邮件是否到达、或为合规取回一个附件时，只能让用户本人或 Google Vault 介入，Neo Box 里看不到任何邮件。Gmail 设置页（[gmail-settings-v1](gmail-settings-v1.md)）已能以访问原因和目标邮箱确认的方式模拟用户，但明确不读邮件。本 spec 把读取邮件作为单独的决策：[ADR 0010](../adr/0010-gmail-message-reading-read-only-and-logged.md)。

范围来自访谈：只读（标签、搜索、查看邮件、下载附件）；对象是 Tenant 内任意 Workspace User，沿用原因加重输邮箱的确认；每次搜索、打开邮件、下载附件都先记录，记录不含内容。修改邮件、标签和附件，以及发送邮件，留到后续。

## Solution

在 Workspace User 详情的 **Gmail** 标签下新增 **邮件** 子页：左侧标签列表，上方搜索框（Gmail 查询语法），中间结果列表，点开显示邮件头、纯文本正文、沙箱中的 HTML 正文和附件列表，附件可下载。每次访问都要有访问原因并重输目标邮箱，所有访问先写入 Workspace operation 再发送。

## User Stories

1. 作为管理员，我希望在授权说明中看到 `gmail.readonly` 的作用和风险（能读 Tenant 内任何人的邮件），以便明确知道我在授权什么。
2. 作为管理员，我希望访问某人的邮件前必须填写原因并重输该用户主邮箱，以便每次访问都有说明。
3. 作为管理员，我希望只能访问当前 Tenant 的用户；别名、其他组织的用户和找不到的地址不会换 token。
4. 作为管理员，我希望看到该用户的标签（系统与自定义）及其邮件数，以便按标签浏览。
5. 作为管理员，我希望用 Gmail 查询语法搜索，并按标签过滤，以便定位邮件。
6. 作为管理员，我希望结果按页显示（每页最多 50 封（默认 25）），带发件人、主题、日期、摘要、标签和是否有附件，并能翻页。
7. 作为管理员，我希望某封邮件的头信息读取失败时它显示为不可用，而不是从列表中消失。
8. 作为管理员，我希望打开一封邮件看到完整邮件头和纯文本正文，且打开它不会把它标成已读。
9. 作为管理员，我希望 HTML 正文在沙箱里显示，不执行脚本、不加载远程图片，以免被追踪或被注入。
10. 作为管理员，我希望看到附件的文件名、类型和大小，并能下载 25 MB 以内的附件；更大的明确提示改去 Gmail 取。
11. 作为管理员，我希望缺少 `gmail.readonly` 授权、用户无邮箱或被停用时，只在邮件页看到明确原因，Tenant 健康不变。
12. 作为管理员，我希望在操作记录里看到谁在何时因何原因对谁做了搜索、打开了哪封邮件、下载了什么类型多大的附件。
13. 作为管理员，我希望操作记录里没有搜索条件的值、主题、正文、地址和文件名，以免日志本身泄露内容。
14. 作为用户，我希望其他 Neo Box 用户无法访问我的 Connection、Tenant 和这些记录。

## Implementation Decisions

### 授权与模拟

- 新增可选 scope `https://www.googleapis.com/auth/gmail.readonly`，只在邮件请求里申请。按 2026-10 核对的 Gmail API 文档，`messages.list` / `get`、`labels.list` / `get`、`messages.attachments.get` 都接受它；`gmail.metadata` 因不能用 `q` 搜索而不选；不申请 `gmail.modify`、`compose`、`send`、`mail.google.com`。
- 目标用户的解析、Customer ID 校验、以主邮箱为 subject 换 token、token 单次使用，均沿用 ADR 0008 的 `gmailAccess` 流程；邮件客户端另申请 `gmail.readonly`，设置客户端仍只申请 `gmail.settings.basic`。
- 不增加任何批量或跨用户的入口。

### 确认与记录

- 每个请求带访问原因（1–200 字，单行）和重输的目标主邮箱，后端逐次校验，不保存服务端会话。前端沿用 Gmail 设置页的 10 分钟内存确认，同一用户共用；切换用户或关闭页面重新询问。窗口聚焦、网络恢复等不会触发重读。
- 复用 `workspace_operations`，新增 action：`gmail.labels.list`、`gmail.messages.search`、`gmail.message.read`、`gmail.attachment.download`，保存访问原因。先落 pending，落库失败则不发送；读取按现有规则重试。附件下载是流式，结果记为 `succeeded` 的时机是流结束；中途断开记为 `failed`，写不进结果则留在 `pending`。
- 记录只含白名单摘要：
  - 搜索：使用了哪些查询操作符（仅操作符名，来自固定列表，如 `from`、`to`、`subject`、`has`、`is`、`label`、`newer_than`、`older_than`、`after`、`before`、`in`、`filename`、`larger`、`smaller`），是否带标签过滤，页大小，返回数量。
  - 读邮件：邮件 ID、附件数量、是否含 HTML 部分。
  - 下载附件：邮件 ID、附件 MIME 类型、大小。
  - 标签：标签数量。
  - 都不含：查询文本及值、标签名、主题、正文、摘要、发件人 / 收件人、文件名、token、Google 原始错误信息。
- 操作记录页的现有搜索覆盖 action，输入 `gmail` 可筛出，不加筛选控件。

### 读取行为

- 标签：`labels.list` 加每个标签的 `labels.get` 取邮件数；单个标签失败只让该项计数未知。
- 搜索：`messages.list`（`q`、`labelIds`、`maxResults` ≤ 50、`pageToken`，默认不含垃圾邮件和已删除；提供“包含垃圾邮件和已删除”开关），再对每个结果用 `messages.get`（`format=metadata`，只取 `From`、`To`、`Subject`、`Date` 头）读取，并发有上限；`resultSizeEstimate` 标为估计值。`q` 长度设上限，不透传多余字段。
- 邮件详情：`messages.get`（`format=full`），解析 MIME：多部分、嵌套、`base64url`、字符集、内联图片（只在附件列表里列出，不内联渲染），取出头信息、纯文本部分、HTML 部分和附件。只有纯文本时不显示 HTML 视图，只有 HTML 时给出由 HTML 转出的纯文本回退。
- 附件下载：重新读取邮件，按 `part_id` 找到该部分；大小超过 25 MB 时在取字节之前（依据邮件里的 `size`）拒绝；再用 `messages.attachments.get` 取数据（Gmail 以 base64url 包在 JSON 里返回，所以在这一次响应期间整个文件在内存里，最大约 25 MB），解码后返回。前端用 `application/octet-stream` 的 Blob 保存，类型用邮件里声明的类型记录但不据此渲染。附件不写入磁盘或 blob 存储。
- 每个请求读取最新数据，不缓存，不存正文。

### API 与前端

- Workspace 专属 RPC：`ListWorkspaceUserGmailLabels`、`SearchWorkspaceUserGmailMessages`、`GetWorkspaceUserGmailMessage`；附件下载也是 Connect RPC `DownloadWorkspaceUserGmailAttachment`（返回文件名、类型和字节），不另开 HTTP 端点，这样鉴权、确认、错误映射和记录都走同一条路径。附件用 `part_id` 标识，而不是 Gmail 的 `attachmentId`：后者在两次读取之间会变，所以下载时重新读取邮件并按 `part_id` 找到该部分。遵循 buf lint 与现有 Connect 错误约定：缺 `gmail.readonly` 为 PermissionDenied，凭据被拒为 FailedPrecondition（不返回 Unauthenticated），邮件或附件不存在为 NotFound，附件超限为 FailedPrecondition 并带明确原因。
- 前端新增 `gmail-mail.tsx`（子页与列表）、`gmail-message.tsx`（详情与 HTML 沙箱）、`gmail-query.ts`（搜索输入处理、操作符提取用于展示，不用于日志）、沿用 `front/src/api/google-workspace.ts` 的 hooks 和现有确认对话框。
- 沙箱：`<iframe sandbox="" srcdoc=...>`，正文加 `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`。因 `sandbox` 为空（无 `allow-popups`、无 `allow-top-navigation`），正文里的链接点击无效；需要时管理员自行复制地址。

### 测试策略

- 主要边界为 Connect application 服务，沿用 Workspace 服务测试的模拟 Google API 与内存 Repository。
- 覆盖：缺少原因或目标邮箱被拒；其他 customer 的用户和别名不换 token；pending 记录写不进去则不发送；各 action 的记录内容不含查询值、主题、正文、地址和文件名；单封邮件头读取失败显示不可用；缺 scope 的错误映射；邮件不存在的错误映射；附件超限在发送前被拒；附件流中途断开记为失败；邮件读取不改变标签。
- `internal/googleworkspace` 测试 MIME 解析（多部分、嵌套、`base64url`、非 UTF-8 字符集、内联与普通附件、仅 HTML、仅纯文本、畸形输入）和操作符提取白名单。
- 前端 Vitest：HTML 沙箱属性和 CSP、纯文本回退、查询操作符展示。PostgreSQL 仓库测试验证新 action 与原因字段的存取和用户隔离，未配置 `NEOBOX_TEST_POSTGRES_DSN` 时如实报告跳过。
- 真实 Gmail 行为需要授权的测试租户，没有则明确写成未验证。

## Acceptance Criteria

- [ ] 没有访问原因或目标邮箱不一致时，后端拒绝任何邮件请求，包括附件下载。
- [ ] 其他 Tenant / customer 的用户、别名和未找到的地址无法触发 token 换取。
- [ ] 每次标签、搜索、读邮件、附件下载在发送前有 pending 记录；记录失败时不发送。
- [ ] 操作记录含访问原因、邮件 ID 等白名单字段；不含查询值、标签名、主题、正文、摘要、地址、文件名、token、Google 原始错误。
- [ ] 请求只申请 `gmail.readonly`（邮件）或 `gmail.settings.basic`（设置），不含修改、发送或 Drive scope。
- [ ] 搜索每页至多 50 封，头信息读取失败的结果显示为不可用；打开邮件不改变其已读状态。
- [ ] HTML 正文只在无脚本、无外部请求的沙箱里显示；无正文时回退到纯文本。
- [ ] 附件只在该次响应的内存里经过，不落盘；超过 25 MB 的在取字节前被拒并有明确提示。
- [ ] 缺 `gmail.readonly` 只在邮件页显示，不改变 Tenant 健康。
- [ ] 适用的后端、PostgreSQL 仓库、前端测试、构建和生成检查通过；跳过项明确报告；真实 Gmail 行为未验证时明确说明。

## Out of Scope

- 修改邮件（已读 / 未读、归档、垃圾箱、删除）、增删改标签、给邮件加减标签、发送或起草邮件。
- 跨用户搜索或批量扫描、导出邮件、保存邮件或附件到 Neo Box。
- 会话（thread）视图、草稿、`mbox` / `eml` 导出。
- 向被访问的用户发送通知。
- 在操作记录里保存搜索条件文本。

## Further Notes

- 本文档是 spec；ADR 0010 记录了边界决策。`docs/google-workspace.md` 的 “Gmail mail” 一节面向管理员说明授权和风险。
- 实施中与最初设计的偏差：附件用 RPC 返回字节而不是 HTTP 流式端点，并以 `part_id` 标识（见上）；默认页大小为 25（上限 50）；搜索结果的“有附件”只是依据邮件结构的提示。
- Google 把 `gmail.readonly` 列为受限（restricted）scope；因为凭据是管理员自己拥有的 Service Account，不走公开应用的验证流程，但文档需向管理员说明授权后的覆盖范围。
- 后续顺序建议：修改邮件与标签（`gmail.modify`，新 ADR）→ 有范围限制的跨用户搜索（需要选定用户集合并另行决策）。
