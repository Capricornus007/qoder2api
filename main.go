package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"qoder2api/account"
	"qoder2api/internal/cosy"
	"qoder2api/logger"
)

//go:embed all:webconsole
var webAssets embed.FS

//go:embed baseprompt.json
var basePromptRaw []byte

var svc *Service

func main() {
	webPort := flag.Int("web-port", account.DefaultWebPort, "web console port")
	cnPort := flag.Int("cn-port", account.DefaultCNPort, "国内版（CN）桥端口，强制只用 region=cn 账号；0=不启动该侧")
	globalPort := flag.Int("global-port", account.DefaultGlobalPort, "国际版（Global）桥端口，强制只用 region=global 账号；0=不启动该侧")
	bind := flag.String("bind", "0.0.0.0", "console bind address")
	bridgeBind := flag.String("bridge-bind", "0.0.0.0", "两条桥的监听地址；本机自用建议 127.0.0.1，否则同内网任何人都能用你的额度")
	dataDir := flag.String("data-dir", "", "data root (default: $QODER2API_HOME or ~/.qoder2api)")
	flag.Parse()

	if *dataDir != "" {
		account.SetDataRoot(*dataDir)
	}
	_ = os.MkdirAll(account.DataRoot(), 0700)
	if err := logger.InitFile(account.LogsDir()); err != nil {
		fmt.Fprintf(os.Stderr, "[logger] init failed: %v\n", err)
	}
	logger.Info("data root: %s", account.DataRoot())

	svc = NewService(basePromptRaw)
	svc.LoadRuntimeSettings()

	// 命令行端口优先于 settings.json（未显式传参时沿用 settings / 默认双桥端口）
	if isFlagPassed("cn-port") || isFlagPassed("global-port") {
		svc.SetPorts(*cnPort, *globalPort)
	} else {
		*cnPort, *globalPort = svc.Ports()
	}
	svc.SetBridgeBind(*bridgeBind)

	// 设备指纹本机盐：首次启动自动生成并落盘（settings.json machine_salt）。
	// 使每个部署的指纹派生空间独立，防止 uid 派生模式被上游全局识别；
	// 生成后保持不变——变更 salt 即所有账号指纹整体漂移（等价于换设备）。
	if salt, err := account.EnsureMachineSalt(); err == nil {
		cosy.SetInstallSalt(salt)
		logger.Info("machine salt loaded (len=%d), fingerprints salted", len(salt))
	} else {
		// 仅提升可观测性、不阻断启动：无盐模式可继续排障，但后果必须明示
		logger.Error("init machine salt: %v；运行于无盐模式（hub 兼容指纹），下次启动恢复加盐将导致全部账号指纹漂移", err)
	}

	// cookie 按控制台端口隔离，避免同机多实例登录互踢
	consoleWebPort = *webPort

	// 控制台密码：环境变量 > settings；都没有则自动生成并落盘
	_ = ensureConsolePassword()

	// 每日自动签到调度（需在控制台手动开启，默认关闭）
	StartCheckinScheduler()

	// 启动时两侧各自尽力拉起：一侧没账号绝不阻塞另一侧
	if err := svc.StartAllBridges(); err != nil {
		logger.Info("至少一侧桥未启动: %v", err)
		logger.Info("open console and login/activate an account: http://127.0.0.1:%d", *webPort)
	}

	sub, err := fs.Sub(webAssets, "webconsole")
	if err != nil {
		log.Fatalf("web assets: %v", err)
	}

	mux := http.NewServeMux()
	// auth (public)
	mux.HandleFunc("/api/auth/login", handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", handleAuthLogout)
	mux.HandleFunc("/api/auth/me", handleAuthMe)

	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/accounts", handleAccounts)
	mux.HandleFunc("/api/accounts/add", handleAddAccount)
	mux.HandleFunc("/api/accounts/delete", handleDeleteAccount)
	mux.HandleFunc("/api/accounts/quota", handleAccountQuota)
	mux.HandleFunc("/api/oauth/start", handleOAuthStart)
	mux.HandleFunc("/api/oauth/wait", handleOAuthWait)
	mux.HandleFunc("/api/oauth/cancel", handleOAuthCancel)
	mux.HandleFunc("/api/active", handleSetActiveAccount)
	mux.HandleFunc("/api/pool", handlePoolStatus)
	mux.HandleFunc("/api/pool/cool", handlePoolCool)
	mux.HandleFunc("/api/settings", handleGetSettings)
	mux.HandleFunc("/api/settings/save", handleSaveSettings)
	mux.HandleFunc("/api/models", handleListModels)
	mux.HandleFunc("/api/logs", handleLogs)
	mux.HandleFunc("/api/cleanup", handleCleanup)
	mux.HandleFunc("/api/bridge/start", handleStartBridge)
	mux.HandleFunc("/api/bridge/stop", handleStopBridge)
	mux.HandleFunc("/api/checkin", handleCheckin)
	mux.Handle("/", http.FileServer(http.FS(sub)))

	addr := fmt.Sprintf("%s:%d", *bind, *webPort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      withCORS(requireConsoleAuth(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Minute, // oauth wait may block
		IdleTimeout:  60 * time.Second,
	}

	logger.Info("qoder2api console: http://%s (password protected)", addr)
	logger.Info("dual bridge: CN=%d Global=%d bind=%s (API key 两区各一把，与控制台密码无关)", *cnPort, *globalPort, *bridgeBind)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, dst interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
}

// connectionInfo 给出两侧桥的对外地址，供控制台一键复制。
// 国内版走 cnPort、国际版走 globalPort，两者同时在线、互不影响。
func connectionInfo() map[string]interface{} {
	out := map[string]interface{}{}
	if svc == nil {
		return out
	}
	host := "127.0.0.1" // 控制台展示用：服务器上请改成公网/内网 IP
	regions := map[string]interface{}{}
	cnPort, globalPort := svc.Ports()
	for _, r := range account.Regions {
		port := cnPort
		if r == account.RegionGlobal {
			port = globalPort
		}
		token := svc.EffectiveToken(r)
		base := fmt.Sprintf("http://%s:%d", host, port)
		enabled := port > 0
		item := map[string]interface{}{
			"region":   string(r),
			"label":    r.Label(),
			"port":     port,
			"enabled":  enabled,
			"base_url": base,
		}
		if enabled {
			item["api_key"] = token
			item["openai_base_url"] = base + "/v1"
			item["claude_base_url"] = base
			item["newapi_hint"] = map[string]string{
				"type":     "openai",
				"base_url": base + "/v1",
				"api_key":  token,
				"model":    "auto",
			}
		}
		regions[string(r)] = item
	}
	out["regions"] = regions
	return out
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, svc.GetStatus())
}

func handleAccounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, svc.ListAccounts())
}

func handleAddAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		PAT    string `json:"pat"`
		Region string `json:"region"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.PAT = strings.TrimSpace(req.PAT)
	if req.PAT == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("pat is required"))
		return
	}
	acct, err := svc.AddAccountByPAT(req.PAT, req.Region)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := svc.SetActiveAccount(acct.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

func handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := svc.DeleteAccount(req.ID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleAccountQuota(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" && r.Method == http.MethodPost {
		var req struct {
			ID string `json:"id"`
		}
		if err := readJSON(r, &req); err == nil {
			id = strings.TrimSpace(req.ID)
		}
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("id is required"))
		return
	}
	quota, err := svc.GetAccountQuota(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, quota)
}

func handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		Region string `json:"region"`
	}
	_ = readJSON(r, &req)
	session, err := svc.StartOAuthLogin(req.Region)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func handleOAuthWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
		Active  *bool  `json:"active"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.LoginID = strings.TrimSpace(req.LoginID)
	if req.LoginID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("login_id is required"))
		return
	}
	acct, err := account.WaitLogin(req.LoginID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := account.Save(acct); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	activate := true
	if req.Active != nil {
		activate = *req.Active
	}
	if activate {
		if err := svc.SetActiveAccount(acct.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, acct)
}

func handleOAuthCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	svc.CancelOAuthLogin(strings.TrimSpace(req.LoginID))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleSetActiveAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := svc.SetActiveAccount(req.ID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handlePoolStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"pools": svc.PoolStatusByRegion()})
}

func handlePoolCool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		ID      string `json:"id"`
		Seconds int    `json:"seconds"`
		Reason  string `json:"reason"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := svc.CoolAccount(req.ID, req.Seconds, req.Reason); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := svc.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := *settings
	if out.CNPort == 0 && out.GlobalPort == 0 {
		out.CNPort, out.GlobalPort = account.DefaultCNPort, account.DefaultGlobalPort
	}
	// 不回传明文控制台密码，只标记是否已设置
	hasConsolePwd := strings.TrimSpace(out.ConsolePassword) != "" || strings.TrimSpace(os.Getenv("QODER2API_CONSOLE_PASSWORD")) != ""
	out.ConsolePassword = ""
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"settings":             out,
		"connection":           connectionInfo(),
		"console_password_set": hasConsolePwd,
	})
}

func handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	var req struct {
		CNPort           *int     `json:"cn_port"`
		GlobalPort       *int     `json:"global_port"`
		CationToken      *string  `json:"cn_token"`
		GlobalToken      *string  `json:"global_token"`
		LogLevel         *string  `json:"log_level"`
		AutoStart        *bool    `json:"auto_start"`
		AutoCheckin      *bool    `json:"auto_checkin"`
		AutoCheckinTimes []string `json:"auto_checkin_times"`
		ConsolePassword  *string  `json:"console_password"`
		// 新格式：包含旧密码和新密码
		ConsolePasswordNew *struct {
			OldPassword string  `json:"old_password"`
			NewPassword *string `json:"new_password"`
		} `json:"console_password_new"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cur, err := svc.GetSettings()
	if err != nil || cur == nil {
		cur = &account.Settings{CNPort: account.DefaultCNPort, GlobalPort: account.DefaultGlobalPort, LogLevel: "info"}
	}
	// 端口校验：0=停用该侧，其余必须在合法范围；两侧不能同时停用、也不能撞同一端口
	validPort := func(p int) bool { return p >= 0 && p <= 65535 }
	if req.CNPort != nil {
		if !validPort(*req.CNPort) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid cn_port"))
			return
		}
		cur.CNPort = *req.CNPort
	}
	if req.GlobalPort != nil {
		if !validPort(*req.GlobalPort) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid global_port"))
			return
		}
		cur.GlobalPort = *req.GlobalPort
	}
	if cur.CNPort == 0 && cur.GlobalPort == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("CN 与 Global 不能同时停用：至少保留一侧端口"))
		return
	}
	if cur.CNPort > 0 && cur.CNPort == cur.GlobalPort {
		writeError(w, http.StatusBadRequest, fmt.Errorf("两侧端口不能相同（当前 CN=%d Global=%d）", cur.CNPort, cur.GlobalPort))
		return
	}
	// 两区各一把 key；留空或填回默认值 qccg 都视为「用默认」，不落盘
	applyToken := func(src *string, dst *string) {
		if src == nil {
			return
		}
		tok := strings.TrimSpace(*src)
		if tok == "" || tok == "qccg" {
			*dst = ""
		} else {
			*dst = tok
		}
	}
	applyToken(req.CationToken, &cur.CationToken)
	applyToken(req.GlobalToken, &cur.GlobalToken)
	if req.LogLevel != nil && *req.LogLevel != "" {
		cur.LogLevel = *req.LogLevel
	}
	if req.AutoStart != nil {
		cur.AutoStart = *req.AutoStart
	}
	if req.AutoCheckin != nil {
		cur.AutoCheckin = *req.AutoCheckin
		logger.Info("auto checkin set to %v", cur.AutoCheckin)
	}
	if req.AutoCheckinTimes != nil {
		var times []string
		for _, t := range req.AutoCheckinTimes {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if _, _, ok := parseHHMM(t); !ok {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid auto_checkin_times entry: %q (want HH:MM)", t))
				return
			}
			times = append(times, t)
		}
		cur.AutoCheckinTimes = times
		logger.Info("auto checkin times set to %v", times)
	}

	// 处理新格式的密码修改
	if req.ConsolePasswordNew != nil {
		oldPwd := strings.TrimSpace(req.ConsolePasswordNew.OldPassword)
		newPwd := ""
		if req.ConsolePasswordNew.NewPassword != nil {
			newPwd = strings.TrimSpace(*req.ConsolePasswordNew.NewPassword)
		}

		// 验证旧密码
		currentPassword := effectiveConsolePassword()
		if currentPassword == "" {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("当前未设置控制台密码"))
			return
		}

		a := sha256.Sum256([]byte(oldPwd))
		b := sha256.Sum256([]byte(currentPassword))
		if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("旧密码错误"))
			return
		}

		// 如果提供了新密码，则更新
		if newPwd != "" {
			cur.ConsolePassword = newPwd
		}
	} else if req.ConsolePassword != nil {
		// 旧格式：直接设置密码
		p := strings.TrimSpace(*req.ConsolePassword)
		if p != "" {
			cur.ConsolePassword = p
		}
	}

	if err := svc.SaveSettings(cur); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	safe := *cur
	safe.ConsolePassword = ""
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"connection": connectionInfo(),
		"settings":   safe,
		"note":       "已保存；控制台密码立即生效；端口变更只重启受影响的那一侧，另一侧不打断",
	})
}

func handleListModels(w http.ResponseWriter, r *http.Request) {
	// ?region=cn|global 指定查哪一侧；不带参数则用第一个已启动的桥
	var region account.Region
	if raw := strings.TrimSpace(r.URL.Query().Get("region")); raw != "" {
		region = account.NormalizeRegion(raw)
	}
	models, err := svc.ListQoderModels(region)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, svc.GetLogs(100))
}

func handleCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	if err := svc.CleanupAllData(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// requestRegion 从查询串或 JSON body 取可选的 region 参数。
// 空串表示「两侧都作用」。
func requestRegion(r *http.Request) account.Region {
	if raw := strings.TrimSpace(r.URL.Query().Get("region")); raw != "" {
		return account.NormalizeRegion(raw)
	}
	if r.Method == http.MethodPost {
		var req struct {
			Region string `json:"region"`
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		if json.Unmarshal(body, &req) == nil {
			if raw := strings.TrimSpace(req.Region); raw != "" {
				return account.NormalizeRegion(raw)
			}
		}
	}
	return ""
}

func handleStartBridge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	if err := svc.StartBridge(requestRegion(r)); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, svc.GetStatus())
}

func handleStopBridge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	region := requestRegion(r)
	var err error
	if region == "" {
		err = svc.StopBridge()
	} else {
		err = svc.StopRegionBridge(region)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, svc.GetStatus())
}
