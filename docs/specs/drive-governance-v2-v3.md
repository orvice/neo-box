# Google Workspace Drive 治理 v2 / v3：共享盘清单与用户已暴露文件

## Problem Statement

v1（[drive-governance-v1.md](drive-governance-v1.md)）只读 Reports API：能看到谁共享了文件、谁占用多少空间，但看不到“现在有哪些共享盘、哪些没有管理员”，也看不到“某个用户名下现在有哪些文件对所有人或整个域开放”。审计事件只说明发生过什么，不能说明当前状态。

边界决策见 [ADR 0009](../adr/0009-drive-governance-reports-first.md) 与 [ADR 0008](../adr/0008-gmail-settings-impersonation-with-logged-access.md)。

## 范围（访谈结论）

- **v2 共享盘清单**：只列清单，不读成员。名称、创建时间、所属组织单位 ID、Google 报告为已开启的限制；可按名称前缀、“无组织者”、成员数上限、组织单位 ID 筛选。
- **v3 用户已暴露文件**：只列该用户名下对“任何人可找到 / 有链接的任何人 / 域内可找到 / 域内有链接”开放的文件。不列全部文件，不扫描全部用户，不显示逐个用户的权限详情。
- 文档与 v2、v3 实现放在同一个 PR。

## 与 Google 文档核对的事实（2026-10-14）

| 调用 | 接受的 scope | 说明 |
| --- | --- | --- |
| `drives.list` / `drives.get` | `drive`、`drive.readonly` | 没有只读元数据 scope，v2 只能用 `drive.readonly`（可读内容） |
| `drives.list` 的 `q` | | `useDomainAdminAccess=true` 时可用 `memberCount`、`organizerCount`、`orgUnitId`、`name`、`createdTime` |
| `files.list` | 含 `drive.metadata.readonly` | `q` 支持 `'me' in owners`、`trashed`、`visibility` |
| `permissions.list` | 含 `drive.metadata.readonly` | v2/v3 本次不用 |

## Solution

### v2：Tenant → Drive → Shared drives

- 以 Tenant 的委派管理员为 token subject，scope 仅 `drive.readonly`，`drives.list?useDomainAdminAccess=true`，字段掩码只含 `id,name,createdTime,orgUnitId,restrictions(...)`。每页 50（上限 100），用 Google 游标翻页。
- 每次请求必须带访问原因（1–200 字符，单行）。先写操作记录（`drive.shared_drives.list`），记录写不进去就不发请求；结果只记数量与是否还有下一页，筛选只记“是否使用了名称前缀”等布尔值，不记任何名称或文本。
- 限制只显示 Google 返回为 true 的；没返回的不声称为“关闭”。组织单位显示 Google 返回的 ID，不假装是路径。
- 缺少授权只在该视图提示，不改变 Tenant 健康。

### v3：User → Drive

- 先用管理员客户端在 Tenant 内解析目标用户并校验 Customer ID，再校验再次输入的主邮箱，然后才申请以该用户为 subject、仅 `drive.metadata.readonly` 的 token。
- `files.list`：`'me' in owners and trashed = false and (visibility = …)`，按修改时间倒序，每页 50（上限 100）。字段掩码为 `id,name,mimeType,modifiedTime,webViewLink,permissions(type,role,domain,allowFileDiscovery)`。
- 每个文件的暴露方式由权限推出：`anyone` + `allowFileDiscovery` → 任何人可找到，否则有链接；`domain` 同理。域暴露只有在租户已验证域名读到时才标注是否在租户外，否则显示“未检查”，不猜。Google 没返回权限的文件明确说明。
- 记录动作 `drive.files.exposed.list`：原因、目标邮箱、选择的暴露类型，以及数量（返回数、含任何人/含域/含外部域/无权限信息）和是否还有下一页、搜索是否不完整；不记文件名、链接、域名。
- `webViewLink` 只有 https 才传给前端；前端也只打开 https。

## User Stories

1. 作为管理员，我想列出域内所有共享盘及其限制，以便发现限制过松的共享盘。
2. 作为管理员，我想直接筛出没有组织者的共享盘，以便找到无人管理的盘。
3. 作为管理员，我想筛出成员很少或属于某个组织单位的共享盘。
4. 作为管理员，我想知道某个用户名下哪些文件对所有人或整个域开放，以便离职或安全审查。
5. 作为管理员，我想看到哪些域暴露是开放给租户外的域。
6. 作为管理员，我想每次读取都留下记录和原因，并且记录里没有文件或共享盘名称。
7. 作为管理员，我想看到未授权时明确的 scope 提示，而不是 Tenant 变成错误状态。
8. 作为用户，我希望其他 Neo Box 用户看不到我的 Connection、Tenant 和这些数据。

## Implementation Decisions

- 每个 Drive 客户端只带一个 scope，并且拒绝另一个 scope 对应的调用：`drive.readonly` 客户端不能列文件，`drive.metadata.readonly` 客户端不能列共享盘；构造函数拒绝完整 `drive` 等其他 scope。
- 记录逻辑与 Gmail 共用同一个“先记录、后发送、结果用不受取消影响的 context 写入”的函数（`recordAccess`）。
- 新增 RPC：`ListWorkspaceSharedDrives`、`ListWorkspaceUserExposedFiles`（`DriveAccessConfirmation{reason,target_email}`）。输入在申请 token 之前全部校验（原因、筛选值、页大小、`org_unit_id` 白名单字符、名称前缀转义）。
- 前端：Tenant Drive 标签新增 Shared drives 子标签，用户页新增 Drive 标签；确认信息与 Gmail 一样只在页面内存里保存十分钟（便利而非控制）；读取结果缓存几分钟，翻页回退、切换标签不会再发请求；“Read again”会再读一次并再记录一次。

## Testing

- Google 客户端：搜索串与转义、输入校验、日志先于请求、日志不含名称/链接/域名、scope 互斥、完整 Drive scope 被拒、权限到暴露类型的推导、域名未知不判外部、记录写不进去则不发请求、结果记录失败的报告。
- 应用层：scope 与 subject 正确、其他用户/租户的隔离、申请 token 之前拒绝、操作记录内容、未授权的错误映射且不改变 Tenant 健康。
- 前端：暴露与限制的措辞、风险等级、链接白名单、筛选输入校验、确认过期；构建、lint、类型检查。
- 真实租户未验证：需要已授权 `drive.readonly` / `drive.metadata.readonly` 的测试租户。

## Out of Scope

- 读取共享盘成员（`permissions.list`）与扫描全部共享盘找外部成员。
- 逐文件权限详情（具体哪些用户或群组有权限）。
- 扫描全部用户的已暴露文件。
- 撤销链接、修改权限、转移所有权、删除文件（需要 `drive`）。
- 下载、导出或读取文件内容。
- 持久化 Drive 数据。

## Acceptance Criteria

- [ ] 共享盘清单只用 `drive.readonly`，且只调用 `drives.list`；用户文件只用 `drive.metadata.readonly`。
- [ ] 没有访问原因、目标邮箱不符或用户不属于该 Tenant 的请求，在申请 token 前被拒绝。
- [ ] 每次读取先有操作记录；记录失败则不发请求；记录不含名称、链接或域名。
- [ ] 域暴露在域名未知时显示“未检查”，不判为内部。
- [ ] 缺少授权只在对应视图提示，不改变 Tenant 健康。
- [ ] 其他 Neo Box 用户无法访问这些数据。
- [ ] 后端、前端测试，构建，lint 与生成检查通过；真实租户未验证需明确说明。
