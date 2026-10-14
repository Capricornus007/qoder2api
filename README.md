# qoder2api

轻量级 [Qoder](https://qoder.ai) → OpenAI / Claude / Codex 兼容 API 网关。

仅包含 **Web 控制台 + Bridge**，无 macOS 桌面 GUI、无 Wails、无系统托盘，适合 Linux 服务器与本地轻量部署。

本项目基于 [wangtufly/QCCG](https://github.com/wangtufly/QCCG) **二次开发**，抽取 Bridge / 账号 / 签名核心逻辑，重做成可服务器部署的精简版本。

[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-见原项目-blue)](https://github.com/wangtufly/QCCG/blob/main/LICENSE)

---

## 🔀 本 fork（Capricornus007）：国内版 + 国际版**同时可用**

上游已经支持两区账号，但「同时在线」并不成立，本 fork 把它做成真正的双桥常驻。

| 项目 | 上游行为 | 本 fork |
|------|----------|---------|
| 8963 端口归属 | 跟随**当前激活账号**的 region：激活国际号后，8963 也变成国际桥，国内号无处可走 | 8963 **永远**是国内版桥、8964 **永远**是国际版桥，各自钉死 region |
| 切激活账号 | `restartBridge` 关掉**两台**桥再重建 → 掐断另一侧在途请求 | 只影响该区默认账号，**不重启端口**；池成员热增删 |
| 数据目录 | `--instance=cn` / `--instance=global` 两套目录（`~/.qoder2api-cn` / `~/.qoder2api-global`），两区账号彼此看不见 | 单一 `~/.qoder2api`，两区账号同池管理，按 `region` 字段分流 |
| 某区没账号 | 主桥照起，但区域错配 | 该区不起端口、控制台写明原因；**另一侧照常服务**；该区补上账号后端口自动上线 |
| 端口冲突 | 未检查，两侧同值会静默只起一台 | 启动与控制台保存都会拒绝同值 / 双零 |
| 状态与模型 | 只报一台桥、一套模型 | `/api/status`、`/api/pool`、`/api/models?region=`、连接信息全部按 CN / Global 分别给出 |
| API Key | 两桥共用一把 `bridge_token` | **两区各一把**（`cn_token` / `global_token`，留空各自回落 `qccg`）：撤权、轮换、只把某一区交给别人时互不影响 |
| 部署 | 三份 systemd unit（cn / global / 混合） | 一个进程一台服务，附 **OpenRC** runscript（Artix 无 systemd）＋ 一条命令的登录向导脚本 |

```bash
# 一条命令，两版同时对外
./qoder2api --bind=127.0.0.1
#   控制台      http://127.0.0.1:3588
#   国内版 API  http://127.0.0.1:8963/v1   （只用 region=cn 账号）
#   国际版 API  http://127.0.0.1:8964/v1   （只用 region=global 账号）
```

两区凭证在同一控制台里各自 OAuth/PAT 登录即可，互不覆盖激活状态（每个区域各有一个 active）。

---

## 🙏 特别致谢 · 贡献者

本项目的海外版签到、多账号池、图片输入等核心特性由社区贡献者 **[LeonardW-sl](https://github.com/LeonardW-sl)** 主导实现，特此致以诚挚谢意。

| 贡献 | PR | 说明 |
|------|-----|------|
| **国际版（Global）签到支持** | [`b84c41b`](https://github.com/Zhengyuuuui/qoder2api/commit/b84c41b) | 新增 `Endpoints.OpenAPIHost`，签到域名按账号 `region` 自动路由（`cn` → `openapi.qoder.com.cn`，`global` → `openapi.qoder.sh`），修复国际账号 token 打到国内端点导致 `401 TOKEN_EXPIRE` 的问题；并补充 `TestCheckinHostForRegion` 单元测试 |
| **每日自动签到时间可配置** | [`fcde08e`](https://github.com/Zhengyuuuui/qoder2api/commit/fcde08e) | 新增 `auto_checkin_times`，支持多时段签到（`HH:MM` 列表，留空默认 `10:00`） |
| **多账号池** | [`c5fa3fa`](https://github.com/Zhengyuuuui/qoder2api/commit/c5fa3fa) ~ [`afe1d11`](https://github.com/Zhengyuuuui/qoder2api/commit/afe1d11) | 账号轮换、冷却、故障转移、状态持久化、池状态接口与控制台可视化 |
| **图片输入（多模态）** | [`63cef2a`](https://github.com/Zhengyuuuui/qoder2api/commit/63cef2a) | 支持 `image_url` 类型的多模态请求 |
| OAuth 会话 TTL 可配置 | [`624ccf6`](https://github.com/Zhengyuuuui/qoder2api/commit/624ccf6) | 新增 `QODER2API_OAUTH_TIMEOUT` 环境变量 |

感谢 [@LeonardW-sl](https://github.com/LeonardW-sl) 的持续贡献与代码质量（含单元测试覆盖）。欢迎更多社区朋友参与共建。

---

## ✨ 新功能：每日签到领 100 Credits

控制台「账号列表 / 额度」区域内置一键签到，可将 Qoder 官方 **每日 100 Credits** 活动直接打进控制台：

- **🎁 一键签到**：顶部按钮批量对所有账号签到（CN 与 Global 账号可混在同一实例）
- **🎁 单账号签到**：账号表格「操作」列独立签到按钮
- **⏰ 每日自动签到**：可选开关（**默认关闭**），开启后每天 `10:00 (UTC+8)` 自动为全部账号签到；
  签到时间点可配置（支持多时段，如 `09:15,17:00`）
- **🔥 连续签到统计**：自动记录签到历史，跨天累计「连续 N 天 / 累计 N 天 / 共 N 积分」
- **幂等安全**：已领取自动跳过，重复点击不会重复领取

### 双区域支持

签到接口按账号 `region` 自动路由到对应站点，**同一控制台可同时管理国内版与国际版账号**：

| Region | `Endpoints.OpenAPIHost` | 签到接口 | `daily-check-in/status` |
|--------|-------------------------|---------|--------------------------|
| `cn` | `openapi.qoder.com.cn` | ✅ 可用 | `200`（legacy 已 `DISABLED`，统计恒 0） |
| `global` | `openapi.qoder.sh` | ✅ 可用 | `404`（该端点从未部署） |

实现由 [@LeonardW-sl](https://github.com/LeonardW-sl) 在 [`b84c41b`](https://github.com/Zhengyuuuui/qoder2api/commit/b84c41b) 贡献：`account.Endpoints` 新增 `OpenAPIHost` 字段，
`checkinAccount` 取 `checkinHostFor(acct.Region)` 后透传给 `campaignsCheckin` / `readDailyCheckinStats` / `doCheckinRequest`。

> 签到链路基于抓包还原，两区路径完全一致：
> `GET /sash/api/v1/me/campaigns` → `POST /sash/api/v1/me/campaigns/{id}/claim`，
> 仅需 device token + `cosy-clienttype: 10`，**无需 COSY 签名**。详见 `scripts/auto_checkin.py`。

### 实现说明

- **权威签到走 `campaigns`**：这是唯一真实发放积分的接口，返回 `grantId` 可核验到账
- **不依赖 `daily-check-in`**：该 legacy 端点在国内已 `DISABLED` 且对未领取日恒返回 `409`（会误判为已领取而漏领），海外则直接 `404`
- **⚠️ 必须携带完整设备指纹**：国际版（`qoder.com`）的签到活动**按设备指纹下发**——
  请求若缺少 `cosy-version` / `cosy-machineid` / `cosy-machinetoken` / `cosy-machinetype` /
  `cosy-machinecode` / `cosy-machinehostname` / `cosy-machineos` 这 7 个头，
  服务端会**不下发 `CLAIM_BENEFIT` 签到活动**，表现为「无可用签到活动」而无法领取。
  本项目复用 Bridge 的同一套 `cosy.FingerprintSeed` 派生规则（uid 优先、凭证兜底），
  保证签到与推理在同一账号上表现为同一台物理设备
- **账号准入**：服务端按账号状态灰度下发签到活动。实测新注册账号（如当天创建）
  即使携带完整指纹也不下发活动，需账号有一定使用记录后才可见
- **领取窗口**：`10:00 → 次日 09:59 (UTC+8)`，窗口未开启时点击会明确提示下次开放时间
- **连续天数本地统计**：上游 legacy 统计恒为 0，故由 qoder2api 记录 `checkin_history.json` 并按**活动窗口 `startAt` 日期**（而非自然日）计算，避免 10:00 前误记
- **调度容错**：签到时刻若活动尚未下发，不标记当日完成，每分钟重试至 12:00，避免漏签
- **多时段支持**：`auto_checkin_times` 可配置多个 `HH:MM` 时间点（贡献者 `fcde08e`）

**手动签到 API**：

```bash
# 所有账号一键签到
curl -X POST http://127.0.0.1:3588/api/checkin

# 指定单账号
curl -X POST http://127.0.0.1:3588/api/checkin -d '{"account_id":"acct_xxx"}'
```

**开启每日自动签到**（默认关闭）：控制台勾选「每日 10:00 自动签到」即可，配置存于 `settings.json` 的 `auto_checkin` 字段。
签到时间点可在控制台「自动签到时间」输入框配置（逗号分隔 `HH:MM`，留空为 `10:00`），
或直接写入 `settings.json` 的 `auto_checkin_times` 字段，例如：

```json
{ "auto_checkin": true, "auto_checkin_times": ["09:15", "17:00"] }
```

⚠️ 自动签到依赖服务常驻运行（电脑开机 + qoder2api 启动）。

---

## 更新记录

### 🏗️ 架构（本 fork）：双桥常驻 —— 国内版与国际版**同时可用**

**2026-10-14 CST** · Capricornus007 fork

上游的「单控制台 + 双桥端口」还差最后一步：**8963 属于哪个区取决于当前激活账号**。
一旦在国际号上点激活，8963 就变成国际桥，国内号没有任何端口可走；
而每次切激活账号都会 `StopBridge()` 同时关掉两台桥再重建，另一侧的在途请求被掐断；
`--instance=cn` / `--instance=global` 还把两区账号拆进两个数据目录，彼此看不见。

本 fork 把「区域」从账号身上移到**端口**身上：

| 文件 | 改动 |
|------|------|
| `service.go` | 引入 `regionBridge{region, port, server, pool, br, reason}`，两区各一实例；`ensureRegionBridge` 取代 `restartBridge`：已监听→`syncPool` 热增删账号，未监听→有账号才开端口；`StopRegionBridge(region)` 单侧停止；`SaveSettings` 端口变更只重启变化那侧；`GetStatus` / `PoolStatusByRegion` / `ListQoderModels(region)` 全部按区返回 |
| `account/store.go` | `SetActive/GetActive` → `SetActiveForRegion/GetActiveForRegion`＋`ListByRegion`：每区各有一个激活账号，互不覆盖 |
| `account/region.go` | `Regions` 列表、`Opposite()`、`Label()`、默认端口常量 |
| `account/model.go` | `Status{Bridges []BridgeStatus}`（含 `pool_size` / `accounts_total` / `reason`）；`Settings.Port` → `CNPort` + `GlobalPort` |
| `internal/bridge/pool.go` | 新增 `Remove/Has/IDs`，供池成员热退出 |
| `internal/bridge/bridge_pool.go` | 删除未钉区域的 `NewPoolBridge`；`pickSlotForModel` 的区域判定不再从池内「猜」首个 slot |
| `main.go` | `--bridge-port`/`--instance` → `--cn-port`/`--global-port`（单数据目录 `~/.qoder2api`）；端口冲突与双零校验；`/api/bridge/{start,stop}?region=`、`/api/models?region=`；`connectionInfo` 输出两区 base URL |
| `webconsole/index.html` | 状态卡改双桥（每侧 ON/OFF、端口、可用号/池内号、激活号、单侧启停按钮与未上线原因）；接入卡加区域下拉、双端口输入、另一侧 URL 提示；模型卡按区拉取 |
| `qoder2api.service` / `qoder2api.openrc` | 一份 systemd 单位（旧的 cn/global 两份 unit 删除）＋ OpenRC runscript，供 Artix 这类无 systemd 环境常驻 |
| `account/model.go`·`service.go`·`main.go` | API Key 拆成 `cn_token` / `global_token`（各留空回落 `qccg`），鉴权中间件按所在区域取自己那把；新增 `--bridge-bind`，两条桥不再硬绑 `0.0.0.0` |
| `scripts/login.sh` | 一条命令走完单区 OAuth 向导（登录控制台 → 起设备流 → 打印授权链接 → 等待 → 落盘激活 → 回显状态），只发 HTTP 不碰桌面 |
| `cmd/checkin/main.go` | 排查工具不再写死 `.qoder2api-cn` 与国内域名，改跟随默认数据目录 + 账号所在区域，空目录明确报错（原来会 index 越界 panic） |

**验证**：`account/store_test.go`（每区独立 active、空 region 归 global、端口默认值）、
`service_dual_test.go`（无账号时的双区状态与原因、只有 CN 号时只有 8963 上线、
停 CN 不打扰 Global 仍在服务、端口撞车被挡、删号热退出池、连接信息两区齐备）、
`internal/bridge/pool_test.go`（钉区域的桥永不派别区号、同区内按目录选号、`Remove` 行为）。
单测不再触碰真实上游（原先 `go test ./internal/bridge` 会打 `api2.qoder.sh`，现已隔绝）。

### 🏗️ 架构：单控制台 + 双桥端口（CN 8963 / Global 8964）

**2026-10-10 21:20 CST** · [`6199925`](https://github.com/Zhengyuuuui/qoder2api/commit/6199925)

Global Web 控制台（3589）闲置但其 Bridge 链路仍需保留，故将两个 Bridge 合并到同一控制台实例：

- 新增 `--global-port`（默认 `8964`，传 `0` 关闭）
- `--instance=cn` 单进程启动 `console 3588 + bridge 8963 + bridge 8964`
- `8964` 使用 Global 专用账号池；`8963` 保留全量池并按模型 region 路由（见下方跨区修复）
- `--instance=global` 保留为独立兜底模式（`3589 + 8964`）

---

### 🐛 修复：请求跨区域路由导致误扣 Global 账号额度

**2026-10-12 21:31 CST** · [`f4b22a0`](https://github.com/Zhengyuuuui/qoder2api/commit/f4b22a0)

**问题现象**

单控制台 + 双桥改造后，CN 桥（8963）把请求派给了 **Global 账号**并扣除其 Credits。
实测 `yusheng zheng`（国际版）在客户端未启动的情况下被消耗 549 Credits。

**根因**

8963 使用 `buildAccountPool()` 构建账号池，**不区分 region**，CN 与 Global 账号混在一起。
选号逻辑 `pickSlotForModel` 只检查模型的**目录归属**，从不检查账号的 **region**：

```go
// ❌ 修复前
slot, ok := b.pool.Pick(exclude, func(s *Slot) bool {
    return b.slotSupportsModel(s, model)   // 只看模型目录，不看 region
})
if ok { return slot, true }
return b.pool.Pick(exclude)               // 兜底完全不看 region
```

由于 `qmodel_38max` / `qfmodel` 等模型在**两个区域的目录中都存在**，
CN 请求因此会被匹配到 Global 账号并扣费。

**修复内容**

| 文件 | 修改 |
|------|------|
| `service.go` | 8963 改用 `buildRegionPool(acct.Region)`，池内只含同区域账号；8964 保持 Global 专用池；删除已无调用者的 `buildAccountPool()` |
| `internal/bridge/bridge_pool.go` | 新增 `NewPoolBridgeFor(region, pool, tmpl)` 将区域绑定到 Bridge；选号的**目录匹配**与**兜底**均增加 region 过滤，杜绝跨区选号 |
| `internal/bridge/bridge.go` | 日志新增账号名 / ID / region / 模型 |

**端口与账号的隔离关系**（修复后）：

```
console 3588    管理全部账号
bridge  8963    region=cn      仅使用 region=cn 的账号
bridge  8964    region=global  仅使用 region=global 的账号
```

**排查用日志**

每次调用上游都会记录实际使用的账号，便于核对扣费归属：

```bash
# 查看某次请求实际用了哪个账号
grep "using account" ~/.qoder2api/logs/qccg-*.log

# 跨区误用自查：8963 侧的请求只应命中 region=cn
grep "using account" ~/.qoder2api/logs/qccg-*.log | grep 'region=global'

# 某侧为什么没上线（缺账号 / 端口冲突 / 监听失败）
curl -s -b cookies http://127.0.0.1:3588/api/status | python3 -m json.tool
```

输出示例：

```
callQoder region=cn model=gmodel prompt=hi
callQoder: using account=nick8034851628 id=acct1789... region=cn model=gmodel
callQoder: account=yusheng zheng id=zysgm... region=global failed, failing over to next account: ...
```

### 签到：支持 Global 国际版 + 修复「显示签到但没到账」

**2026-10-10 ~ 2026-10-11** · [`b84c41b`](https://github.com/Zhengyuuuui/qoder2api/commit/b84c41b) 双区域 · [`fcde08e`](https://github.com/Zhengyuuuui/qoder2api/commit/fcde08e) 可配置时间 · [`2cd2ccd`](https://github.com/Zhengyuuuui/qoder2api/commit/2cd2ccd) 设备指纹

> 本节特性由社区贡献者 [@LeonardW-sl](https://github.com/LeonardW-sl) 主导实现，详见文首[致谢](#🙏-特别致谢--贡献者)。

- **双区域签到**（`b84c41b`）：`account.Endpoints` 新增 `OpenAPIHost`，签到域名按账号 `region` 自动路由
  （`cn` → `openapi.qoder.com.cn`，`global` → `openapi.qoder.sh`），
  同一控制台实例可同时管理国内版与国际版账号，无需分实例
- **修复国际版签到不可用（重要）**：抓包对比官方客户端发现，国际版签到活动**按设备指纹下发**。
  此前签到请求只带 `Authorization` + `cosy-clienttype`，缺少
  `cosy-version` / `cosy-machineid` / `cosy-machinetoken` / `cosy-machinetype` /
  `cosy-machinecode` / `cosy-machinehostname` / `cosy-machineos` 共 7 个头，
  服务端因此不下发 `CLAIM_BENEFIT` 活动，表现为「无可用签到活动」。
  现已复用 Bridge 的 `cosy.FingerprintSeed` 派生规则补齐全部指纹头，
  国际版账号签到恢复正常（实测由 `no_campaign` 变为可领取并成功到账）
- **修复空签到（重要）**：`daily-check-in/claim` 在国内已 `DISABLED`，却对**未领取日也恒返回 409**
  （被误读为「已领取」而跳过真实领取）；海外该端点直接 404。
  现已**彻底移除**对它的领取调用，`campaigns` 成为唯一权威签到路径（返回 `grantId` 可核验到账）
- **窗口日期记账**：本地签到历史改按活动窗口 `startAt` 记录，而非自然日。
  此前在 `10:00` 前点击（仍属前一日窗口）会把当天误记为已签，连续天数虚高
- **提示更明确**：未到新窗口时明确输出「已领取 YYYY-MM-DD 窗口额度（新窗口 MM-DD 10:00 开放）」
- **调度防漏签**：10:00 整点若活动尚未下发，不再标记当日完成，改为每分钟重试至 12:00
- **连续签到统计**：本地 `checkin_history.json` 按账号分别记录，
  跨天自动累计「连续 N 天 / 累计 N 天 / 共 N 积分」，支持断签识别
- **签到时间可配置**（`fcde08e`）：`auto_checkin_times` 支持多时段（HH:MM 列表），留空默认 `10:00`

### 流稳定性 / 错误分类 / 安全加固

**2026-10-07 20:56 CST** · [`be38b8f`](https://github.com/Zhengyuuuui/qoder2api/commit/be38b8f)（合并 `LeonardW-sl` 多账号池 / 图片输入 / 区域路由）

**上游流处理（对齐 hub 行为）**

- **错误帧及时中止**：上游 HTTP200 建流后在信封里投递错误帧（418/5xx 等）且不关流时，
  网关立即停止读取并按瞬时故障自动重开上游（有界重试，对调用方无感），请求不再挂起
- **空流显式报错**：上游建流成功但零有效帧即关流时返回 `empty upstream stream`；
  三协议流式路径输出 error 事件帧（chat 为 data 错误帧，claude/codex 为 `event: error`），
  非流式路径返回 500 + 错误 JSON（此前为空内容正常 finish，属与 hub 对齐的有意变更）
- **错误四分类**：上游错误按「内容审核 / 瞬时可重试 / 客户端参数 / 普通上游」分类，
  对客户端输出友好中文消息与正确 HTTP 状态（内容审核 400、瞬时耗尽 502 等）；
  同账号瞬时故障（418/5xx/传输抖动）连接层 1s/2s 退避自动重试
- **usage 同帧合并**：上游同帧返回 usage + choices 时，token 用量与内容一并返回，不再二选一

**安全**

- **Bridge 端点鉴权**：`/v1/*` 必须携带 API Key（默认 `qccg`，控制台可修改）——
  支持 `Authorization: Bearer <token>` 或 `x-api-key: <token>`，常量时间比较；
  OPTIONS 预检直接 204 放行；校验失败返回 401 + OpenAI 风格错误体。
  同网络无凭证客户端不再能消耗账号配额
- **短 token 防护**：误粘贴短 token 不再导致启动 panic，日志只输出前缀
- **PII 降级**：userinfo 原始响应日志降为 Debug 级并截断 500 字节，默认日志级别不再落盘
- **未跟踪文件隔离**：`.gitignore` 新增 `.ydevsphere/`、`webconsole/*.bak*`、`scripts/capture/`
  （抓包流量目录，可能含凭证，仅存本地、不入库，请自行处理）

**设备指纹与可观测性**

- **本机指纹盐**：`settings.json` 的 `machine_salt` 首次启动自动生成，使每个部署的
  指纹派生空间独立（uid + 本机盐派生机器码，重启不变）；**勿手动修改，变更即全部账号指纹漂移**
- **盐降级明示**：盐加载失败时日志明确写出「运行于无盐模式（hub 兼容指纹），下次启动
  恢复加盐将导致全部账号指纹漂移」；成功输出 `machine salt loaded (len=N)`；
  `cmd/checkin` 调试工具失败时向 stderr 告警（不阻断运行）

**测试与工具**

- `internal/bridge`、`internal/cosy` 新增单元/回归测试（信封重试闸门、错误帧停读信号、
  空流报错、usage 合并、指纹派生等），离线可跑、无外部依赖
- 新增 `cmd/checkin` 调试工具（只读查询活动/额度接口）与 `scripts/` 签到辅助脚本
  （`checkin.py` 等，凭证运行时从本地数据目录读取，无硬编码）

---

## 功能

- 🎁 **每日签到**：一键/单账号/定时自动领取每日 100 Credits（见上）
- 将 Qoder 账号转为本地兼容 API，供 **NewAPI**、**OpenCode**、**Claude Code**、**Codex** 等客户端使用
- Bridge 端点鉴权：`/v1/*` 需携带 API Key（默认 `qccg`，控制台可改）
- 多账号管理：支持 **OAuth** 与 **PAT**
- 账号额度展示（套餐额度 + 个人拓展包分开显示）
- 模型列表展示上下文窗口 / 最大输出 / 推理支持
- 一键复制 NewAPI 渠道配置（Base URL + API Key）
- 数据与密钥落盘，适合服务器常驻与保活

## 端口

**单进程双桥常驻**（默认，无需任何参数）：

| 服务 | 端口 | 说明 |
|------|------|------|
| Web 控制台 | `3588` | 唯一控制台，管理全部账号（CN + Global） |
| Bridge（国内版） | `8963` | **只用 `region=cn` 账号**；`--cn-port=0` 停用该侧 |
| Bridge（国际版） | `8964` | **只用 `region=global` 账号**；`--global-port=0` 停用该侧 |

数据目录：`~/.qoder2api`（两区账号集中存放，按 `region` 字段分流；冷却状态分文件 `pool_state_cn.json` / `pool_state_global.json`）

客户端接入（两个地址可同时配，互不干扰）：

- 国内版 → `http://<host>:8963/v1`
- 国际版 → `http://<host>:8964/v1`

某一区还没有账号时，那侧端口不会监听，控制台状态里会写「region "cn" 还没有可用账号…」；
**另一侧完全不受影响**，补上该区账号后端口自己上线（不需要重启进程）。

两侧端口相同或同时为 0 会被拒绝（启动日志与控制台保存都会报错）——避免一侧静默失效。

> 只想跑单侧：`--global-port=0`（只留国内版）或 `--cn-port=0`（只留国际版）。
> 想同一台机器再开一套独立实例：换 `--data-dir` 与三个端口即可，不再需要 `--instance`。

## 快速开始

### 1. 编译

> 不需要 Docker。本项目的 Dockerfile 只是可选打包；Artix / OpenRC 等无 systemd 环境直接用下面的二进制 + runscript。

```bash
git clone https://github.com/Capricornus007/qoder2api.git
cd qoder2api
go build -ldflags="-s -w" -o qoder2api .   # Go 1.27+，纯标准库、无外部依赖
```

Linux 交叉编译示例：

```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o qoder2api .
```

### 2. 启动（控制台 + 双桥一体）

一条命令同时启动 **网页控制台** 与 **两条区域桥**：

```bash
./qoder2api                        # 默认 3588 控制台 + 8963 国内桥 + 8964 国际桥
./qoder2api --cn-port=0            # 只跑国际版
./qoder2api --global-port=0        # 只跑国内版
```

可选参数：

| 参数 | 默认 | 说明 |
|------|------|------|
| `--web-port` | `3588` | Web 控制台端口 |
| `--cn-port` | `8963` | 国内版桥端口（强制 `region=cn` 账号池）；`0` = 不启动该侧 |
| `--global-port` | `8964` | 国际版桥端口（强制 `region=global` 账号池）；`0` = 不启动该侧 |
| `--bind` | `0.0.0.0` | 控制台监听地址 |
| `--bridge-bind` | `0.0.0.0` | **两条桥的监听地址**；本机自用填 `127.0.0.1`，否则同内网任何设备带着默认 key `qccg` 就能烧你的额度 |
| `--data-dir` | `~/.qoder2api` | 数据根目录（两区账号 + 密钥 + 日志 + 各区冷却状态） |
| 环境变量 `QODER2API_HOME` | — | 等同 `--data-dir` |
| 环境变量 `QODER2API_CONSOLE_PASSWORD` | — | 控制台登录密码 |

端口优先级：命令行 > `settings.json`（`cn_port` / `global_port`）> 内置默认。
控制台里改端口会 **只重启变动的那一侧**，另一侧在途请求不断。

启动成功后日志类似：

```text
qoder2api console: http://0.0.0.0:3588 (password protected)
dual bridge: CN=8963 Global=8964 (API key separate from console password)
Bridge started on port 8963 region=cn pool=1
Bridge started on port 8964 region=global pool=2
```

### 2.1 常驻运行

**OpenRC（Artix 等，无需 systemd / Docker）**：

```bash
sudo cp qoder2api /usr/local/bin/
sudo cp qoder2api.openrc /etc/init.d/qoder2api
sudo chmod +x /etc/init.d/qoder2api
sudo rc-update add qoder2api default
sudo rc-service qoder2api start
```
runscript 里可改 `qoder2api_args` / `QODER2API_HOME`（默认 `/var/lib/qoder2api`）；
建目录：`sudo install -d -m 700 /var/lib/qoder2api`。

**一条命令登录某个区域**（`scripts/login.sh`，只发 HTTP，不碰你的桌面）：

```bash
sudo cp scripts/login.sh /usr/local/bin/qoder2api-login && sudo chmod +x /usr/local/bin/qoder2api-login
qoder2api-login cn        # 打印国内版授权链接 → 浏览器点确认 → 自动落盘并在 CN 区激活
qoder2api-login global    # 同上，国际版
```
它做的是 `/api/auth/login` → `/api/oauth/start` → `/api/oauth/wait` → 回显该区的 `/api/status`，
所以服务必须已经在跑。控制台密码取 `QODER2API_CONSOLE_PASSWORD`，没设就交互输入（不回显）。

> **两区必须轮流登**：程序里同时只保留一个待授权会话（`pending` 是单例，新流会取消旧流）。
> 先 `qoder2api-login cn` 在浏览器点完确认、看到「已保存并激活」之后，再跑 `qoder2api-login global`。

**systemd**：见 `qoder2api.service`（已是单进程双桥版本，旧的 cn/global 两份 unit 不再需要）。

**Docker（可选）**：

```bash
docker build -t qoder2api .
docker run -d --name qoder2api -p 3588:3588 -p 8963:8963 -p 8964:8964 \
  -v $HOME/.qoder2api:/data qoder2api
```

### 3. 打开网页控制台（需登录）

浏览器访问：

```text
http://127.0.0.1:3588
```

会跳转到 `/login.html`。控制台密码来源（优先级从高到低）：

1. 环境变量 `QODER2API_CONSOLE_PASSWORD`
2. `~/.qoder2api/settings.json` 里的 `console_password`
3. 若都没有，**首次启动自动生成**并写入 settings，日志会打印一次

```bash
export QODER2API_CONSOLE_PASSWORD='你的强密码'
./qoder2api
```

服务器部署时替换为：

```text
http://<服务器IP>:3588
```

登录后在控制台中：

1. 「添加账号」里选 Region（`cn` / `global`）
2. 点击 **OAuth 登录**（或填写 PAT 添加）
3. 授权成功后账号写入本地，并在 **该区域** 自动激活
4. 该区域有账号后，对应端口自动上线（CN→8963、Global→8964）；另一侧不受影响

> 说明：控制台密码只保护 **管理页**；两条桥共用独立 API Key（默认 `qccg`），与 NewAPI 对接不受影响。  
> 每个区域各有自己的「激活账号」，切 CN 的号不会把国际号的激活状态抢走（旧版是全局单一 active）。

### 4. 启动 / 确认 Bridge

- **自动**：启动时两区各自尽力拉起；某侧新增账号后该侧端口自动上线
- **手动**：控制台「状态 · 双桥」有每侧的启动/停止按钮，或用 API：

```bash
# 查看两侧状态（region / port / running / pool_size / reason）
curl -b cookies http://127.0.0.1:3588/api/status

# 只停国际版，国内版继续服务
curl -b cookies -X POST 'http://127.0.0.1:3588/api/bridge/stop?region=global'

# 只启动国内版
curl -b cookies -X POST 'http://127.0.0.1:3588/api/bridge/start?region=cn'

# 两区分别确认模型（需携带 API Key，默认 qccg）
curl -s -H "x-api-key: qccg" http://127.0.0.1:8963/v1/models   # 国内版
curl -s -H "x-api-key: qccg" http://127.0.0.1:8964/v1/models   # 国际版
```

若返回模型 JSON，说明桥已正常；返回 401 说明 API Key 缺失或不匹配。

---

## 使用方法

### Bridge 端点

| 端点 | 兼容格式 |
|------|----------|
| `POST /v1/chat/completions` | OpenAI Chat |
| `POST /v1/messages` | Anthropic Claude |
| `GET  /v1/models` | 模型列表 |
| `POST /v1/responses` | OpenAI Responses (Codex) |

### 默认接入信息

| 字段 | 值 |
|------|-----|
| Base URL (OpenAI) | `http://127.0.0.1:8963/v1` |
| API Key | `qccg`（可在控制台修改） |
| 推荐模型 | `auto` |

控制台 **「NewAPI / 中转站接入」** 区域可复制完整配置。

### 接入 NewAPI

1. 打开 qoder2api 控制台 → 复制 Base URL 与 API Key  
2. NewAPI 新建渠道：
   - 类型：`OpenAI`
   - Base URL：`http://<host>:8963/v1`
   - Key：控制台中的密钥  
3. 模型填 Qoder 侧 ID，例如：`auto`、`qmodel_38max`、`qfmodel`、`gmodel`、`dmodel`、`kmodel_latest` 等  

### 接入 OpenCode

在 `~/.config/opencode/opencode.json` 中增加 provider：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "qoder2api": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Qoder2API",
      "options": {
        "baseURL": "http://127.0.0.1:8963/v1",
        "apiKey": "qccg"
      },
      "models": {
        "auto": { "name": "Qoder Auto" },
        "ultimate": { "name": "Ultimate" },
        "qmodel_latest": { "name": "Qwen Max" }
      }
    }
  },
  "model": "qoder2api/auto"
}
```

### 接入 Claude Code

```bash
# 示例：环境变量方式（也可写 ~/.claude/settings.json）
export ANTHROPIC_BASE_URL="http://127.0.0.1:8963"
export ANTHROPIC_AUTH_TOKEN="qccg"
export ANTHROPIC_MODEL="auto"
claude
```

### 命令行快速自测

```bash
curl http://127.0.0.1:8963/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer qccg" \
  -d '{"model":"auto","messages":[{"role":"user","content":"你好"}]}'
```

---

## 数据目录

```text
~/.qoder2api/
├── accounts/          # 账号元数据 JSON
├── secrets/           # OAuth/PAT 凭证（权限 0600）
├── settings.json      # 端口、bridge_token 等
└── logs/              # 运行日志
```

服务器上请保证进程用户对 `$HOME/.qoder2api` 可写；用 Docker 时挂载数据卷到 `/data`（`HOME=/data`）。

---

## Docker 部署

```bash
docker build -t qoder2api .
docker run -d --name qoder2api \
  -p 3588:3588 -p 8963:8963 \
  -v qoder2api-data:/data \
  -e HOME=/data \
  qoder2api
```

然后访问：`http://<服务器IP>:3588` 完成 OAuth 登录。

## systemd 部署

```bash
sudo mkdir -p /opt/qoder2api
sudo cp qoder2api /opt/qoder2api/
sudo cp qoder2api.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now qoder2api
```

---

## 项目结构

```text
qoder2api/
├── main.go              # 入口：Web + API
├── service.go           # 账号 / Bridge 业务
├── checkin.go           # 每日签到（手动 API + 10:00 自动调度 + 连续天数统计 + CN/Global 双区域）
├── account/             # 账号、OAuth、设置、密钥文件存储
├── cmd/checkin/         # 签到/额度只读调试工具
├── internal/
│   ├── bridge/          # OpenAI / Claude / Codex 兼容层 + 错误分类
│   └── cosy/            # Qoder 签名、会话与设备指纹
├── logger/
├── scripts/             # 签到辅助脚本与抓包工具
├── webconsole/          # 轻量 HTML 控制台
├── baseprompt.json
├── Dockerfile
└── qoder2api.service
```

---

## 致谢

- **[QCCG](https://github.com/wangtufly/QCCG)**（[wangtufly](https://github.com/wangtufly)）  
  本项目在 QCCG 之上进行二次开发：复用 / 精简了 Bridge、账号体系、OAuth 与 Qoder 协议相关实现，并改为无 GUI 的服务器友好形态。  
  **感谢原作者的开源工作。** 若你需要完整的 macOS 桌面端体验，请优先使用官方 QCCG。

- 上游思路与生态亦受益于 Qoder 社区及相关逆向/兼容项目（见 QCCG 仓库 README 中的鸣谢）。

---

## 免责声明

本项目仅供学习与自用。请遵守 [Qoder](https://qoder.ai) 服务条款与当地法律法规；账号与 API 使用风险自负。

---

## License

二次开发基于 [QCCG](https://github.com/wangtufly/QCCG) 的开源协议；请同时遵守原项目 [LICENSE](https://github.com/wangtufly/QCCG/blob/main/LICENSE) 的要求。
