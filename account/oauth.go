package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"qoder2api/logger"
)

// OAuth 认证流程
// 1. StartLogin: 生成 PKCE 参数，返回登录 URL
// 2. WaitLogin: 轮询 deviceToken/poll 端点，等待用户授权
// 3. 获取 device token (dt-xxx)，用于后续 API 调用

const oauthClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"

// oauthSessionTTL 是待授权 OAuth 会话的有效期。默认 10 分钟（用户需在
// 此时间内完成浏览器授权），可用 QODER2API_OAUTH_TIMEOUT 覆盖（如 "30m"）。
func oauthSessionTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("QODER2API_OAUTH_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		logger.Error("invalid QODER2API_OAUTH_TIMEOUT=%q, falling back to 10m", v)
	}
	return 10 * time.Minute
}

type pendingOAuth struct {
	loginID  string
	nonce    string
	verifier string
	region   Region
	ctx      context.Context
	cancel   context.CancelFunc
	deadline time.Time
}

// pendingLogins 以 loginID 为键：国内版与国际版的授权流必须能同时挂着
// （「双版本同时可用」包含一次命令登两个号）。旧实现是单个 pending 变量，
// 后起的流会 cancel 掉前一条，导致只能轮流登录。
// 同一区域仍只保留最新一条：重跑向导不该留下两条互相抢授权的同号流。
var (
	pendingMu     sync.Mutex
	pendingLogins = map[string]*pendingOAuth{}
)

// pruneExpiredLocked 清掉已过期待授权的会话（调用方须持有 pendingMu）。
func pruneExpiredLocked() {
	now := time.Now()
	for id, p := range pendingLogins {
		if now.After(p.deadline) {
			p.cancel()
			delete(pendingLogins, id)
		}
	}
}

// PendingRegions 返回当前挂着的待授权会话：region → loginID。
// 供状态展示与测试用；同一区域最多一条。
func PendingRegions() map[Region]string {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	out := map[Region]string{}
	for _, p := range pendingLogins {
		out[p.region.Normalized()] = p.loginID
	}
	return out
}

// StartLogin 启动 OAuth 登录流程
func StartLogin(region Region) (*OAuthSession, error) {
	verifier, challenge, err := pkce()
	if err != nil {
		return nil, err
	}
	nonce := newSimpleID()
	loginID := newSimpleID()

	ep := GetEndpoints(region)
	params := url.Values{}
	params.Set("nonce", nonce)
	params.Set("challenge", challenge)
	params.Set("challenge_method", "S256")
	params.Set("client_id", oauthClientID)
	loginURL := ep.DeviceLoginBase + "?" + params.Encode()

	logger.Info("OAuth: StartLogin loginID=%s region=%s", loginID, region)

	pendingMu.Lock()
	defer pendingMu.Unlock()
	pruneExpiredLocked()
	want := region.Normalized()
	for id, p := range pendingLogins {
		if p.region.Normalized() == want {
			logger.Info("OAuth: replacing previous pending login for region %s (id=%s)", want, id)
			p.cancel()
			delete(pendingLogins, id)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	pendingLogins[loginID] = &pendingOAuth{
		loginID:  loginID,
		nonce:    nonce,
		verifier: verifier,
		region:   region,
		ctx:      ctx,
		cancel:   cancel,
		deadline: time.Now().Add(oauthSessionTTL()),
	}

	return &OAuthSession{LoginID: loginID, LoginURL: loginURL}, nil
}

// WaitLogin 等待用户完成 OAuth 授权。只操作自己那一条会话，
// 不影响同时在等的其他区域。
func WaitLogin(loginID string) (*Account, error) {
	logger.Debug("OAuth: WaitLogin called loginID=%s", loginID)

	pendingMu.Lock()
	p := pendingLogins[loginID]
	pendingMu.Unlock()

	if p == nil {
		logger.Error("OAuth: no pending login for id %s", loginID)
		return nil, fmt.Errorf("no pending login for id %s", loginID)
	}

	drop := func() {
		pendingMu.Lock()
		if cur, ok := pendingLogins[loginID]; ok && cur == p {
			delete(pendingLogins, loginID)
		}
		pendingMu.Unlock()
	}

	ep := GetEndpoints(p.region)

	logger.Info("OAuth: Starting poll loop (region=%s)", p.region)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if time.Now().After(p.deadline) {
			logger.Error("OAuth: Timeout reached (region=%s)", p.region)
			drop()
			return nil, fmt.Errorf("oauth login timed out")
		}
		select {
		case <-p.ctx.Done():
			logger.Info("OAuth: Cancelled (region=%s)", p.region)
			drop()
			return nil, fmt.Errorf("oauth login cancelled")
		case <-ticker.C:
			deviceToken, refreshToken, err := pollToken(p.nonce, p.verifier, ep)
			if err != nil {
				continue
			}
			logger.Info("OAuth: Got token, building account")
			acct, err := buildAccountFromToken(deviceToken, refreshToken, p.region, ep)
			if err != nil {
				logger.Error("OAuth: Build account error: %v", err)
				return nil, err
			}
			drop()
			logger.Info("OAuth: Success! Account: %s region=%s", acct.Name, p.region)
			return acct, nil
		}
	}
}

// CancelLogin 取消指定会话，其他区域的待授权流不受影响。
func CancelLogin(loginID string) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	if p, ok := pendingLogins[loginID]; ok {
		p.cancel()
		delete(pendingLogins, loginID)
	}
}

// pollToken 轮询 deviceToken/poll 端点
func pollToken(nonce, verifier string, ep Endpoints) (string, string, error) {
	reqURL := fmt.Sprintf("%s?nonce=%s&verifier=%s&challenge_method=S256",
		ep.PollEndpoint,
		url.QueryEscape(nonce),
		url.QueryEscape(verifier))

	logger.Debug("OAuth: Polling %s", ep.PollEndpoint)

	resp, err := http.Get(reqURL)
	if err != nil {
		logger.Error("OAuth: Poll error: %v", err)
		return "", "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	logger.Debug("OAuth: Response [%d]: %s", resp.StatusCode, string(raw))

	// 404 表示还没授权，继续等待
	if resp.StatusCode == 404 {
		return "", "", fmt.Errorf("not authorized yet")
	}

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("poll: HTTP %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", "", err
	}

	deviceToken, _ := result["token"].(string)
	refreshToken, _ := result["refresh_token"].(string)

	if deviceToken == "" {
		return "", "", fmt.Errorf("no device token in response")
	}
	if refreshToken != "" {
		logger.Info("OAuth: Got device token and refresh token")
	} else {
		logger.Info("OAuth: Got device token")
	}
	return deviceToken, refreshToken, nil
}

// buildAccountFromToken 使用 device token 构建账号信息
func buildAccountFromToken(deviceToken, refreshToken string, region Region, ep Endpoints) (*Account, error) {
	info, err := fetchUserInfo(deviceToken, ep)
	if err != nil {
		return nil, fmt.Errorf("fetch user info: %w", err)
	}
	plan := fetchPlan(deviceToken, ep)

	id := SanitizeID(strGet(info, "userId") + strGet(info, "email"))
	if id == "" {
		id = newSimpleID()
	}
	now := time.Now()
	acct := &Account{
		ID:        id,
		Name:      strGet(info, "name"),
		Email:     strings.ToLower(strGet(info, "email")),
		UserType:  strGet(info, "userType"),
		Plan:      plan,
		Region:    region,
		AuthMode:  "oauth",
		APIMode:   "openai",
		Tags:      []string{},
		CreatedAt: now,
	}
	secretPayload, err := json.Marshal(map[string]string{
		"device_token":  deviceToken,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, err
	}
	if err := SaveSecret(id, string(secretPayload)); err != nil {
		return nil, err
	}
	return acct, nil
}

func fetchUserInfo(token string, ep Endpoints) (map[string]interface{}, error) {
	req, _ := http.NewRequest("GET", ep.UserinfoBase, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	raw, _ := io.ReadAll(resp.Body)
	json.Unmarshal(raw, &result)
	return result, nil
}

func fetchPlan(token string, ep Endpoints) string {
	req, _ := http.NewRequest("GET", ep.PlanEndpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	raw, _ := io.ReadAll(resp.Body)
	json.Unmarshal(raw, &result)
	return strGet(result, "plan_tier_name")
}

func pkce() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

func newSimpleID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func strGet(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func FetchQuota(token string, region Region) (*QuotaInfo, error) {
	ep := GetEndpoints(region)
	req, _ := http.NewRequest("GET", ep.QuotaEndpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	fmt.Printf("[QUOTA DEBUG] API URL: %s\n", ep.QuotaEndpoint)
	fmt.Printf("[QUOTA DEBUG] Raw response: %s\n", string(raw))

	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	info := &QuotaInfo{
		Plan:            fetchPlan(token, ep),
		IsQuotaExceeded: result["isQuotaExceeded"] == true,
		ExpiresAt:       int64(toFloat(result, "expiresAt")),
	}

	if uq := extractBucket(result, "userQuota"); uq != nil {
		info.UserQuota = uq
		fmt.Printf("[QUOTA DEBUG] userQuota found: %+v\n", uq)
	} else {
		fmt.Printf("[QUOTA DEBUG] userQuota NOT found in response\n")
	}

	if aq := extractBucket(result, "addOnQuota"); aq != nil {
		info.AddonQuota = aq
		fmt.Printf("[QUOTA DEBUG] addonQuota found: %+v\n", aq)
	} else {
		fmt.Printf("[QUOTA DEBUG] addonQuota NOT found in response\n")
	}

	return info, nil
}

func extractBucket(data map[string]interface{}, key string) *QuotaBucket {
	obj, ok := data[key].(map[string]interface{})
	if !ok {
		return nil
	}
	used := toFloat(obj, "used")
	total := toFloat(obj, "total")
	remaining := toFloat(obj, "remaining")
	if total == 0 && used == 0 && remaining == 0 {
		return nil
	}
	resetTime := ""
	if v, ok := obj["resetTime"].(string); ok {
		resetTime = v
	} else if v, ok := obj["reset_time"].(string); ok {
		resetTime = v
	}
	return &QuotaBucket{Used: used, Total: total, Remaining: remaining, ResetTime: resetTime}
}

func toFloat(m map[string]interface{}, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
