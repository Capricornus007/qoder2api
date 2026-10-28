package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"qoder2api/account"
	"qoder2api/internal/bridge"
	"qoder2api/internal/cosy"
	"qoder2api/logger"
)

type QoderModel struct {
	Key             string  `json:"id"`
	Name            string  `json:"name"` // 与 display_name 同值：不少客户端只认 name
	DisplayName     string  `json:"display_name"`
	Enable          bool    `json:"enable"`
	IsDefault       bool    `json:"is_default"`
	IsReasoning     bool    `json:"is_reasoning,omitempty"`
	ContextWindow   int     `json:"context_window,omitempty"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	MaxInputTokens  int     `json:"max_input_tokens,omitempty"`
	PriceFactor     float64 `json:"price_factor,omitempty"`
	// ContextTiers 上游 context_config 的全部档位（如 200K/400K/1M）。
	ContextTiers []bridge.ContextTier `json:"context_tiers,omitempty"`
}

// regionBridge 把「端口 + 账号池 + 区域」三者绑成一个常驻单元。
// 国内版与国际版各占一个实例，互不覆盖：任何一侧启停、换号、扣完额度
// 都不会打断另一侧正在跑的请求。
type regionBridge struct {
	region account.Region
	port   int
	server *http.Server
	pool   *bridge.Pool
	br     *bridge.Bridge
	// reason 说明这一侧为什么没在监听（例如该区还没有账号）。
	reason string
}

type Service struct {
	mu          sync.Mutex
	bridges     map[account.Region]*regionBridge
	cnPort      int
	globalPort  int
	cnToken     string
	globalToken string
	// bridgeBind 是两条桥的监听地址。默认 0.0.0.0（沿用上游行为），
	// 笔记本/家用机上建议 127.0.0.1：桥没有内网隔离就会把账号额度暴露在同一 LAN。
	bridgeBind string
	basePrompt []byte
}

func NewService(basePrompt []byte) *Service {
	return &Service{
		bridges:    map[account.Region]*regionBridge{},
		cnPort:     account.DefaultCNPort,
		globalPort: account.DefaultGlobalPort,
		bridgeBind: "0.0.0.0",
		basePrompt: basePrompt,
	}
}

func (s *Service) LoadRuntimeSettings() {
	settings, err := account.LoadSettings()
	if err != nil {
		logger.Error("load settings: %v", err)
		return
	}
	logger.SetLevel(settings.LogLevel)
	s.cnToken = settings.CationToken
	s.globalToken = settings.GlobalToken
	s.cnPort = settings.CNPort
	s.globalPort = settings.GlobalPort
	// 排队重试参数（控制台可配）：启动时应用，保存设置时再热更新
	bridge.ApplyQueueSettings(settings.QueueMaxRetries, settings.QueueWaitCapSec, settings.QueueWaitDefaultSec, settings.EffectiveQueueWaitFirstToken())
}

// ApplyQueueSettings 热更新模型排队重试参数（控制台保存设置后调用，即时生效）。
func (s *Service) ApplyQueueSettings(maxRetries, waitCapSec, waitDefaultSec int, waitFirstToken bool) {
	bridge.ApplyQueueSettings(maxRetries, waitCapSec, waitDefaultSec, waitFirstToken)
}

// RecentWaitLog 返回最近排队等待记录（最新在前），供控制台「最近等待记录」面板使用。
func (s *Service) RecentWaitLog() []bridge.WaitEpisode {
	return bridge.RecentWaitLog()
}

// SetPorts 设置两侧的对外端口（命令行覆盖 settings）。
func (s *Service) SetPorts(cnPort, globalPort int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cnPort = cnPort
	s.globalPort = globalPort
}

// SetBridgeBind 设置两条桥的绑定地址（只影响之后启动的桥）。
func (s *Service) SetBridgeBind(bind string) {
	if strings.TrimSpace(bind) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bridgeBind = strings.TrimSpace(bind)
}

func (s *Service) bridgeBindAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.bridgeBind) == "" {
		return "0.0.0.0"
	}
	return s.bridgeBind
}

// Ports 返回两侧当前生效的端口（0 表示该侧不监听）。
func (s *Service) Ports() (cnPort, globalPort int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cnPort, s.globalPort
}

func (s *Service) portForLocked(r account.Region) int {
	if r == account.RegionCN {
		return s.cnPort
	}
	return s.globalPort
}

// EffectiveToken 返回该区域桥的鉴权 token；留空回落到默认 "qccg"。
// 两区各一把：额度耗尽、要撤权、给别人只放某一区时，能单独轮换那把 key，
// 不至于因为共用一把而把另一区一起交出去。
func (s *Service) EffectiveToken(region account.Region) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok := s.cnToken
	if region.Normalized() == account.RegionGlobal {
		tok = s.globalToken
	}
	if strings.TrimSpace(tok) == "" {
		return "qccg"
	}
	return tok
}

func (s *Service) ListAccounts() []account.Account {
	accounts, _ := account.List()
	if accounts == nil {
		return []account.Account{}
	}
	return accounts
}

func (s *Service) AddAccountByPAT(pat, region string) (*account.Account, error) {
	r := account.NormalizeRegion(region)
	ep := account.GetEndpoints(r)
	// 交换时尚无 uid：机器头按凭证稳定派生，避免每次导入生成新机器码
	seed := cosy.FingerprintSeed("", pat)
	jt, err := cosy.ExchangeJobToken(pat, cosy.DeriveMachineID(seed), cosy.DeriveMachineToken(seed), cosy.DeriveMachineType(seed), ep.JobTokenURL)
	if err != nil {
		return nil, fmt.Errorf("验证 PAT 失败: %w", err)
	}
	id := account.SanitizeID(bridge.StrVal(jt, "id") + bridge.StrVal(jt, "name"))
	now := time.Now()
	acct := &account.Account{
		ID:        id,
		Name:      bridge.StrVal(jt, "name"),
		Email:     bridge.StrVal(jt, "email"),
		UserType:  bridge.StrValDefault(jt, "userType", "personal_standard"),
		Region:    r,
		AuthMode:  "pat",
		APIMode:   "openai",
		Tags:      []string{},
		CreatedAt: now,
	}
	if err := account.SaveSecret(acct.ID, pat); err != nil {
		return nil, err
	}
	if err := account.Save(acct); err != nil {
		return nil, err
	}
	return acct, nil
}

// AfterAccountAdded 新账号落盘后调用：把它接进所属区域的桥。
// 该侧原本没有账号时端口会自动起来；已在监听时只增量补号，不打断在途请求。
func (s *Service) AfterAccountAdded(id string) error {
	acct, err := account.Get(id)
	if err != nil {
		return err
	}
	return s.ensureRegionBridge(acct.Region.Normalized())
}

func (s *Service) DeleteAccount(id string) error {
	acct, err := account.Get(id)
	region := account.RegionGlobal
	if err == nil {
		region = acct.Region.Normalized()
	}
	_ = account.DeleteSecret(id)
	if err := account.Delete(id); err != nil {
		return err
	}
	// 只重算被删账号所在的那一侧
	if syncErr := s.ensureRegionBridge(region); syncErr != nil {
		logger.Info("after delete %s: region %s bridge: %v", id, region, syncErr)
	}
	return nil
}

// SetActiveAccount 只影响该账号所在区域的默认账号（另一区的激活状态不动）。
// 池内已有该账号时不需要重启监听，因此切号不会掐断正在跑的流。
func (s *Service) SetActiveAccount(id string) error {
	acct, err := account.Get(id)
	if err != nil {
		return err
	}
	region := acct.Region.Normalized()
	if err := account.SetActiveForRegion(region, id); err != nil {
		return err
	}
	return s.ensureRegionBridge(region)
}

func (s *Service) GetStatus() account.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := account.Status{}
	for _, r := range account.Regions {
		bs := account.BridgeStatus{
			Region: r,
			Label:  r.Label(),
			Port:   s.portForLocked(r),
		}
		if rb := s.bridges[r]; rb != nil {
			bs.Running = rb.server != nil
			bs.Reason = rb.reason
			if rb.pool != nil {
				bs.PoolSize = rb.pool.Size()
			}
		}
		if !bs.Running && bs.Reason == "" {
			bs.Reason = "尚未启动"
		}
		if acct, _ := account.GetActiveForRegion(r); acct != nil {
			bs.ActiveAccount = acct.Name
		}
		if accts, err := account.ListByRegion(r); err == nil {
			usable := 0
			for i := range accts {
				if account.HasSecret(accts[i].ID) {
					usable++
				}
			}
			bs.AccountsTotal = usable
		}
		out.Bridges = append(out.Bridges, bs)
	}
	// 兼容上游单桥状态字段：旧控制台/脚本仍读 running/port/active_account。
	// 本 fork 里 8963 固定是 CN、8964 固定是 Global；单桥字段优先 CN，
	// CN 未启用时回落到 Global，保证「只跑国际版」的旧客户端也读得到。
	out.Port = s.cnPort
	if out.Port == 0 {
		out.Port = s.globalPort
	}
	out.Running = false
	for _, bs := range out.Bridges {
		if bs.Running {
			out.Running = true
		}
		if bs.Region == account.RegionGlobal {
			out.GlobalRunning = bs.Running
		}
	}
	if cn, _ := account.GetActiveForRegion(account.RegionCN); cn != nil {
		out.ActiveAccount = cn.ID
		out.APIMode = cn.APIMode
		out.CNActive = cn.ID
		out.CNActiveName = cn.Name
	}
	if gl, _ := account.GetActiveForRegion(account.RegionGlobal); gl != nil {
		out.GlobalActive = gl.ID
		out.GlobalActiveName = gl.Name
		if out.ActiveAccount == "" {
			out.ActiveAccount = gl.ID
			out.APIMode = gl.APIMode
		}
	}
	return out
}

// StartAllBridges 启动阶段调用：两区各自尽力拉起。
// 一侧缺账号绝不阻塞另一侧——这正是双版本「同时可用」的底线。
func (s *Service) StartAllBridges() error {
	var firstErr error
	for _, r := range account.Regions {
		if err := s.ensureRegionBridge(r); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ensureRegionBridge 让该区域进入「有可用账号就在监听」的状态：
//   - 已在监听 → 增量对齐账号池（补新号、踢已删号），不重启端口；
//   - 未在监听 → 池里有账号就开端口，否则把原因写进状态供控制台显示。
func (s *Service) ensureRegionBridge(region account.Region) error {
	s.mu.Lock()
	port := s.portForLocked(region)
	rb := s.bridges[region]
	if rb == nil {
		rb = &regionBridge{region: region, port: port}
		s.bridges[region] = rb
	}
	listening := rb.server != nil
	pool := rb.pool
	s.mu.Unlock()

	if port <= 0 {
		s.markIdle(region, 0, "该侧端口设为 0（已停用）：在控制台改端口或用 --cn-port / --global-port 指定")
		return fmt.Errorf("%s bridge disabled by port config", region)
	}
	if other := s.conflictingRegion(region, port); other != "" && !listening {
		s.markIdle(region, port, fmt.Sprintf("端口 %d 已被 %s 侧占用，请改一侧端口", port, other))
		return fmt.Errorf("%s bridge port %d conflicts with %s", region, port, other)
	}

	if listening && pool != nil {
		changed, err := s.syncPool(region, pool)
		if err != nil {
			return err
		}
		if changed == 0 && pool.Size() > 0 {
			return nil
		}
		if pool.Size() == 0 {
			s.stopRegion(region)
			msg := fmt.Sprintf("region %q 没有可用账号：请在控制台以 region=%s 完成 OAuth/PAT 登录", region, region)
			s.markIdle(region, port, msg)
			return fmt.Errorf("no %s account with usable secret", region)
		}
		logger.Info("region %s pool synced: %d account(s) on port %d", region, pool.Size(), port)
		return nil
	}

	built := s.buildRegionPool(region)
	if built.Size() == 0 {
		msg := fmt.Sprintf("region %q 还没有可用账号：请在控制台以 region=%s 完成 OAuth/PAT 登录", region, region)
		s.markIdle(region, port, msg)
		return fmt.Errorf("no %s account with usable secret", region)
	}
	return s.listen(region, port, built)
}

func (s *Service) conflictingRegion(region account.Region, port int) account.Region {
	for _, r := range account.Regions {
		if r == region {
			continue
		}
		s.mu.Lock()
		rb := s.bridges[r]
		other := s.portForLocked(r)
		s.mu.Unlock()
		if other > 0 && other == port && rb != nil && rb.server != nil {
			return r
		}
	}
	return ""
}

// listen 在指定端口开启该区域的桥。二次检查 server 避免并发重复监听。
func (s *Service) listen(region account.Region, port int, pool *bridge.Pool) error {
	tmpl := s.renderPromptTemplate()
	b := bridge.NewPoolBridgeFor(region, pool, tmpl)
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.bridgeBindAddr(), port),
		Handler: s.authMiddleware(region, s.bridgeMux(b)),
	}

	s.mu.Lock()
	rb := s.bridges[region]
	if rb == nil {
		rb = &regionBridge{region: region}
		s.bridges[region] = rb
	}
	if rb.server != nil {
		s.mu.Unlock()
		return nil
	}
	rb.server = srv
	rb.pool = pool
	rb.br = b
	rb.port = port
	rb.reason = ""
	s.mu.Unlock()

	logger.Info("Bridge started on port %d region=%s pool=%d", port, region, pool.Size())
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("bridge serve error (region=%s port=%d): %v", region, port, err)
			s.markIdle(region, port, fmt.Sprintf("端口 %d 监听失败: %v", port, err))
		}
	}()
	return nil
}

func (s *Service) bridgeMux(b *bridge.Bridge) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", b.HandleChatCompletions)
	mux.HandleFunc("/v1/messages", b.HandleClaudeMessages)
	mux.HandleFunc("/v1/models", b.HandleListModels)
	mux.HandleFunc("/v1/responses", b.HandleCodexResponses)
	return mux
}

// markIdle 记录该侧未在监听的原因（端口保留展示，方便控制台指路）。
func (s *Service) markIdle(region account.Region, port int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rb := s.bridges[region]
	if rb == nil {
		rb = &regionBridge{region: region}
		s.bridges[region] = rb
	}
	rb.server = nil
	rb.br = nil
	rb.pool = nil
	if port > 0 {
		rb.port = port
	}
	rb.reason = reason
}

// stopRegion 关闭该侧监听；另一侧完全不受影响。
func (s *Service) stopRegion(region account.Region) {
	s.mu.Lock()
	rb := s.bridges[region]
	var srv *http.Server
	if rb != nil {
		srv = rb.server
		rb.server = nil
		rb.br = nil
		rb.pool = nil
	}
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
		logger.Info("Bridge stopped on region=%s", region)
	}
}

// StopBridge 停止全部区域（控制台「停止」按钮）。
func (s *Service) StopBridge() error {
	for _, r := range account.Regions {
		s.stopRegion(r)
		s.markIdle(r, s.portOf(r), "已手动停止；用「启动」或新增该区域账号即可恢复")
	}
	return nil
}

// StopRegionBridge 只停一侧，另一侧继续服务。
func (s *Service) StopRegionBridge(region account.Region) error {
	s.stopRegion(region)
	s.markIdle(region, s.portOf(region), "已手动停止；用「启动」或新增该区域账号即可恢复")
	return nil
}

// StartBridge 启动全部区域；指定 region 时只启动那一侧。
func (s *Service) StartBridge(region account.Region) error {
	if region != "" {
		return s.ensureRegionBridge(region.Normalized())
	}
	return s.StartAllBridges()
}

func (s *Service) portOf(region account.Region) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.portForLocked(region)
}

// syncPool 把池成员对齐到「该区域当前有 secret 的账号」，返回变动数量。
// 新增账号是热加入（下一次选号即可能被选中），删除账号热退出，
// 全程不重开端口，因此不会掐断该侧或其他侧的在途请求。
func (s *Service) syncPool(region account.Region, pool *bridge.Pool) (int, error) {
	accounts, err := account.ListByRegion(region)
	if err != nil {
		return 0, fmt.Errorf("list %s accounts: %w", region, err)
	}
	desired := map[string]string{} // id -> secret
	names := map[string]string{}
	fps := map[string]*account.CheckinFingerprint{}
	for i := range accounts {
		a := accounts[i]
		if !account.HasSecret(a.ID) {
			continue
		}
		sec, err := account.GetSecret(a.ID)
		if err != nil {
			logger.Error("syncPool(%s): get secret for %s: %v", region, a.Name, err)
			continue
		}
		desired[a.ID] = sec
		names[a.ID] = a.Name
		fps[a.ID] = a.CheckinFingerprint
	}

	changed := 0
	for _, id := range pool.IDs() {
		if _, keep := desired[id]; !keep {
			pool.Remove(id)
			changed++
			logger.Info("syncPool(%s): removed account %s", region, id)
		}
	}
	for id, sec := range desired {
		if pool.Has(id) {
			continue
		}
		slot, err := bridge.NewSlotFromSecretWithFP(id, names[id], region, sec, fps[id])
		if err != nil {
			logger.Error("syncPool(%s): account %s unusable, skipped: %v", region, id, err)
			continue
		}
		pool.Add(slot)
		changed++
		logger.Info("syncPool(%s): added account %s", region, id)
	}
	return changed, nil
}

// renderPromptTemplate 展开 baseprompt.json 里的 UUID/时间占位符。
func (s *Service) renderPromptTemplate() map[string]interface{} {
	tmpl := string(s.basePrompt)
	for _, ukey := range []string{"{UUID1}", "{UUID2}", "{UUID3}", "{UUID4}", "{UUID5}"} {
		tmpl = strings.ReplaceAll(tmpl, ukey, cosy.NewUUID())
	}
	tmpl = strings.ReplaceAll(tmpl, "{TIME1}", fmt.Sprintf("%d", cosy.UnixMs()))
	var templateBase map[string]interface{}
	_ = json.Unmarshal([]byte(tmpl), &templateBase)
	return templateBase
}

// buildRegionPool 只把指定区域的账号建成池（另一区账号永不入池，
// 跨区派号会扣到用户根本没在用那一侧的账号的 credits）。
// 状态文件按区域隔离，两侧冷却记录互不覆盖。
func (s *Service) buildRegionPool(region account.Region) *bridge.Pool {
	pool := bridge.NewPoolWithState(filepath.Join(account.DataRoot(), "pool_state_"+string(region)+".json"))
	accounts, err := account.ListByRegion(region)
	if err != nil {
		logger.Error("buildRegionPool(%s): list accounts: %v", region, err)
		return pool
	}
	for i := range accounts {
		a := accounts[i]
		if !account.HasSecret(a.ID) {
			continue
		}
		pat, err := account.GetSecret(a.ID)
		if err != nil {
			logger.Error("buildRegionPool(%s): get secret for %s: %v", region, a.Name, err)
			continue
		}
		slot, err := bridge.NewSlotFromSecretWithFP(a.ID, a.Name, a.Region, pat, a.CheckinFingerprint)
		if err != nil {
			logger.Error("buildRegionPool(%s): account %s unusable, skipped: %v", region, a.Name, err)
			continue
		}
		pool.Add(slot)
		logger.Info("buildRegionPool(%s): added account %s", region, a.Name)
	}
	pool.LoadState()
	return pool
}

// authMiddleware Bridge 端点（/v1/*）鉴权中间件：
// 接受 Authorization: Bearer <token> 或 x-api-key: <token>；
// token 每次请求实时取 svc.EffectiveToken()，容忍运行中改配置；
// 比较用 crypto/subtle.ConstantTimeCompare 防时序侧信道；
// OPTIONS 预检直接放行（204），避免浏览器客户端 CORS 失败；
// 校验失败返回 401 + OpenAI 风格错误体。
// region 写进日志，让「哪一侧在收请求」一眼可查。
func (s *Service) authMiddleware(region account.Region, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("[HTTP][%s] %s %s from %s", region, r.Method, r.URL.Path, r.RemoteAddr)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		token := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		} else if k := r.Header.Get("x-api-key"); k != "" {
			token = k
		}
		expect := s.EffectiveToken(region)
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(expect)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"invalid api key","type":"authentication_error"}}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PoolStatusByRegion 返回两侧账号池的运行时状态（未启动的一侧 slots 为空）。
func (s *Service) PoolStatusByRegion() []map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]interface{}, 0, len(account.Regions))
	for _, r := range account.Regions {
		entry := map[string]interface{}{
			"region":  r,
			"label":   r.Label(),
			"port":    s.portForLocked(r),
			"running": false,
			"slots":   []bridge.SlotStatus{},
		}
		if rb := s.bridges[r]; rb != nil {
			entry["running"] = rb.server != nil
			entry["reason"] = rb.reason
			if rb.pool != nil {
				entry["slots"] = rb.pool.Status()
			}
		}
		out = append(out, entry)
	}
	return out
}

// CoolAccount 手动冷却指定账号（运维/调试用，便于观察选号与换号）。
// 账号 ID 在两池间查找，命中哪个池就冷却哪个池。
func (s *Service) CoolAccount(id string, seconds int, reason string) error {
	s.mu.Lock()
	pools := make([]*bridge.Pool, 0, len(account.Regions))
	for _, r := range account.Regions {
		if rb := s.bridges[r]; rb != nil && rb.pool != nil {
			pools = append(pools, rb.pool)
		}
	}
	s.mu.Unlock()

	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("id required")
	}
	if seconds <= 0 {
		seconds = 60
	}
	if strings.TrimSpace(reason) == "" {
		reason = "manual cooldown"
	}
	for _, p := range pools {
		if p.Has(id) {
			p.Cool(id, time.Duration(seconds)*time.Second, reason)
			return nil
		}
	}
	return fmt.Errorf("account %s not in any running pool", id)
}

func (s *Service) GetSettings() (*account.Settings, error) {
	return account.LoadSettings()
}

// SaveSettings 落盘配置并让端口/token/日志级别即时生效：
// 端口变化只重启受影响的那一侧，另一侧继续服务。
func (s *Service) SaveSettings(st *account.Settings) error {
	logger.SetLevel(st.LogLevel)
	if st.QuotaRefreshInterval > 0 && st.QuotaRefreshInterval < 10 {
		st.QuotaRefreshInterval = 10
	}
	if err := account.SaveSettings(st); err != nil {
		return err
	}

	s.mu.Lock()
	oldCN, oldGlobal := s.cnPort, s.globalPort
	s.cnToken, s.globalToken = st.CationToken, st.GlobalToken
	s.cnPort, s.globalPort = st.CNPort, st.GlobalPort
	changed := []account.Region{}
	if oldCN != st.CNPort {
		changed = append(changed, account.RegionCN)
	}
	if oldGlobal != st.GlobalPort {
		changed = append(changed, account.RegionGlobal)
	}
	s.mu.Unlock()

	for _, r := range changed {
		s.stopRegion(r)
		if err := s.ensureRegionBridge(r); err != nil {
			logger.Info("region %s restart after port change: %v", r, err)
		}
	}
	return nil
}

func (s *Service) GetLogs(limit int) []logger.Entry {
	return logger.GetLogs(limit)
}

func (s *Service) GetAccountQuota(accountID string) (*account.QuotaInfo, error) {
	acct, err := account.Get(accountID)
	if err != nil {
		return nil, err
	}
	token, err := account.GetSecret(acct.ID)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}
	deviceToken, _ := bridge.ParseOAuthSecret(token)
	if strings.HasPrefix(deviceToken, "dt-") {
		token = deviceToken
	} else {
		ep := account.GetEndpoints(acct.Region)
		seed := cosy.FingerprintSeed("", token)
		jt, err := cosy.ExchangeJobToken(token, cosy.DeriveMachineID(seed), cosy.DeriveMachineToken(seed), cosy.DeriveMachineType(seed), ep.JobTokenURL)
		if err != nil {
			return nil, fmt.Errorf("exchange token: %w", err)
		}
		oauthToken := bridge.StrVal(jt, "securityOauthToken")
		if oauthToken == "" {
			return nil, fmt.Errorf("no securityOauthToken in response")
		}
		token = oauthToken
	}
	quota, err := account.FetchQuota(token, acct.Region)
	if err != nil {
		return nil, err
	}
	logger.Debug("quota result for %s: user_quota=%+v addon_quota=%+v", accountID, quota.UserQuota, quota.AddonQuota)
	return quota, nil
}

// ListQoderModels 返回指定区域桥的模型目录；region 为空时取第一个已启动的桥。
func (s *Service) ListQoderModels(region account.Region) ([]QoderModel, error) {
	s.mu.Lock()
	var b *bridge.Bridge
	reason := ""
	if region != "" {
		if rb := s.bridges[region]; rb != nil {
			b, reason = rb.br, rb.reason
		}
	} else {
		for _, r := range account.Regions {
			if rb := s.bridges[r]; rb != nil && rb.br != nil {
				b, region = rb.br, r
				break
			}
		}
	}
	s.mu.Unlock()

	if b == nil {
		if reason == "" {
			reason = "该侧桥未启动，请先激活账号并启动 bridge"
		}
		return nil, fmt.Errorf("BRIDGE_NOT_RUNNING (%s): %s", region, reason)
	}
	models, err := b.ListAvailableModels()
	if err != nil {
		return nil, err
	}
	out := make([]QoderModel, len(models))
	for i, m := range models {
		out[i] = QoderModel{
			Key:             m.Key,
			Name:            m.DisplayName,
			DisplayName:     m.DisplayName,
			Enable:          m.Enable,
			IsDefault:       m.IsDefault,
			IsReasoning:     m.IsReasoning,
			MaxInputTokens:  m.MaxInputTokens,
			PriceFactor:     m.PriceFactor,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			ContextTiers:    m.ContextTiers,
		}
	}
	return out, nil
}

func (s *Service) StartOAuthLogin(region string) (*account.OAuthSession, error) {
	return account.StartLogin(account.NormalizeRegion(region))
}

func (s *Service) CancelOAuthLogin(loginID string) {
	account.CancelLogin(loginID)
}

func (s *Service) CleanupAllData() error {
	_ = s.StopBridge()
	accounts, err := account.List()
	if err == nil {
		for _, acct := range accounts {
			_ = account.DeleteSecret(acct.ID)
			_ = account.Delete(acct.ID)
		}
	}
	return nil
}
