package account

import (
	"testing"
	"time"
)

func mk(id string, region Region) *Account {
	return &Account{ID: id, Name: id, Region: region, AuthMode: "pat", CreatedAt: time.Now()}
}

// 每个区域各自保有一个激活账号：切 CN 的号绝不能把 Global 的激活抢走。
func TestSetActiveForRegionIsScopedToRegion(t *testing.T) {
	SetDataRoot(t.TempDir())
	for _, a := range []*Account{mk("cn-1", RegionCN), mk("cn-2", RegionCN), mk("gl-1", RegionGlobal)} {
		if err := Save(a); err != nil {
			t.Fatalf("save %s: %v", a.ID, err)
		}
	}

	if err := SetActiveForRegion(RegionGlobal, "gl-1"); err != nil {
		t.Fatalf("activate gl-1: %v", err)
	}
	if err := SetActiveForRegion(RegionCN, "cn-1"); err != nil {
		t.Fatalf("activate cn-1: %v", err)
	}
	// 激活 CN 的另一号：Global 侧必须保持 gl-1 不变
	if err := SetActiveForRegion(RegionCN, "cn-2"); err != nil {
		t.Fatalf("activate cn-2: %v", err)
	}

	cn, err := GetActiveForRegion(RegionCN)
	if err != nil || cn == nil || cn.ID != "cn-2" {
		t.Fatalf("cn active = %v (%v), want cn-2", cn, err)
	}
	gl, err := GetActiveForRegion(RegionGlobal)
	if err != nil || gl == nil || gl.ID != "gl-1" {
		t.Fatalf("global active = %v (%v), want gl-1（被 CN 操作波及）", gl, err)
	}
}

// 缺省 region 字段（历史数据 / 空串）归属 global，不能凭空变成 CN。
func TestEmptyRegionDefaultsToGlobal(t *testing.T) {
	SetDataRoot(t.TempDir())
	legacy := &Account{ID: "legacy", Name: "legacy", AuthMode: "pat", CreatedAt: time.Now()}
	if err := Save(legacy); err != nil {
		t.Fatal(err)
	}
	if err := SetActiveForRegion(RegionGlobal, "legacy"); err != nil {
		t.Fatal(err)
	}
	if a, _ := GetActiveForRegion(RegionGlobal); a == nil || a.ID != "legacy" {
		t.Fatalf("legacy account should be reachable as global, got %v", a)
	}
	if a, _ := GetActiveForRegion(RegionCN); a != nil {
		t.Fatalf("CN side must not see the global-only account, got %v", a)
	}
	cnList, err := ListByRegion(RegionCN)
	if err != nil || len(cnList) != 0 {
		t.Fatalf("ListByRegion(cn) = %v (%v), want empty", cnList, err)
	}
	glList, err := ListByRegion(RegionGlobal)
	if err != nil || len(glList) != 1 {
		t.Fatalf("ListByRegion(global) = %v (%v), want 1", glList, err)
	}
}

// 两侧都为 0 端口会让服务一个 API 口都不剩，必须回落到默认双桥端口。
func TestLoadSettingsDefaultsBothPorts(t *testing.T) {
	SetDataRoot(t.TempDir())
	st, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if st.CNPort != DefaultCNPort || st.GlobalPort != DefaultGlobalPort {
		t.Fatalf("defaults = CN %d Global %d, want %d/%d", st.CNPort, st.GlobalPort, DefaultCNPort, DefaultGlobalPort)
	}

	// 显式停用一侧（另一个保留）必须被尊重
	st.CNPort, st.GlobalPort = 0, 9000
	if err := SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.CNPort != 0 || got.GlobalPort != 9000 {
		t.Fatalf("single-side disable not honored: %+v", got)
	}

	// 两侧同时为 0 视为未配置
	if err := SaveSettings(&Settings{LogLevel: "info"}); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadSettings()
	if got.CNPort != DefaultCNPort || got.GlobalPort != DefaultGlobalPort {
		t.Fatalf("0/0 should fall back to defaults, got CN %d Global %d", got.CNPort, got.GlobalPort)
	}
}
