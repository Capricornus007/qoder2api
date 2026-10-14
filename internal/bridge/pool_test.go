package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"qoder2api/account"
	"qoder2api/internal/cosy"
)

func testSlotFull(t *testing.T, id, userType string, region account.Region) *Slot {
	t.Helper()
	account.SetDataRoot(t.TempDir())
	sess, err := cosy.NewSession(
		cosy.AuthIdentity{Uid: id, UserType: userType},
		"mid-"+id, "mtok-"+id, "mtype-"+id,
	)
	if err != nil {
		t.Fatalf("NewSession(%s): %v", id, err)
	}
	s := NewSlot(id, id, region, sess, NewBearerClient(sess))
	// catalogAt 置为「刚拉过」：目录仍为 nil（不过滤），但选号路径不会去敲真实上游
	s.catalogAt = time.Now()
	return s
}

func testSlot(t *testing.T, id, userType string) *Slot {
	t.Helper()
	return testSlotFull(t, id, userType, account.RegionCN)
}

func TestPoolPickSkipsCooling(t *testing.T) {
	p := NewPool(testSlot(t, "a", "t"), testSlot(t, "b", "t"))
	s1, ok := p.Pick(nil)
	if !ok || s1.ID != "a" {
		t.Fatalf("first pick = %v", s1)
	}
	p.Cool("a", time.Minute, "quota")
	s2, ok := p.Pick(nil)
	if !ok || s2.ID != "b" {
		t.Fatalf("after cooling a, pick = %v", s2)
	}
	p.Cool("b", time.Minute, "quota")
	if _, ok := p.Pick(nil); ok {
		t.Fatal("expected no available account when all cooling")
	}
}

func TestPoolPickRespectsExclude(t *testing.T) {
	p := NewPool(testSlot(t, "a", "t"), testSlot(t, "b", "t"))
	if s, ok := p.Pick(map[string]struct{}{"a": {}}); !ok || s.ID != "b" {
		t.Fatalf("exclude a -> %v", s)
	}
	if _, ok := p.Pick(map[string]struct{}{"a": {}, "b": {}}); ok {
		t.Fatal("all excluded should yield none")
	}
}

func TestPoolCooldownExpires(t *testing.T) {
	p := NewPool(testSlot(t, "a", "t"))
	p.Cool("a", 20*time.Millisecond, "transient")
	if _, ok := p.Pick(nil); ok {
		t.Fatal("should be cooling")
	}
	time.Sleep(30 * time.Millisecond)
	if _, ok := p.Pick(nil); !ok {
		t.Fatal("cooldown should have expired")
	}
}

func TestShouldFailoverClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"403 quota", NewUpstreamError(403, `{"code":"112"}`), true},
		{"429 rate", NewUpstreamError(429, "usage exceeds frequency limit"), true},
		{"502 transient", NewUpstreamError(502, "provider_error"), true},
		{"400 content policy", NewUpstreamError(400, "InternalError.Algo.DataInspectionFailed"), false},
		{"400 invalid param", NewUpstreamError(400, "invalid_parameter_error: Range of x"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := ShouldFailover(c.err); got != c.want {
			t.Errorf("%s: ShouldFailover=%v want %v", c.name, got, c.want)
		}
	}
}

func TestCooldownForClassification(t *testing.T) {
	if got := CooldownFor(NewUpstreamError(403, "quota")); got != CooldownQuota {
		t.Errorf("403 -> %v", got)
	}
	if got := CooldownFor(NewUpstreamError(429, "rate")); got != CooldownRate {
		t.Errorf("429 -> %v", got)
	}
	if got := CooldownFor(NewUpstreamError(502, "boom")); got != CooldownTransient {
		t.Errorf("502 -> %v", got)
	}
}

func poolBridge(t *testing.T, url string, ids ...string) (*Bridge, *Pool) {
	t.Helper()
	slots := make([]*Slot, 0, len(ids))
	for _, id := range ids {
		slots = append(slots, testSlot(t, id, "type_"+id))
	}
	pool := NewPool(slots...)
	b := NewPoolBridgeFor(account.RegionCN, pool, map[string]interface{}{
		"model_config": map[string]interface{}{"key": "auto"},
		"messages":     []interface{}{},
	})
	b.chatStreamURL = url
	return b, pool
}

// 第一个账号 403（额度）→ 冷却换号 → 第二个账号 200 成功。
func TestCallQoderFailsOverToNextAccount(t *testing.T) {
	hits := 0
	var userTypes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, _ := io.ReadAll(r.Body)
		if plain, err := cosy.Decode(string(raw)); err == nil {
			var m map[string]interface{}
			if json.Unmarshal(plain, &m) == nil {
				if ut, ok := m["aliyun_user_type"].(string); ok {
					userTypes = append(userTypes, ut)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if hits == 1 {
			fmt.Fprint(w, sseEnvelope(403, `{"code":"112","message":"need upgrade"}`))
			return
		}
		fmt.Fprint(w, sseContent("ok"))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	b, pool := poolBridge(t, srv.URL, "a", "b")
	var got []string
	err := b.CallQoderWithOpts(context.Background(), "codex", testMessages(), "auto", nil, CallOpts{},
		func(d Delta) {
			if d.Content != "" {
				got = append(got, d.Content)
			}
		})
	if err != nil {
		t.Fatalf("expected failover success, got %v", err)
	}
	if hits != 2 {
		t.Fatalf("upstream hits = %d, want 2", hits)
	}
	if len(got) != 1 || got[0] != "ok" {
		t.Fatalf("deltas = %v", got)
	}
	if len(userTypes) != 2 || userTypes[0] != "type_a" || userTypes[1] != "type_b" {
		t.Fatalf("expected failover a->b, userTypes=%v", userTypes)
	}
	if _, ok := pool.Pick(map[string]struct{}{"b": {}}); ok {
		t.Fatal("account a should be cooling after 403")
	}
}

// 所有账号都 403 → 每个账号各试一次后返回错误，且全部进入冷却。
func TestCallQoderAllAccountsFail(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseEnvelope(403, `{"code":"112","message":"need upgrade"}`))
	}))
	defer srv.Close()

	b, pool := poolBridge(t, srv.URL, "a", "b")
	err := b.CallQoderWithOpts(context.Background(), "codex", testMessages(), "auto", nil, CallOpts{}, func(Delta) {})
	if err == nil {
		t.Fatal("expected error when all accounts fail")
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (one per account)", hits)
	}
	if _, ok := pool.Pick(nil); ok {
		t.Fatal("both accounts should be cooling")
	}
}

// 内容审核（不可换号）→ 只打一次上游，不换号。
func TestCallQoderContentPolicyNoFailover(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseEnvelope(500, "InternalError.Algo.DataInspectionFailed: bad content"))
	}))
	defer srv.Close()

	b, _ := poolBridge(t, srv.URL, "a", "b")
	err := b.CallQoderWithOpts(context.Background(), "codex", testMessages(), "auto", nil, CallOpts{}, func(Delta) {})
	if err == nil {
		t.Fatal("expected content policy error")
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (no failover on content policy)", hits)
	}
}

func TestNoAccountErrorMapsTo503(t *testing.T) {
	if got := ErrorStatus(ErrNoAccount); got != 503 {
		t.Errorf("ErrorStatus(ErrNoAccount) = %d, want 503", got)
	}
	msg, typ := FriendlyError(ErrNoAccount)
	if typ != ErrTypeNoAccount || msg == "" {
		t.Errorf("FriendlyError(ErrNoAccount) = (%q, %q)", msg, typ)
	}
}

func TestCooldownForRetryAfterSeconds(t *testing.T) {
	detail := `{"code":"403","message":"{\"code\":\"10605\",\"retryAfterSeconds\":30}"}`
	if got := CooldownFor(NewUpstreamError(403, detail)); got != 30*time.Second {
		t.Errorf("retryAfterSeconds=30 -> %v, want 30s", got)
	}
	if got := CooldownFor(NewUpstreamError(403, `{"code":"112"}`)); got != CooldownQuota {
		t.Errorf("plain 403 -> %v, want %v", got, CooldownQuota)
	}
	// 超大值被截到 5 分钟
	big := `{"retryAfterSeconds":99999}`
	if got := CooldownFor(NewUpstreamError(403, big)); got != 5*time.Minute {
		t.Errorf("huge retryAfter -> %v, want 5m", got)
	}
}

// 双桥钉死区域：即使池被误装成跨区账号，CN 桥也永不派 Global 号，反之亦然。
// （跨区派号会扣到用户根本没在跑的那一侧账号的 credits，实测扣过 549。）
func TestPinnedBridgeNeverRoutesAcrossRegions(t *testing.T) {
	cn := testSlot(t, "cn-slot", "t")
	cn.catalog = map[string]bool{"gmodel": true}
	cn.catalogAt = time.Now()
	gl := testSlotFull(t, "global-slot", "t", account.RegionGlobal)
	gl.catalog = map[string]bool{"ultimate": true}
	gl.catalogAt = time.Now()

	mixed := NewPool(cn, gl)
	cnBridge := NewPoolBridgeFor(account.RegionCN, mixed, nil)
	globalBridge := NewPoolBridgeFor(account.RegionGlobal, NewPool(cn, gl), nil)

	// CN 桥：连「只有 Global 有」的模型也只能落在 CN 号上
	for i := 0; i < 4; i++ {
		s, ok := cnBridge.pickSlotForModel("ultimate", nil)
		if !ok {
			t.Fatal("cn bridge should still find a cn slot")
		}
		if s.Region != account.RegionCN {
			t.Fatalf("cn bridge routed to %s (%s)", s.Region, s.ID)
		}
	}
	for i := 0; i < 4; i++ {
		s, ok := globalBridge.pickSlotForModel("gmodel", nil)
		if !ok {
			t.Fatal("global bridge should still find a global slot")
		}
		if s.Region != account.RegionGlobal {
			t.Fatalf("global bridge routed to %s (%s)", s.Region, s.ID)
		}
	}
}

// 同区域内多号时，模型目录用于优先命中真正提供该模型的账号。
func TestPickSlotForModelUsesCatalogWithinRegion(t *testing.T) {
	a := testSlot(t, "a", "t")
	a.catalog = map[string]bool{"gmodel": true}
	a.catalogAt = time.Now()
	b := testSlot(t, "b", "t")
	b.catalog = map[string]bool{"dmodel": true}
	b.catalogAt = time.Now()
	br := NewPoolBridgeFor(account.RegionCN, NewPool(a, b), nil)

	if s, ok := br.pickSlotForModel("dmodel", nil); !ok || s.ID != "b" {
		t.Fatalf("dmodel should pick b, got %v", s)
	}
	if s, ok := br.pickSlotForModel("gmodel", nil); !ok || s.ID != "a" {
		t.Fatalf("gmodel should pick a, got %v", s)
	}
	// 目录都不认识的模型：仍在同区内兜底，不逃逸到别区
	s, ok := br.pickSlotForModel("who-knows", nil)
	if !ok || s.Region != account.RegionCN {
		t.Fatalf("unknown model should fall back within region, got %v", s)
	}
}

// 账号被删除时池要能热退出：Remove 后不再被选中，游标不越界。
func TestPoolRemoveDropsSlot(t *testing.T) {
	a := testSlot(t, "a", "t")
	b := testSlot(t, "b", "t")
	p := NewPool(a, b)
	if !p.Has("a") || !p.Has("b") {
		t.Fatalf("IDs=%v, want both", p.IDs())
	}
	if !p.Remove("a") {
		t.Fatal("Remove(a) should report removal")
	}
	if p.Has("a") || p.Size() != 1 {
		t.Fatalf("after remove: size=%d has(a)=%v", p.Size(), p.Has("a"))
	}
	if p.Remove("a") {
		t.Fatal("removing an absent id should return false")
	}
	for i := 0; i < 3; i++ {
		if s, ok := p.Pick(nil); !ok || s.ID != "b" {
			t.Fatalf("pick after remove = %v (%v)", s, ok)
		}
	}
}
