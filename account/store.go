package account

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var mu sync.Mutex

func dir() (string, error) {
	d := accountsDir()
	return d, os.MkdirAll(d, 0700)
}

func List() ([]Account, error) {
	mu.Lock()
	defer mu.Unlock()
	d, err := dir()
	if err != nil {
		return nil, err
	}
	return listUnlocked(d)
}

func Save(a *Account) error {
	mu.Lock()
	defer mu.Unlock()
	d, err := dir()
	if err != nil {
		return err
	}
	if a.ID == "" {
		a.ID = SanitizeID(a.Email + a.Name + fmt.Sprintf("%d", time.Now().UnixNano()))
	}
	return saveUnlocked(d, a)
}

func Delete(id string) error {
	mu.Lock()
	defer mu.Unlock()
	d, err := dir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(d, SanitizeID(id)+".json"))
}

// SetActiveForRegion 在指定区域内设为唯一激活账号，其他区域不受影响。
// 双桥模式下每个区域各有一个激活账号（CN 一个、Global 一个），
// 切换某一区的激活账号绝不能把另一区的「激活」抢走——那会让另一区桥失去默认账号。
func SetActiveForRegion(region Region, id string) error {
	mu.Lock()
	defer mu.Unlock()
	d, err := dir()
	if err != nil {
		return err
	}
	accounts, err := listUnlocked(d)
	if err != nil {
		return err
	}
	want := region.Normalized()
	for i := range accounts {
		if accounts[i].Region.Normalized() != want {
			continue
		}
		accounts[i].Active = accounts[i].ID == id
		if err := saveUnlocked(d, &accounts[i]); err != nil {
			return err
		}
	}
	return nil
}

// SetActive 是上游 API 的兼容包装：按账号自身 region 作用，等价于
// SetActiveForRegion(acct.Region, id)。
func SetActive(id string) error {
	acct, err := Get(id)
	if err != nil {
		return err
	}
	return SetActiveForRegion(acct.Region, id)
}

// GetActiveForRegion 返回该区域的激活账号；没有则返回 nil（另一区的激活账号不会被误取）。
func GetActiveForRegion(region Region) (*Account, error) {
	accounts, err := List()
	if err != nil {
		return nil, err
	}
	want := region.Normalized()
	for i := range accounts {
		if accounts[i].Region.Normalized() == want && accounts[i].Active {
			return &accounts[i], nil
		}
	}
	return nil, nil
}

// GetActiveByRegion 是上游命名的兼容别名，行为与 GetActiveForRegion 一致。
func GetActiveByRegion(region Region) (*Account, error) {
	return GetActiveForRegion(region)
}

// GetActive 返回任意一个激活账号（上游兼容 API）。双桥下优先 CN，再 Global。
func GetActive() (*Account, error) {
	for _, r := range Regions {
		if acct, err := GetActiveForRegion(r); err != nil {
			return nil, err
		} else if acct != nil {
			return acct, nil
		}
	}
	return nil, nil
}

// ListByRegion 返回归属该区域的全部账号（空 region 字段视为 global）。
func ListByRegion(region Region) ([]Account, error) {
	accounts, err := List()
	if err != nil {
		return nil, err
	}
	want := region.Normalized()
	out := make([]Account, 0, len(accounts))
	for _, a := range accounts {
		if a.Region.Normalized() == want {
			out = append(out, a)
		}
	}
	return out, nil
}

// Get 根据 ID 获取账号
func Get(id string) (*Account, error) {
	accounts, err := List()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if accounts[i].ID == id {
			return &accounts[i], nil
		}
	}
	return nil, fmt.Errorf("account not found: %s", id)
}

func listUnlocked(d string) ([]Account, error) {
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, err
	}
	var accounts []Account
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(d, e.Name()))
		if err != nil {
			continue
		}
		var a Account
		if err := json.Unmarshal(data, &a); err != nil {
			continue
		}
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].SortOrder < accounts[j].SortOrder
	})
	return accounts, nil
}

func saveUnlocked(d string, a *Account) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, a.ID+".json"), data, 0600)
}

func Reorder(ids []string) error {
	mu.Lock()
	defer mu.Unlock()
	d, err := dir()
	if err != nil {
		return err
	}
	accounts, err := listUnlocked(d)
	if err != nil {
		return err
	}
	orderMap := make(map[string]int)
	for i, id := range ids {
		orderMap[id] = i
	}
	for i := range accounts {
		if order, ok := orderMap[accounts[i].ID]; ok {
			accounts[i].SortOrder = order
		}
		if err := saveUnlocked(d, &accounts[i]); err != nil {
			return err
		}
	}
	return nil
}

func SanitizeID(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = fmt.Sprintf("acct%d", time.Now().UnixNano())
	}
	return s
}

func LoadSettings() (*Settings, error) {
	path := settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		// 返回默认设置
		return &Settings{
			CNPort:     DefaultCNPort,
			GlobalPort: DefaultGlobalPort,
			LogLevel:   "info",
		}, nil
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	// 旧版单桥 settings.json 迁移：port/bridge_token 是上游字段。
	// 迁移后清空，避免下次保存继续写废弃字段。
	if s.CNPort == 0 && s.GlobalPort == 0 && s.Port > 0 {
		if s.Port == DefaultGlobalPort {
			// 旧 --instance=global 的单桥设置：它原本就是 Global 端口，
			// 不能同时当成 CN，否则两桥会抢同一端口。
			s.GlobalPort = s.Port
		} else {
			s.CNPort = s.Port
			s.GlobalPort = DefaultGlobalPort
		}
	}
	if s.CationToken == "" && s.GlobalToken == "" && s.BridgeToken != "" {
		// 旧版两桥共用一把 key：拆到两区，行为保持不变
		s.CationToken = s.BridgeToken
		s.GlobalToken = s.BridgeToken
	}
	s.Port = 0
	s.BridgeToken = ""
	if s.CNPort == 0 && s.GlobalPort == 0 {
		// 两侧都为 0 会让服务没有任何 API 端口，视为未配置 → 回到默认双桥
		s.CNPort = DefaultCNPort
		s.GlobalPort = DefaultGlobalPort
	}
	return &s, nil
}

func SaveSettings(s *Settings) error {
	path := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// EnsureMachineSalt 读取（或首次生成并落盘）本机设备指纹盐（settings.json machine_salt）。
// 用途：使每个部署的指纹派生空间独立，防止 uid 派生模式被上游全局识别。
// 生成后保持不变——变更 salt 即所有账号指纹整体漂移（等价于换设备）。
func EnsureMachineSalt() (string, error) {
	st, err := LoadSettings()
	if err != nil || st == nil {
		st = &Settings{CNPort: DefaultCNPort, GlobalPort: DefaultGlobalPort, LogLevel: "info"}
	}
	if st.MachineSalt != "" {
		return st.MachineSalt, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	st.MachineSalt = hex.EncodeToString(b)
	if err := SaveSettings(st); err != nil {
		return "", err
	}
	return st.MachineSalt, nil
}
