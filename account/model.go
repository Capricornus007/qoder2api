package account

import "time"

type Account struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Email     string     `json:"email,omitempty"`
	UserType  string     `json:"user_type,omitempty"`
	Plan      string     `json:"plan,omitempty"`
	Region    Region     `json:"region,omitempty"` // "global" | "cn"，空值视为 global
	AuthMode  string     `json:"auth_mode"`        // "pat" | "oauth"
	APIMode   string     `json:"api_mode"`         // "openai" | "anthropic" | "gemini" | "claude-code"
	Tags      []string   `json:"tags"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	Active    bool       `json:"active"`
	SortOrder int        `json:"sort_order"`

	// CheckinFingerprint 签到/新版推理协议专用设备指纹（可选）。
	// 国际版（qoder.com）的签到活动**按真实设备身份下发**：请求必须携带与登录
	// 时协商一致的 cosy 指纹，否则服务端不下发 CLAIM_BENEFIT 活动（表现为
	// no_campaign）。自造指纹无效，需从官方客户端抓包取得。
	// 国内版（cn）反之——发送自造 machinetoken 会被拒，留空即可。
	CheckinFingerprint *CheckinFingerprint `json:"checkin_fingerprint,omitempty"`
}

// CheckinFingerprint 账号级「真实设备指纹」。
//
// 用途一：国际版（qoder.com）每日签到需要真实 cosy 设备身份，缺了指纹服务端
// 不下发 CLAIM_BENEFIT 活动。
//
// 用途二：启用新版推理协议。实测（2026-10-23）上游按
// (cosy-clienttype, cosy-version) 决定 model/list 下发范围——旧值
// （5 / 1.0.10）只给 12~13 个模型，换成官方桌面端同款的 10 / 1.1.69 后才会
// 额外下发 fast / smodel(Sonus) / cmodel(Cantus)。但推理侧会校验真实设备
// 指纹，自造值会被拒（RESOURCE_LIMIT）。因此配置了指纹的账号走新版协议，
// 未配置的账号保持旧协议不变。
//
// 新版指纹形态（客户端 0.4.8+）：machineId = machineToken = clientIp 为同一
// UUID，machineType 为 "5"。从官方客户端抓包获取，同一账号多次请求稳定。
type CheckinFingerprint struct {
	MachineID    string `json:"machine_id"`
	MachineToken string `json:"machine_token"`
	MachineType  string `json:"machine_type"`
	ClientIP     string `json:"client_ip,omitempty"`
	ClientType   string `json:"client_type,omitempty"` // 空则默认 10
	Version      string `json:"version,omitempty"`     // 空则默认 1.1.69
	MachineOS    string `json:"machine_os,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	Scene        string `json:"scene,omitempty"`       // 空则 app
	Product      string `json:"product,omitempty"`     // 空则 app
	DataPolicy   string `json:"data_policy,omitempty"` // 空则 agree
	MachineCode  string `json:"machine_code,omitempty"`
}

type OAuthSession struct {
	LoginID  string `json:"login_id"`
	LoginURL string `json:"login_url"`
}

type Status struct {
	// Bridges 每个区域一项：双桥常驻，CN 与 Global 各自独立运行/停止。
	Bridges []BridgeStatus `json:"bridges"`

	// 以下字段保留给仍读上游单桥状态的旧客户端/脚本；本 fork 中映射到 CN 侧。
	Running       bool   `json:"running"`
	Port          int    `json:"port"`
	ActiveAccount string `json:"active_account"`
	APIMode       string `json:"api_mode"`

	// 按 region 的启用账号与运行状态（单控制台双桥）
	CNActive         string `json:"cn_active,omitempty"`
	CNActiveName     string `json:"cn_active_name,omitempty"`
	GlobalActive     string `json:"global_active,omitempty"`
	GlobalActiveName string `json:"global_active_name,omitempty"`
	GlobalRunning    bool   `json:"global_running,omitempty"`
}

// BridgeStatus 是单个区域桥的对外状态（端口、池、激活账号、未起原因）。
type BridgeStatus struct {
	Region        Region `json:"region"`
	Label         string `json:"label"`
	Port          int    `json:"port"`
	Running       bool   `json:"running"`
	PoolSize      int    `json:"pool_size"`
	AccountsTotal int    `json:"accounts_total"` // 该区具备可用 secret 的账号数
	ActiveAccount string `json:"active_account"`
	Reason        string `json:"reason,omitempty"` // 未运行时的可读原因
}

type Settings struct {
	CNPort               int                          `json:"cn_port"`     // CN 桥端口（默认 8963），0=不启动该侧
	GlobalPort           int                          `json:"global_port"` // Global 桥端口（默认 8964），0=不启动该侧
	CationToken          string                       `json:"cn_token,omitempty"`
	GlobalToken          string                       `json:"global_token,omitempty"`           // 两区各一把 key：空则回落默认 "qccg"
	LogLevel             string                       `json:"log_level"`                        // "info" | "debug" | "error"
	QuotaRefreshInterval int                          `json:"quota_refresh_interval"`           // 秒，0=不自动刷新
	ConsolePassword      string                       `json:"console_password,omitempty"`       // Web 控制台登录密码（类似 CPA management key）
	ModelMapping         map[string]string            `json:"model_mapping,omitempty"`          // [DEPRECATED] 旧扁平映射（向后兼容，MapModel 中作为兜底回退使用）
	ModelMappings        map[string]map[string]string `json:"model_mappings,omitempty"`         // agent (claude/codex/gemini) → 客户端模型名 → Qoder model.key
	AutoCheckin          bool                         `json:"auto_checkin"`                     // 自动签到开关（默认关闭）
	AutoCheckinTimes     []string                     `json:"auto_checkin_times,omitempty"`     // 自动签到时间点，HH:MM 列表，如 ["09:15","17:00"]；空=10:00
	AutoRefreshToken     bool                         `json:"auto_refresh_token"`               // 自动续期 token 开关（默认关闭）；开启后每 15 天用 refresh_token 换新 token
	ContextTierDefault   string                       `json:"context_tier_default,omitempty"`   // 全局默认 context 档位名（如 "400K"）；空=用上游默认档
	ContextTierOverrides map[string]string            `json:"context_tier_overrides,omitempty"` // 按模型 key 覆盖档位：model_key -> 档位名
	MachineSalt          string                       `json:"machine_salt,omitempty"`           // 本机设备指纹盐（首次启动自动生成；勿修改，变更即指纹漂移）
	QueueMaxRetries      int                          `json:"queue_max_retries,omitempty"`      // 模型排队（上游 10605 isQueued）同账号最多重试次数；≤0 用默认 2，范围 1~10
	QueueWaitCapSec      int                          `json:"queue_wait_cap_sec,omitempty"`     // 单次排队等待上限（秒）；≤0 用默认 30，范围 1~300
	QueueWaitDefaultSec  int                          `json:"queue_wait_default_sec,omitempty"` // 上游未给 retryAfter 时的默认等待（秒）；≤0 用默认 15，范围 1~300

	// [DEPRECATED] 上游单桥时代的字段，仅用于读取旧 settings.json 并迁移，
	// 保存时会被清空；新代码一律使用 CNPort/GlobalPort 与 CationToken/GlobalToken。
	Port        int    `json:"port,omitempty"`
	BridgeToken string `json:"bridge_token,omitempty"`
	// AutoStart 是上游旧控制台的开机自启开关；本 fork 双桥按账号自动常驻，
	// 该字段仅保留读写兼容，不影响运行行为。
	AutoStart bool `json:"auto_start,omitempty"`
}

type QuotaInfo struct {
	Plan              string             `json:"plan"`
	UserQuota         *QuotaBucket       `json:"user_quota,omitempty"`
	AddonQuota        *QuotaBucket       `json:"addon_quota,omitempty"`
	DedicatedPackages []DedicatedPackage `json:"dedicated_packages,omitempty"`
	IsQuotaExceeded   bool               `json:"is_quota_exceeded"`
	ExpiresAt         int64              `json:"expires_at,omitempty"`
}

// DedicatedPackage 上游「专属资源包」额度（dedicatedResourcePackages）。
// 活动奖励（如 SOTA 专属积分）单独成桶，不与 user/addon 混合，
// 通常带独立过期时间与使用范围限制。
type DedicatedPackage struct {
	ID          string              `json:"id,omitempty"`
	Name        string              `json:"name,omitempty"`
	Description string              `json:"description,omitempty"`
	Used        float64             `json:"used"`
	Total       float64             `json:"total"`
	Remaining   float64             `json:"remaining"`
	Unit        string              `json:"unit,omitempty"`
	ExpiresAtMs int64               `json:"expires_at_ms,omitempty"`
	Available   bool                `json:"available,omitempty"`
	Status      string              `json:"status,omitempty"`
	Labels      []DedicatedPkgLabel `json:"labels,omitempty"`
}

// DedicatedPkgLabel 资源包的展示标签（上游 displayLabels，含多语言）。
type DedicatedPkgLabel struct {
	Dimension string            `json:"dimension,omitempty"`
	Value     string            `json:"value,omitempty"`
	ValueI18n map[string]string `json:"value_i18n,omitempty"`
}

type QuotaBucket struct {
	Used      float64 `json:"used"`
	Total     float64 `json:"total"`
	Remaining float64 `json:"remaining"`
	ResetTime string  `json:"reset_time,omitempty"`
}
