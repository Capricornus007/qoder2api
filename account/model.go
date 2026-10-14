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
}

type OAuthSession struct {
	LoginID  string `json:"login_id"`
	LoginURL string `json:"login_url"`
}

type Status struct {
	// Bridges 每个区域一项：双桥常驻，CN 与 Global 各自独立运行/停止。
	Bridges []BridgeStatus `json:"bridges"`
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
	GlobalToken          string                       `json:"global_token,omitempty"`       // 两区各一把 key：空则回落默认 "qccg"
	LogLevel             string                       `json:"log_level"`                    // "info" | "debug" | "error"
	QuotaRefreshInterval int                          `json:"quota_refresh_interval"`       // 秒，0=不自动刷新
	ConsolePassword      string                       `json:"console_password,omitempty"`   // Web 控制台登录密码（类似 CPA management key）
	ModelMapping         map[string]string            `json:"model_mapping,omitempty"`      // [DEPRECATED] 旧扁平映射（向后兼容，MapModel 中作为兜底回退使用）
	ModelMappings        map[string]map[string]string `json:"model_mappings,omitempty"`     // agent (claude/codex/gemini) → 客户端模型名 → Qoder model.key
	AutoCheckin          bool                         `json:"auto_checkin"`                 // 自动签到开关（默认关闭）
	AutoCheckinTimes     []string                     `json:"auto_checkin_times,omitempty"` // 自动签到时间点，HH:MM 列表，如 ["09:15","17:00"]；空=10:00
	MachineSalt          string                       `json:"machine_salt,omitempty"`       // 本机设备指纹盐（首次启动自动生成；勿修改，变更即指纹漂移）
}

type QuotaInfo struct {
	Plan            string       `json:"plan"`
	UserQuota       *QuotaBucket `json:"user_quota,omitempty"`
	AddonQuota      *QuotaBucket `json:"addon_quota,omitempty"`
	IsQuotaExceeded bool         `json:"is_quota_exceeded"`
	ExpiresAt       int64        `json:"expires_at,omitempty"`
}

type QuotaBucket struct {
	Used      float64 `json:"used"`
	Total     float64 `json:"total"`
	Remaining float64 `json:"remaining"`
	ResetTime string  `json:"reset_time,omitempty"`
}
