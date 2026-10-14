package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"

	"qoder2api/account"
	"qoder2api/internal/bridge"
	"qoder2api/internal/cosy"
)

// checkin 是排查签到/活动接口用的调试工具：对指定账号所在区域的活动端点逐个 GET，
// 打印原始响应。它不参与服务运行，因此失败也不做优雅处理，只求看得清楚。
func main() {
	dataDir := flag.String("data-dir", "", "data root (default: $QODER2API_HOME or ~/.qoder2api)")
	accountID := flag.String("account", "", "账号 ID（默认取列表里第一个）")
	flag.Parse()

	// 单进程双桥后两区账号同在 ~/.qoder2api，按 region 分流；
	// 这里不再写死 .qoder2api-cn，否则国际号根本读不到。
	if *dataDir != "" {
		account.SetDataRoot(*dataDir)
	}

	// 与主程序共享 settings.json 的指纹盐，保证同一部署内指纹一致
	if salt, err := account.EnsureMachineSalt(); err == nil {
		cosy.SetInstallSalt(salt)
	} else {
		// 调试工具保持可运行（不改变退出行为），仅向 stderr 告警降级后果
		fmt.Fprintln(os.Stderr, "[checkin] init machine salt failed:", err,
			"；运行于无盐模式（hub 兼容指纹），下次启动恢复加盐将导致全部账号指纹漂移")
	}

	accounts, _ := account.List()
	if len(accounts) == 0 {
		log.Fatalf("no accounts under %s (登录后再跑，或用 --data-dir 指定数据目录)", account.DataRoot())
	}
	acct := accounts[0]
	if want := *accountID; want != "" {
		found, err := account.Get(want)
		if err != nil {
			log.Fatalf("account %s: %v", want, err)
		}
		acct = *found
	}
	token, err := account.GetSecret(acct.ID)
	if err != nil {
		log.Fatalf("secret for %s: %v", acct.ID, err)
	}
	deviceToken, refreshToken := bridge.ParseOAuthSecret(token)
	if deviceToken == "" {
		deviceToken = token
	}

	ep := account.GetEndpoints(acct.Region.Normalized())
	fmt.Printf("Account: %s (id=%s region=%s)\n\n", acct.Name, acct.ID, acct.Region)

	userInfo, err := bridge.FetchUserInfoWithToken(deviceToken, acct.Region)
	if err != nil {
		log.Fatalf("userinfo: %v", err)
	}
	uid := bridge.ResolveOAuthUserID(userInfo)
	name := bridge.StrVal(userInfo, "name")

	// 稳定指纹：uid 已知，按 uid 派生（与 bridge 会话同一套机器码）
	seed := cosy.FingerprintSeed(uid, deviceToken)
	mid := cosy.DeriveMachineID(seed)
	mtoken := cosy.DeriveMachineToken(seed)
	mtype := cosy.DeriveMachineType(seed)
	identity := cosy.AuthIdentity{
		Name:               name,
		Aid:                uid,
		Uid:                uid,
		OrganizationId:     bridge.StrVal(userInfo, "organization_id"),
		OrganizationName:   bridge.StrVal(userInfo, "organization_name"),
		UserType:           bridge.StrValDefault(userInfo, "userType", "personal_standard"),
		SecurityOauthToken: deviceToken,
		RefreshToken:       refreshToken,
	}
	sess, err := cosy.NewSession(identity, mid, mtoken, mtype)
	if err != nil {
		log.Fatalf("session: %v", err)
	}
	client := bridge.NewBearerClient(sess)

	fmt.Printf("User: %s\n\n", name)

	// 两区主机不同（国内 gateway.qoder.com.cn / 国际 api2.qoder.sh 等），
	// 从该区域的端点配置里取主机，不再写死国内域名。
	hosts := []string{"https://" + ep.OpenAPIHost}
	if u, err := url.Parse(ep.ModelListURL); err == nil {
		hosts = append([]string{u.Scheme + "://" + u.Host}, hosts...)
	}
	paths := []string{
		"/algo/api/v2/activity",
		"/algo/api/v2/activity/claim/eligibility",
		"/algo/api/v2/activity/quota/reset/latest",
		"/api/v2/activity",
		"/api/v2/activity/claim/eligibility",
		"/api/v2/user/plan",
	}

	for _, h := range hosts {
		for _, p := range paths {
			full := h + p
			fmt.Printf("=== GET %s ===\n", full)
			result, err := client.CallGetForTest(full)
			if err != nil {
				fmt.Printf("  error: %v\n\n", err)
				continue
			}
			pretty, _ := json.MarshalIndent(result, "  ", "  ")
			fmt.Printf("  %s\n\n", string(pretty))
		}
	}
}
