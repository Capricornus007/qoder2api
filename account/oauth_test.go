package account

import (
	"strings"
	"testing"
)

// 双版本「同时可用」包含一次同时挂两条授权流：CN 与 Global 的 pending 必须并存，
// 且任一区的操作都不能影响另一区。
func TestPendingLoginsCoexistPerRegion(t *testing.T) {
	SetDataRoot(t.TempDir())

	cn, err := StartLogin(RegionCN)
	if err != nil {
		t.Fatalf("StartLogin(cn): %v", err)
	}
	gl, err := StartLogin(RegionGlobal)
	if err != nil {
		t.Fatalf("StartLogin(global): %v", err)
	}
	if !strings.Contains(cn.LoginURL, "qoder.com.cn") {
		t.Errorf("cn 授权链接没指向国内站: %s", cn.LoginURL)
	}
	if strings.Contains(gl.LoginURL, "qoder.com.cn") {
		t.Errorf("global 授权链接串到了国内站: %s", gl.LoginURL)
	}

	got := PendingRegions()
	if len(got) != 2 {
		t.Fatalf("两区应同时挂起，实际 %v", got)
	}
	if got[RegionCN] != cn.LoginID || got[RegionGlobal] != gl.LoginID {
		t.Fatalf("挂起的 loginID 不匹配: %v", got)
	}

	// 同区重跑只替换自己那条，另一区必须原封不动
	cn2, err := StartLogin(RegionCN)
	if err != nil {
		t.Fatal(err)
	}
	got = PendingRegions()
	if len(got) != 2 || got[RegionCN] != cn2.LoginID || got[RegionGlobal] != gl.LoginID {
		t.Fatalf("替换 CN 后应仍是两区各一条: %v", got)
	}
	// 被替换掉的旧流再等必须立刻失败（不触网：已从表中移除）
	if _, err := WaitLogin(cn.LoginID); err == nil {
		t.Fatal("被替换的旧 CN 流不该还能等")
	}
	if p := PendingRegions(); p[RegionGlobal] != gl.LoginID {
		t.Fatalf("旧的 WaitLogin 失败波及了 Global: %v", p)
	}

	CancelLogin(cn2.LoginID)
	got = PendingRegions()
	if _, ok := got[RegionCN]; ok {
		t.Errorf("取消 CN 后仍有 CN 挂起: %v", got)
	}
	if got[RegionGlobal] != gl.LoginID {
		t.Errorf("取消 CN 影响了 Global: %v", got)
	}

	// 未知 loginID 不能命中任何在等的会话（否则会把别区的流误完成）
	if _, err := WaitLogin("does-not-exist"); err == nil {
		t.Fatal("未知 loginID 应当报错")
	}
	if p := PendingRegions(); p[RegionGlobal] != gl.LoginID {
		t.Fatalf("未知 loginID 的等待影响了 Global 会话: %v", p)
	}

	CancelLogin(gl.LoginID)
	if p := PendingRegions(); len(p) != 0 {
		t.Fatalf("全部取消后应无残留: %v", p)
	}
}
