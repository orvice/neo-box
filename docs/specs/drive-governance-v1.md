# Google Workspace Drive 治理：共享活动、存储用量，以及后续的共享盘与用户文件

## Problem Statement

管理员想知道组织里谁把文件公开到了网上、通过链接共享、共享给了外部用户，谁的 Drive 占用最多，以及有哪些共享盘。现在只能进 Google 管理控制台逐页查，且多个组织（Tenant）要来回切换。Neo Box 已有 Google 审计页，但它显示的是原始事件参数，无法按共享风险筛选，也没有存储用量。

边界决策见 [ADR 0009](../adr/0009-drive-governance-reports-first.md)。用户在访谈中要求最终覆盖四块：共享审计事件、存储用量、共享盘清单、模拟用户列出文件和共享设置。其中后两块需要 Drive API 的 scope，风险高于前两块，因此按版本分步交付。

## 版本划分

| 版本 | 内容 | 新增 scope | 能否读到文件内容 |
| --- | --- | --- | --- |
| v1 | Drive 共享活动、Drive 存储用量 | `admin.reports.usage.readonly`（审计 scope 已有） | 不能 |
| v2 | 共享盘清单（含成员数、限制设置） | `drive.readonly`（受限 scope，**可读内容**） | 技术上可以，Neo Box 不调用 |
| v3 | 模拟用户列出其文件和共享权限 | 以 `drive.metadata.readonly` 为目标（待核对） | 不能 |
| 之后 | 撤销公开链接 / 外部权限 | `drive`（最宽） | 可以，另行决策 |

本 spec 详细定义 v1；v2、v3 只固定约束，详细 spec 在做之前再写。

## v1 Solution

在 Tenant 页新增 **Drive** 标签，包含两个视图：

1. **共享活动**：读取 Google 的 Drive 审计事件，按风险预设筛选，每行显示文档标题与类型、所有者、操作者、时间、动作，以及可见性或权限的前后变化。
2. **存储用量**：按用户列出 Drive 已用空间，可排序，显示占该用户配额的比例。

两者都是实时读取，不存储，不产生 Neo Box 操作记录（和现有 Google 审计页一致）。

## User Stories

1. 作为管理员，我想一眼看到文件被设为“互联网上任何人可见”或“知道链接即可访问”的事件，以便尽快发现公开暴露。
2. 作为管理员，我想看到文件被共享给外部用户或域的事件，以及共享给了谁、权限从什么变成什么。
3. 作为管理员，我想看到所有权转移，以便发现文件被转走。
4. 作为管理员，我想看到下载和删除事件，以便排查数据外流或误删。
5. 作为管理员，我想看到共享盘的成员变更和设置变更。
6. 作为管理员，我想按时间范围、用户和预设筛选，并翻页，以便处理较大的组织。
7. 作为管理员，我想点开一个事件看全部参数，以便核对细节。
8. 作为管理员，我想看到每个用户的 Drive 用量和占配额比例，按用量排序，以便找出占用最多的账号。
9. 作为管理员，我想知道用量数据的日期，以及它为何不是今天，以便不把延迟当错误。
10. 作为管理员，我想在缺少所需 scope 或报告权限时只在对应视图看到明确原因，Tenant 的基础用户功能不受影响。
11. 作为管理员，我想在 Tenant 的授权说明里看到这两个 scope 都读不到文件内容。
12. 作为用户，我希望其他 Neo Box 用户无法看到我的 Connection、Tenant 和这些数据。

## v1 Implementation Decisions

### 共享活动

- 使用 Reports Activities API，`applicationName=drive`，限定 Tenant 的 Customer ID（沿用现有校验：返回事件的 Customer ID 和应用名必须匹配）。scope 为现有的 `admin.reports.audit.readonly`，不改变基础 scope。
- 新增 Drive 专用的读取 RPC，而不是让前端解释原始参数。后端把事件整理成：事件名、文档 ID / 标题 / 类型、所有者、所有者是否为共享盘、共享盘 ID、操作者、时间、新旧可见性、新旧角色、目标用户或域、成员变更类型；其余参数保留在详情里。缺失的字段保持缺失，不编造。
- 预设对应已核实的事件名与参数（2026-10 的 Drive 审计文档）：
  - 公开暴露：`change_document_visibility` / `change_document_access_scope`，`visibility` 为 `public_on_the_web` 或 `people_with_link`。
  - 外部共享：`change_user_access`、`change_document_visibility`，`visibility_change=external` 或 `visibility=shared_externally`。
  - 所有权：`change_owner`。
  - 下载：`download`。删除：`delete`、`trash`。
  - 共享盘：`shared_drive_membership_change`、`shared_drive_settings_change`。
  - 全部：不限事件。
- 预设优先用 Reports 的 `eventName` 与 `filters` 查询参数在 Google 端筛选，使每页结果都是匹配的；翻页沿用 Google 的游标。不能在 Google 端表达的条件，在界面上明确标出“仅在已读取的页面中筛选”，不假装是全量。
- 时间范围、用户筛选、180 天窗口、分页大小沿用现有 Google 审计页的规则。
- 一次用户操作可以产生多个事件，其中只有 `primary_event=true` 的是用户直接操作，其余是副作用。默认只显示 primary 事件，可切换显示全部。
- 文档标题和地址是 Google 提供的审计数据，原样显示；已知凭据类参数沿用现有脱敏规则。

### 存储用量

- 使用 Usage Reports 的用户用量接口（`userUsageReport`，`accounts:drive_used_quota_in_mb`、`accounts:total_quota_in_mb`、`accounts:used_quota_in_percentage`），scope 为新增的可选 `admin.reports.usage.readonly`，仅在该视图请求。
- 数据日期：Google 的用量有数日延迟。读取时从最近一天向前探测最多几天，使用第一个有数据的日期，并显示该日期。探测不到数据时显示“暂无用量数据”，不显示 0。
- 读取全部分页，沿用现有概览的上限（每 Tenant 最多 5,000 个用户、20 秒 Tenant 超时），超限或部分失败时明确标为部分结果。某用户缺少某个数值则显示未知，不当作 0。
- 排序、搜索和分页只作用于已读取的账号。
- 没有配额数据的用户（如无限配额）显示为无限制或未知，不计算比例。

### Scope 与授权说明

- 文档新增 Drive 章节：两个 scope（审计已有，用量新增）、都不能读文件内容、管理员需要的 Reports 权限、数据延迟与保留期（审计 180 天）。
- Scope 失败按现有 `mapWorkspaceFeatureErr` 报告，提示该 scope，不改变 Tenant 的基础健康。

### API 与前端

- 新增 Workspace 专属 RPC：列出 Drive 活动、列出 Drive 用量。遵循 buf lint 与现有 Connect 错误约定。
- 前端：Tenant 页新增 Drive 标签；预设选择、风险徽标（公开 / 链接 / 外部）、事件详情、用量表格。事件整理、预设到查询的映射、用量排序与比例计算放进独立模块并附 Vitest。

### 测试策略

- 后端服务测试沿用现有 Workspace 测试：模拟 Reports API，验证 Customer ID / 应用名校验、预设到 `eventName` 与 `filters` 的映射、primary 事件过滤、缺失字段保持缺失、分页游标、跨 Tenant 隔离、scope 缺失的错误映射、不改变 Tenant 健康。
- 用量：日期探测、无数据不显示为 0、缺失数值为未知、超限和部分失败标为部分结果、去重。
- 前端 Vitest 覆盖事件整理和用量计算；检查构建和 lint。
- 真实 Reports 数据需要授权的测试租户，没有则明确写成未验证。

## v1 Acceptance Criteria

- [ ] Drive 共享活动按预设返回，且预设在 Google 端筛选；翻页正确。
- [ ] 公开、链接、外部共享事件被标出，并显示新旧可见性或角色及目标用户 / 域。
- [ ] 默认只显示 primary 事件，可切换。
- [ ] 返回事件的 Customer ID 或应用名不符时拒绝，不显示。
- [ ] 存储用量显示数据日期；无数据不显示为 0；缺失数值为未知。
- [ ] 超限或部分失败时结果明确标为部分。
- [ ] 缺少 scope 或报告权限只在对应视图提示，不改变 Tenant 健康。
- [ ] 请求只使用审计和用量 scope，不含任何 Drive API scope。
- [ ] 其他 Neo Box 用户无法访问这些数据。
- [ ] 适用的后端、前端测试、构建和生成检查通过；跳过项明确报告。

## v2 / v3 约束（详细 spec 另写）

- 每个能力是 Tenant 上单独的可选授权，只在该功能的请求里申请 scope，失败不影响 Tenant 基础健康。
- 沿用 ADR 0008：访问原因、模拟用户时要重新输入主邮箱、先记录后发送、记录写不进去则拒绝、日志只含计数和标志。
- Neo Box 只调用元数据接口（`drives.list`、限定字段的 `files.list`、`permissions.list`），绝不下载或导出文件。
- v2 的 `drive.readonly` 技术上可读内容，文档必须直说，并把 Neo Box 的调用清单作为控制手段。
- v3 的一次请求只针对一个用户，不做批量；扫描型功能需要单独决策。

## Out of Scope

- 读取、下载、导出或搜索文件内容。
- 撤销或修改共享权限、转移所有权、删除文件（需要 `drive`，另行决策）。
- 自动告警（可在读取功能稳定后复用 `notify` 单独加）。
- 持久化 Drive 活动或用量、历史趋势。
- Drive 之外的 Google 产品。

## Further Notes

- 实施前核对当时的文档：Drive 审计事件名与参数、Activities `filters` 对各事件的支持范围、用量报告的参数与延迟、`drive.metadata.readonly` 是否足以调用 `files.list` 与 `permissions.list`。
- v1 已按本文实施。与上文预想不同的几处：`primary_event` 在后端按事件过滤（并返回被隐藏的事件数），没有依赖未核实的 Google 端 `primary_event` 筛选；预设“删除”和“移到回收站”、“共享盘成员”和“共享盘设置”各自独立；外部共享预设是 `change_user_access` 且 `visibility==shared_externally`；用量只读 `accounts:drive_used_quota_in_mb` 与 `accounts:total_quota_in_mb`，比例由后端计算。真实租户数据尚未验证。
- v2、v3 尚未开始，需要先写详细 spec。
