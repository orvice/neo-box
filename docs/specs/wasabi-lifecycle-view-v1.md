# Wasabi Object Lifecycle View v1：只读解析补全、风险诊断与跨桶视图

Status: implemented

## Problem Statement

Neo Box 已支持通过 Wasabi Stats API 与 S3 API 只读同步每个 Wasabi Connection 的用量、费用估算及 Bucket settings，并在单个 Bucket 详情页的 Settings 卡片底部展示简化的生命周期规则表。但当前的 Lifecycle 只读展示存在三个明显缺口：

1. **规则解析不完整甚至失真**：`ConfigReader` 仅解析 `Filter.Prefix`（及旧版顶层 `Prefix`）、`Expiration.Days`、`ExpiredObjectDeleteMarker`、`NoncurrentDays` 和 `AbortMultipartDays`。当规则使用 `Filter.And`（如同时配置前缀与对象大小过滤）、`ObjectSizeGreaterThan` / `ObjectSizeLessThan`、`Expiration.Date` 或 `NoncurrentVersionExpiration.NewerNoncurrentVersions` 时，当前实现会丢失这些条件，甚至把带前缀/大小限制的规则误显示为 `all objects`；同时后端已读取的 `ExpiredObjectDeleteMarker` 在前端表格中未展示。
2. **缺少与 Wasabi 计费和保留机制联动的诊断**：Wasabi Pay-Go 存在 90 天最低存储期（提前删除仍计为 Deleted storage），且 Object Lock / Wasabi Compliance 会阻断未到期对象的删除、MFA-Delete 与 Lifecycle 互斥。目前仅有 `old_versions_kept` 一条诊断，且对仅覆盖局部前缀的规则无法区分全桶覆盖与局部覆盖。
3. **缺少账号级全局视图**：用户需要逐个点进每个 Bucket 详情页才能查看 Lifecycle 规则，在 Connection 详情页的 Buckets 列表中看不出各桶是否配置了 Lifecycle，也无法跨 Bucket 快速排查哪些规则存在 `< 90 天` 提前删除计费风险或哪些桶未配置任何清理策略。

第一阶段（v1）聚焦 **View（纯只读）**：在完全不引入写权限（`s3:PutLifecycleConfiguration`）的前提下，补全解析准确度、增强只读诊断并升级单桶与账号级视图，同时为后续可能的写管理（v2）打好数据结构基础。

## Solution

复用现有 Wasabi Connection 的只读 S3 同步链路（`wasabisync` → `wasabi.ConfigReader` → `wasabi_bucket_configs` JSONB → `WasabiService.ListWasabiBuckets`），在不新增 IAM 权限、不改数据库表结构、不新增强制写依赖的前提下完成三项增强：

1. **完整只读解析**：修复 `Filter.And` 过滤条件解析，补齐对象大小范围（`ObjectSizeGreaterThan` / `ObjectSizeLessThan`）、对象标签（若返回则忠实展示，不误判为全桶）、按日期过期（`Expiration.Date`）以及非当前版本保留数量（`NewerNoncurrentVersions`），并在前端补齐 `ExpiredObjectDeleteMarker` 展示。
2. **Lifecycle 诊断增强**：在现有 `BucketFindings` 中新增与 Wasabi 计费/保留策略强相关的只读 Findings（提前删除计费提醒、被 Object Lock / Compliance 保留期阻断提示、旧版本清理仅覆盖局部范围提示），遵循 `CONTEXT.md` 约定仅作为 Bucket settings findings 展示，不触发 Alert。
3. **单桶 + 跨桶视图升级**：
   - **Connection 详情页**：在 Buckets 表的 `Settings` 列增加轻量 Lifecycle 标识，并在连接页提供跨 Bucket 的 **Lifecycle rules** 汇总视图（支持按 Bucket、启用状态、风险筛选，点击跳转对应 Bucket 页）。
   - **Bucket 详情页**：升级 Settings 卡片内的 Lifecycle 规则表，清晰呈现作用范围、当前版本动作、历史版本动作、未完成分片清理及 Wasabi 生命周期运行特性说明（每日 `00:00 UTC` 执行、变更 24–48h 生效、未配置分片规则时 Wasabi 默认约 31 天清理、MFA-Delete 互斥提示）。

## User Stories

1. 作为用户，我希望在 Bucket 详情页准确看到每条 Lifecycle 规则的完整作用域（前缀、最小/最大对象大小、以及可能存在的标签条件），而不是被错误显示为 `all objects`。
2. 作为用户，我希望看到每条规则的完整动作配置，包括按天数或指定日期过期、清理孤立 Delete Marker（`ExpiredObjectDeleteMarker`）、清理旧版本天数与保留最近 N 个非当前版本（`NewerNoncurrentVersions`）、以及中止未完成分片上传天数。
3. 作为用户，我希望在规则配置了小于 90 天的过期或旧版本删除周期时看到明确提示，以便识别可能导致 Wasabi Deleted storage 额外计费的配置。
4. 作为用户，我希望在 Bucket 的 Object Lock 默认保留期或 Wasabi Compliance 保留期长于 Lifecycle 清理周期时看到提示，以便理解为何对象尚未被 Lifecycle 删除。
5. 作为用户，我希望在开启 Versioning 但仅有局部前缀/大小过滤的旧版本清理规则时，能区分“完全无旧版本清理”与“仅覆盖部分对象”，以便评估潜在存储增长。
6. 作为用户，我希望在 Connection 详情页的 Buckets 列表中一眼看出每个 Bucket 是否配置了 Lifecycle 规则及启用规则数量，以便快速定位未配置清理策略的桶。
7. 作为用户，我希望在 Connection 详情页通过跨 Bucket 的 Lifecycle 汇总视图集中浏览、搜索和筛选整个账号的所有规则，并能直接跳转到对应 Bucket 详情页。
8. 作为用户，我希望在 Bucket 开启了 MFA-Delete 或未配置权限读取 Lifecycle 时，看到准确的状态原因，而不是将其误判为“无规则”。
9. 作为用户，我希望整个升级完全保持现有只读子账号策略，不要求授予 `s3:PutLifecycleConfiguration` 等写权限。

## Implementation Decisions

### S3 Lifecycle 解析与领域模型

- 扩展 `internal/wasabi/bucketconfig.go` 中的 `LifecycleRule`（字段均使用 `omitempty`，与 `wasabi_bucket_configs.config` 既有 JSONB 历史数据完全向后兼容）：
  - 保留既有字段：`ID`、`Enabled`、`Prefix`、`ExpirationDays`、`ExpiredObjectDeleteMarker`、`NoncurrentDays`、`AbortMultipartDays`。
  - 新增字段：
    - `ObjectSizeGreaterThan *int64`（单位：bytes；使用指针区分未设置与 `0`）
    - `ObjectSizeLessThan *int64`（单位：bytes；使用指针区分未设置与 `0`）
    - `Tags map[string]string`（尽管 Wasabi 当前不支持在 Lifecycle 中配置 Tag 过滤，若 API 返回仍忠实保留，避免将带标签规则误判为全桶匹配）
    - `ExpirationDate string`（UTC `YYYY-MM-DD`，与 `wasabi.DateLayout` 一致，由 S3 `Expiration.Date` 转换）
    - `NewerNoncurrentVersions int`（来自 `NoncurrentVersionExpiration.NewerNoncurrentVersions`，`0` 表示未设置）
- 修复 `internal/wasabi/configreader.go` 的 `lifecycleRule(r s3types.LifecycleRule)` 解析逻辑：
  - 当 `r.Filter != nil`：
    - 若 `r.Filter.And != nil`：同时读取 `And.Prefix`、`And.ObjectSizeGreaterThan`、`And.ObjectSizeLessThan` 及 `And.Tags`。
    - 否则读取单条件：`r.Filter.Prefix`、`r.Filter.ObjectSizeGreaterThan`、`r.Filter.ObjectSizeLessThan` 及 `r.Filter.Tag`。
  - 若 `r.Filter == nil` 且旧版顶层 `r.Prefix != nil`：回退读取 `r.Prefix`。
  - 若 `r.Expiration != nil` 且 `r.Expiration.Date != nil`：格式化为 UTC `YYYY-MM-DD` 写入 `ExpirationDate`。
  - 若 `r.NoncurrentVersionExpiration != nil`：补充读取 `NewerNoncurrentVersions`。
- 新增辅助方法 `(r LifecycleRule) AppliesToAllObjects() bool`：仅当 `Prefix == ""`、`ObjectSizeGreaterThan == nil`、`ObjectSizeLessThan == nil` 且 `len(Tags) == 0` 时返回 `true`。
- 不解析 AWS 专属的 `Transitions` / `NoncurrentVersionTransitions`（Wasabi 为单一热存储层级，不支持存储类别沉降）。

### Findings 诊断规则

延续 `internal/wasabi/bucketconfig.go` 的 `BucketFindings(c BucketConfig)` 纯函数推导逻辑（每次响应动态计算，不依赖额外存储，也不触发 Alert，符合 `CONTEXT.md`）：

- **保留并细化旧版本清理诊断**（前提：`Versioning.Status == "Enabled"` 且 `c.Errors[SettingLifecycle] == ""`）：
  - `old_versions_kept`（`Warning`）：没有任何**已启用**规则设置 `NoncurrentDays > 0`。
  - `old_versions_partially_expired`（`Info`）：存在已启用且 `NoncurrentDays > 0` 的规则，但**没有任何一条**满足 `AppliesToAllObjects()`（即全部带前缀/大小/标签过滤）。提示仅部分对象会清理旧版本，其余对象的历史版本仍会长期计费。
- **新增 `early_deletion_lifecycle`（`Warning`）**：
  - 条件：`c.Errors[SettingLifecycle] == ""` 且存在至少一条**已启用**规则满足 `0 < ExpirationDays < 90` 或 `0 < NoncurrentDays < 90`（常量 `MinStorageDurationDays = 90`）。
  - 文案需明确列出最短天数，说明 Wasabi Pay-Go 默认 90 天最低存储期——提前删除的对象在剩余天数内仍会按 *Deleted storage* 计费（保留天数已满足的预付费/30 天计划可忽略）。
  - 仅按单桶聚合为 1 条 Finding（报告命中的规则数及最短天数），避免多条规则刷屏。
- **新增 `lifecycle_blocked_by_retention`（`Info`）**：
  - 有效默认保留天数 `retentionDays` 计算：
    - 若 `ObjectLock != nil && ObjectLock.Enabled`：`Days`（或 `Years * 365`，取大于 0 者）。
    - 否则若 `Compliance != nil && Compliance.Enabled`：`Compliance.RetentionDays`。
  - 触发条件（严格小于 `<`，因 `ruleDays == retentionDays` 属于推荐配合模式，保留期满次日即可清理）：
    - 当 `ObjectLock` 启用默认保留时：存在已启用规则且 `0 < NoncurrentDays < retentionDays`（注：版本化桶的 `ExpirationDays` 仅写入 Delete Marker，本身不被 Object Lock 阻止，真正被保留期阻断的是永久删除旧版本 `NoncurrentDays`）。
    - 当 Wasabi `Compliance` 启用时：存在已启用规则且 `(0 < ExpirationDays < retentionDays) || (0 < NoncurrentDays < retentionDays)`。
  - 聚合为单条 Finding，说明在保留期满前 Lifecycle 无法永久删除受保护对象。
- 严重度排序维持 `Critical` → `Warning` → `Info`。

### API 与存储契约

- 扩展 `proto/neobox/v1/wasabi.proto` 的 `WasabiLifecycleRule`（仅追加新 field number，保持二进制与 JSON 向后兼容）：
  - `optional int64 object_size_greater_than = 8;`
  - `optional int64 object_size_less_than = 9;`
  - `map<string, string> tags = 10;`
  - `string expiration_date = 11;`
  - `int32 newer_noncurrent_versions = 12;`
- 更新 `WasabiBucketFinding.code` 注释，纳入 `old_versions_partially_expired`、`early_deletion_lifecycle`、`lifecycle_blocked_by_retention`。
- 同步更新 `internal/application/wasabi_service.go` 中的 `bucketConfigToProto`，传递新增字段。
- **不新增 RPC**：Connection 详情页和 Bucket 详情页已调用 `ListWasabiBuckets`，返回的每个 `WasabiBucket.config` 已包含完整 `lifecycle_rules` 与 `findings`。
- **不新增或修改 ent schema**：`wasabi_bucket_configs` 存的是 `BucketConfig` JSONB，新字段将在下一次同步（定时 02:30 UTC 或点击 Sync now）后自动填充；同步前的旧记录新字段优雅退化为空/未设置。
- 保持既有约束：Stats API 尚未出现记录的新建桶在首次出数前不出现在 `ListWasabiBuckets` 中，本 v1 不改变该同步模型。

### 前端视图与信息架构

- 新增纯逻辑模块 `front/src/features/wasabi/lifecycle.ts`（配 Vitest 单元测试），负责：
  - 作用域文案格式化（`All objects`、前缀、`≥ X` / `< Y` 字节范围、标签列表）。
  - 动作文案格式化（当前版本：`After N days` / `On YYYY-MM-DD` / `Remove expired delete markers`；历史版本：`After N days` + `Keep newest M`；未完成分片：`After N days`）。
  - 跨 Bucket 规则展平与统计（总规则数、启用/停用数、有规则桶数 / 无规则桶数、`< 90 days` 规则数）及客户端筛选逻辑。
- **Bucket 详情页**（`front/src/features/wasabi/bucket-settings.tsx`）：
  - 升级 Settings 卡片内的 `Lifecycle rules` 表格：
    - 列：`Rule`（名称 + `Off` 徽标 + `< 90d` 风险标记）、`Applies to`、`Current versions`、`Noncurrent versions`、`Incomplete uploads`。
    - 当 `ExpirationDays` / `NoncurrentDays` 小于 90 天且规则启用时，行内给出清晰提示（与顶部 `early_deletion_lifecycle` Finding 呼应）。
    - 当 Bucket 开启 `mfaDelete` 且无规则时，空态补充说明：Wasabi 上 MFA-Delete 与 Lifecycle 规则互斥。
    - 表格底部附一行简明说明：Wasabi 每日 `00:00 UTC` 评估生命周期（变更后通常 24–48 小时首次生效）；未配置分片清理规则时 Wasabi 默认约 31 天清理未完成分片。
- **Connection 详情页**（`front/src/features/wasabi/connection-detail.tsx`）：
  - `SettingsBadges` 增加轻量 `Lifecycle (N)` 徽标（显示已启用规则数；若全部规则均已禁用则显示 `Lifecycle (off)`；无规则时不展示以保持列表简洁）。
  - 在 `BucketsCard` 下方（或独立卡片）增加 **Lifecycle rules** 跨桶只读总览卡片：
    - 顶部摘要展示已配置 Lifecycle 的 Bucket 数量、启用规则总数及 `< 90 天` 规则数（若有）。
    - 支持按关键字（Bucket 名 / Rule ID / Prefix）、状态（All / Enabled / Disabled / `< 90 days`）筛选；点击 Bucket 名跳转对应 Bucket 详情页。
    - 当整个连接无任何规则或 bucket settings 不可读时，给出清晰的空态或权限提示（复用现有 `configError` / `access_denied` 文案）。

### 权限与安全边界

- **无新增 IAM 权限**：继续仅依赖既有只读策略中的 `s3:GetLifecycleConfiguration`（[README.md](../../README.md#bucket-settings)）。
- 延续 `ConfigReader` 既有语义：缺少 `s3:GetLifecycleConfiguration` 时记录 `errors["lifecycle"] = "access_denied"`，前端显示 `Needs permission`，不得伪装成“无规则”；`NoSuchLifecycleConfiguration` 正常视为空规则列表。
- 保持用户与 Connection 隔离，所有数据读取走既有 `WasabiServiceServer` 鉴权与归属校验。

### 仓库约定

- 修改 `proto/neobox/v1/wasabi.proto` 后运行 `make buf` 并提交生成的 `pkg/proto/` 与 `front/src/gen/`，不手改生成代码。
- 实施后视需要更新 `CONTEXT.md` 中 Wasabi `Bucket settings` 的简短描述（补充 Lifecycle 诊断与跨桶视图）；本 spec 不改变现有 ADR 的架构决定。

## Testing Decisions

重点验证外部可观测行为（S3 XML 解析、Findings 规则推导、RPC 字段映射、前端格式化与筛选逻辑），不依赖生产 Wasabi 凭据：

- **S3 解析测试**（`internal/wasabi/configreader_test.go`）：扩展 `fakeS3` 用例，覆盖：
  - `Filter.And` 同时包含 `Prefix` + `ObjectSizeGreaterThan` + `ObjectSizeLessThan`
  - 单一 `Filter.ObjectSizeGreaterThan` / `ObjectSizeLessThan`（含边界值 `0`）
  - 旧版顶层 `<Prefix>` 兼容
  - `<Expiration><Date>` 与 `<Expiration><ExpiredObjectDeleteMarker>true</ExpiredObjectDeleteMarker>`
  - `<NoncurrentVersionExpiration><NoncurrentDays>` + `<NewerNoncurrentVersions>`
  - `<Tag>` 过滤（确保不会误判为 `AppliesToAllObjects`）
  - `NoSuchLifecycleConfiguration` 与 `AccessDenied` 错误区分
- **Findings 单元测试**（`internal/wasabi/configreader_test.go`）：覆盖：
  - `old_versions_kept` vs `old_versions_partially_expired`（全桶规则 vs 仅前缀/大小过滤规则 vs 禁用规则）
  - `early_deletion_lifecycle`（`< 90` 触发、`== 90` 不触发、禁用规则不触发、多条规则聚合为一条）
  - `lifecycle_blocked_by_retention`（Object Lock `Days`/`Years` 与 `NoncurrentDays` 比较、`==` 边界不触发、Object Lock 仅设 `ExpirationDays` 不误报、Wasabi Compliance `RetentionDays` 与 `ExpirationDays`/`NoncurrentDays` 比较）
- **Service 映射测试**（`internal/application/wasabi_service_test.go`）：验证 `ListWasabiBuckets` 正确返回扩展后的 `WasabiLifecycleRule` 字段与新 Finding codes。
- **前端单元测试**（`front/src/features/wasabi/lifecycle.test.ts`）：使用 Vitest 验证作用域文案、动作文案、`< 90d` 判定、跨桶规则展平与筛选逻辑；并运行 `npm run build`、`npm run lint`、`npm run test`。

### Acceptance Criteria

- [ ] `ConfigReader` 正确解析 `Filter.Prefix`、`Filter.And`、旧版 `Prefix`、`ObjectSizeGreaterThan`/`ObjectSizeLessThan`、`Tag`、`Expiration.Days`/`Date`/`ExpiredObjectDeleteMarker`、`NoncurrentDays`/`NewerNoncurrentVersions` 和 `AbortMultipartDays`；带 `Filter.And` 的规则不再误显示为 `all objects`。
- [ ] `BucketFindings` 准确输出 `old_versions_kept`、`old_versions_partially_expired`、`early_deletion_lifecycle`（`< 90` 天）与 `lifecycle_blocked_by_retention`，且全部不触发 Alert。
- [ ] `WasabiLifecycleRule` proto 与 `ListWasabiBuckets` 响应向后兼容；已有 JSONB 历史数据反序列化不报错。
- [ ] Bucket 详情页完整展示单桶 Lifecycle 规则（含作用域、当前版本、旧版本、未完成分片及 Wasabi 运行/互斥说明），缺少权限时显示 `Needs permission` 而非空规则。
- [ ] Connection 详情页的 Buckets 列表展示 Lifecycle 徽标，并提供跨 Bucket 的 Lifecycle 只读汇总与筛选视图，可跳转对应 Bucket 页。
- [ ] 无需任何新增 Wasabi IAM 写权限，也无 ent schema 迁移。
- [ ] `make buf`、`make test`、`make lint` 及前端 `npm run build` / `npm run lint` / `npm run test` 全部通过。

## Out of Scope

- Lifecycle 规则的新增、修改、启用/停用、删除（`PutBucketLifecycleConfiguration` / `DeleteBucketLifecycle`）及对应的操作审计日志（留待后续写管理 v2 spec，并需单独评估 `s3:PutLifecycleConfiguration` 授权及 `aws-sdk-go-v2` Checksum / `Content-MD5` 实测兼容性）。
- 独立的“仅刷新 Bucket Settings”实时 S3 刷新 RPC（v1 继续复用现有 sync 周期与 Sync now 按钮）。
- 对尚未出现在 Stats API 报表中的新建 Bucket 做独立列表合并。
- 按 Connection 定制最低存储期天数（如 30 天 RCS 计划配置项）；v1 统一按 Wasabi Pay-Go 90 天提示并在文案中注明适用前提。
- 将 Lifecycle Findings 接入 Telegram Alert 通知通道。
- AWS S3 存储层级沉降（`Transition` / `NoncurrentVersionTransition`）支持。

## Further Notes

- Wasabi 生命周期规则每日 **00:00 UTC** 执行一次，新建或修改规则后通常需 **24–48 小时**首次生效；超大规模版本化桶处理时间可能更长。
- Wasabi 在未配置 `AbortIncompleteMultipartUpload` 规则时，默认约 **31 天**会自动清理未完成的分片上传。
- 若后续启动 **v2（Lifecycle 写管理）**，除权限模型与操作日志外，重点需提前验证两项实操问题：
  1. `aws-sdk-go-v2/service/s3`（当前 `v1.113.3`）调用 Wasabi `PutBucketLifecycleConfiguration` 时的 `CRC32` vs `Content-MD5` 校验头兼容性；
  2. 整包覆盖语义下的 per-bucket 锁、未知字段保留与写前 90 天计费二次确认。
- 官方参考：[Wasabi Lifecycle Settings](https://docs.wasabi.com/docs/lifecycle-settings)、[Wasabi Minimum Storage Duration Policy](https://docs.wasabi.com/docs/how-does-wasabis-minimum-storage-duration-policy-work)、[Wasabi Object Lock](https://docs.wasabi.com/docs/object-lock)。
