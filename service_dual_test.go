package main

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"qoder2api/account"
	"qoder2api/internal/bridge"
	"qoder2api/internal/cosy"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// fakeSlot 造一个不触网的池内账号（session 本地生成，凭证不参与构造）。
func fakeSlot(t *testing.T, id string, region account.Region) *bridge.Slot {
	t.Helper()
	sess, err := cosy.NewSession(cosy.AuthIdentity{Uid: id, UserType: "personal_standard"}, "mid-"+id, "mtok-"+id, "mtype-"+id)
	if err != nil {
		t.Fatalf("NewSession(%s): %v", id, err)
	}
	return bridge.NewSlot(id, id, region, sess, bridge.NewBearerClient(sess))
}

// waitOpen 轮询到端口能连上为止（桥在 goroutine 里 ListenAndServe）。
func waitOpen(t *testing.T, port int, wantOpen bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			if wantOpen {
				return
			}
		} else if !wantOpen {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port %d state did not become open=%v", port, wantOpen)
}

func statusFor(t *testing.T, s *Service, region account.Region) account.BridgeStatus {
	t.Helper()
	for _, bs := range s.GetStatus().Bridges {
		if bs.Region == region {
			return bs
		}
	}
	t.Fatalf("status missing region %s", region)
	return account.BridgeStatus{}
}

// 控制台状态必须同时列出两侧，并且各自说清「为什么没在跑」。
func TestStatusListsBothRegions(t *testing.T) {
	account.SetDataRoot(t.TempDir())
	s := NewService([]byte(`{"messages":[]}`))
	st := s.GetStatus()
	if len(st.Bridges) != 2 {
		t.Fatalf("bridges = %d, want 2 (%+v)", len(st.Bridges), st.Bridges)
	}
	cn, gl := statusFor(t, s, account.RegionCN), statusFor(t, s, account.RegionGlobal)
	if cn.Running || gl.Running {
		t.Fatalf("no accounts configured but bridges running: %+v %+v", cn, gl)
	}
	if cn.Port != account.DefaultCNPort || gl.Port != account.DefaultGlobalPort {
		t.Fatalf("default ports = %d/%d, want %d/%d", cn.Port, gl.Port, account.DefaultCNPort, account.DefaultGlobalPort)
	}
	if cn.Reason == "" || gl.Reason == "" {
		t.Fatalf("idle bridges must explain why: cn=%q global=%q", cn.Reason, gl.Reason)
	}
}

// 只有某一区有账号时，那一区上听、另一区保持「未启动」且不影响对方。
func TestOnlyRegionWithAccountsListens(t *testing.T) {
	account.SetDataRoot(t.TempDir())
	cnPort, glPort := freePort(t), freePort(t)
	s := NewService([]byte(`{"messages":[]}`))
	s.SetPorts(cnPort, glPort)

	cnPool := bridge.NewPool(fakeSlot(t, "cn-1", account.RegionCN))
	if err := s.listen(account.RegionCN, cnPort, cnPool); err != nil {
		t.Fatalf("listen cn: %v", err)
	}
	waitOpen(t, cnPort, true)

	if err := s.ensureRegionBridge(account.RegionGlobal); err == nil {
		t.Fatal("global side should refuse to start without a global account")
	}
	if bs := statusFor(t, s, account.RegionGlobal); bs.Running {
		t.Fatal("global side reported running without accounts")
	}
	// CN 侧必须照常对外服务
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/models", cnPort))
	if err != nil {
		t.Fatalf("cn bridge unreachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cn bridge without api key = %d, want 401", resp.StatusCode)
	}
}

// 停一侧不能碰另一侧：CN 端口关闭后 Global 仍在服务。
func TestStopOneRegionLeavesOtherServing(t *testing.T) {
	account.SetDataRoot(t.TempDir())
	cnPort, glPort := freePort(t), freePort(t)
	s := NewService([]byte(`{"messages":[]}`))
	s.SetPorts(cnPort, glPort)

	if err := s.listen(account.RegionCN, cnPort, bridge.NewPool(fakeSlot(t, "cn-1", account.RegionCN))); err != nil {
		t.Fatalf("listen cn: %v", err)
	}
	if err := s.listen(account.RegionGlobal, glPort, bridge.NewPool(fakeSlot(t, "gl-1", account.RegionGlobal))); err != nil {
		t.Fatalf("listen global: %v", err)
	}
	waitOpen(t, cnPort, true)
	waitOpen(t, glPort, true)

	if err := s.StopRegionBridge(account.RegionCN); err != nil {
		t.Fatalf("stop cn: %v", err)
	}
	waitOpen(t, cnPort, false)
	if bs := statusFor(t, s, account.RegionGlobal); !bs.Running {
		t.Fatalf("global bridge got dragged down by stopping cn: %+v", bs)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/models", glPort))
	if err != nil {
		t.Fatalf("global bridge unreachable after cn stop: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("global bridge = %d, want 401", resp.StatusCode)
	}
}

// 两侧撞同一端口时，后启动的一侧必须被挡下而不是抢端口或静默失败。
func TestConflictingPortsBlockSecondBridge(t *testing.T) {
	account.SetDataRoot(t.TempDir())
	shared := freePort(t)
	s := NewService([]byte(`{"messages":[]}`))
	s.SetPorts(shared, shared)
	// 标记 Global 侧已在监听（只验证冲突判定，不必真的占端口）
	s.mu.Lock()
	s.bridges[account.RegionGlobal] = &regionBridge{region: account.RegionGlobal, port: shared, server: &http.Server{}}
	s.mu.Unlock()

	err := s.ensureRegionBridge(account.RegionCN)
	if err == nil {
		t.Fatal("cn should refuse a port already taken by the global bridge")
	}
	if bs := statusFor(t, s, account.RegionCN); bs.Reason == "" {
		t.Fatalf("conflict must be explained in status: %+v", bs)
	}
}

// 删账号是热退出：池里摘掉它，端口不重启。
func TestSyncPoolDropsDeletedAccounts(t *testing.T) {
	account.SetDataRoot(t.TempDir())
	s := NewService([]byte(`{"messages":[]}`))
	pool := bridge.NewPool(fakeSlot(t, "ghost", account.RegionCN))
	if err := account.Save(&account.Account{ID: "cn-live", Name: "cn-live", Region: account.RegionCN}); err != nil {
		t.Fatal(err)
	}

	changed, err := s.syncPool(account.RegionCN, pool)
	if err != nil {
		t.Fatalf("syncPool: %v", err)
	}
	if changed != 1 || pool.Has("ghost") || pool.Size() != 0 {
		t.Fatalf("ghost should be dropped: changed=%d size=%d", changed, pool.Size())
	}
}

// 连接信息要同时给出两区 base_url，客户端才能各走各的端口。
func TestConnectionInfoCoversBothRegions(t *testing.T) {
	account.SetDataRoot(t.TempDir())
	s := NewService([]byte(`{"messages":[]}`))
	s.SetPorts(9100, 9101)
	old := svc
	svc = s
	defer func() { svc = old }()

	conn := connectionInfo()
	regions, ok := conn["regions"].(map[string]interface{})
	if !ok || len(regions) != 2 {
		t.Fatalf("connection regions = %v", conn["regions"])
	}
	cn, _ := regions["cn"].(map[string]interface{})
	gl, _ := regions["global"].(map[string]interface{})
	if cn == nil || gl == nil {
		t.Fatalf("both regions must be present: %+v", regions)
	}
	if cn["port"] != 9100 || gl["port"] != 9101 {
		t.Fatalf("ports wrong: cn=%v global=%v", cn["port"], gl["port"])
	}
	if cn["openai_base_url"] != "http://127.0.0.1:9100/v1" || gl["openai_base_url"] != "http://127.0.0.1:9101/v1" {
		t.Fatalf("base urls: cn=%v global=%v", cn["openai_base_url"], gl["openai_base_url"])
	}
}
