#!/usr/bin/env bash
# qoder2api-login — 一条命令走完 OAuth 登录向导
#
#   qoder2api-login both      # 国内版 + 国际版【同时】挂两条授权流，两个链接一起给你
#   qoder2api-login cn        # 只登国内版（落 8963 那条桥）
#   qoder2api-login global    # 只登国际版（落 8964 那条桥）
#
# 只做 HTTP 调用：登录控制台 → 起设备授权流 → 打印授权链接 → 等你在浏览器点确认
# → 账号落盘并在该区域激活（该区域端口若之前因没账号而没开，会自动上线）。
# 不会替你打开浏览器、也不碰你的桌面/键盘。
#
# 环境变量：
#   QODER2API_CONSOLE_PASSWORD  控制台密码（不设置则交互输入，不回显）
#   QODER2API_CONSOLE_PORT      控制台端口，默认 3588
set -uo pipefail

which="${1:-both}"
port="${QODER2API_CONSOLE_PORT:-${2:-3588}}"
base="http://127.0.0.1:${port}"

case "$which" in
	both) regions=(cn global) ;;
	cn) regions=(cn) ;;
	global) regions=(global) ;;
	*) echo "用法: qoder2api-login [both|cn|global]" >&2; exit 2 ;;
esac
command -v curl >/dev/null || { echo "需要 curl" >&2; exit 1; }
command -v python3 >/dev/null || { echo "需要 python3（用于 JSON 编解码）" >&2; exit 1; }

# JSON 一律交给 python3 生成，避免在 bash 里手工转义引号
json_str() { python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"; }
json_field() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get(sys.argv[1],""))' "$1"; }
body_kv() { python3 -c 'import json,sys; print(json.dumps({sys.argv[1]: sys.argv[2]}))' "$1" "$2"; }
body_login_wait() { python3 -c 'import json,sys; print(json.dumps({"login_id": sys.argv[1], "active": True}))' "$1"; }

pw="${QODER2API_CONSOLE_PASSWORD:-}"
pw_src="环境变量 QODER2API_CONSOLE_PASSWORD"
if [ -z "$pw" ]; then
	# 服务与向导跑在同一个用户下：直接从 settings.json 取密码，不用 sudo 也不用手输
	for home in "${QODER2API_HOME:-}" /var/lib/qoder2api "$HOME/.qoder2api"; do
		[ -n "$home" ] && [ -r "$home/settings.json" ] || continue
		got="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("console_password",""))' "$home/settings.json" 2>/dev/null)"
		if [ -n "$got" ]; then
			pw="$got"; pw_src="$home/settings.json"; break
		fi
	done
fi
if [ -z "$pw" ]; then
	read -rsp "控制台密码 (${base}) : " pw; echo
	pw_src="你刚才输入的"
fi

tmp="$(mktemp -d)"
ck="$tmp/ck"
ids=()

# 中途 Ctrl-C 时把还挂着的授权流取消掉，别在服务端留僵尸会话
cleanup() {
	for id in "${ids[@]:-}"; do
		[ -n "$id" ] || continue
		curl -s -m 3 -b "$ck" -X POST "$base/api/oauth/cancel" \
			-H 'Content-Type: application/json' \
			-d "$(body_kv login_id "$id")" >/dev/null 2>&1
	done
	rm -rf "$tmp"
}
trap cleanup EXIT

if ! curl -sf -c "$ck" -X POST "$base/api/auth/login" \
	-H 'Content-Type: application/json' \
	-d "$(body_kv password "$pw")" -o /dev/null; then
	echo "控制台登录失败（密码来源：${pw_src}）。检查：sudo rc-service qoder2api status；" >&2
	echo "或改密码：控制台设置里重填，或直接改 ${QODER2API_HOME:-/var/lib/qoder2api}/settings.json 的 console_password" >&2
	exit 1
fi

urls=()
for r in "${regions[@]}"; do
	start="$(curl -sf -b "$ck" -X POST "$base/api/oauth/start" \
		-H 'Content-Type: application/json' \
		-d "$(body_kv region "$r")")" || {
		echo "起 $r 的 OAuth 流失败（服务没在跑？）" >&2; exit 1; }
	id="$(printf '%s' "$start" | json_field login_id)"
	url="$(printf '%s' "$start" | json_field login_url)"
	if [ -z "$id" ] || [ -z "$url" ]; then
		echo "$r 的响应缺少 login_id/login_url：$start" >&2; exit 1
	fi
	ids+=("$id")
	urls+=("$r=$url")
done

echo
echo "① 下面的链接逐个用【对应区域】的账号打开并授权（两条流同时挂着，先后顺序随意）："
for entry in "${urls[@]}"; do
	echo "     ${entry%%=*} → ${entry#*=}"
done
echo
echo "② 这里在等授权结果（默认最多 10 分钟，服务端 QODER2API_OAUTH_TIMEOUT 可调）..."
echo

fail=0
for i in "${!ids[@]}"; do
	r="${regions[$i]}"
	id="${ids[$i]}"
	[ -n "$id" ] || continue
	echo "── 等待 $r ..."
	acct="$(curl -sf -b "$ck" -X POST "$base/api/oauth/wait" \
		-H 'Content-Type: application/json' \
		-d "$(body_login_wait "$id")")"
	rc=$?
	if [ "$rc" -ne 0 ] || [ -z "$acct" ]; then
		echo "   $r 授权失败或超时（rc=$rc）" >&2
		fail=1
		continue
	fi
	echo "   已保存并激活："
	printf '%s\n' "$acct" | python3 -c '
import json, sys
d = json.load(sys.stdin)
print("     %s · %s · plan=%s · region=%s · id=%s" % (
    d.get("name"), d.get("email"), d.get("plan"), d.get("region"), d.get("id")))
' || printf '%s\n' "$acct"
	unset "ids[$i]"   # 这条已完成，退出时不必再取消
done

echo
echo "③ 双桥当前状态："
curl -sf -b "$ck" "$base/api/status" | python3 -c '
import json, sys
for b in json.load(sys.stdin)["bridges"]:
    print("   %-16s :%-6s %s 池 %s/%s 激活 %s%s" % (
        b["region"], b["port"], "ON " if b["running"] else "OFF",
        b.get("pool_size", 0), b.get("accounts_total", 0),
        b.get("active_account") or "-",
        "" if b["running"] else "  ← " + (b.get("reason") or "")))
'

if [ "$fail" -ne 0 ]; then
	echo
	echo "有区域没登成功。接入地址：cn → http://127.0.0.1:8963/v1 ，global → http://127.0.0.1:8964/v1" >&2
	exit 1
fi
echo
echo "完成：两区各一条桥、各一把 API Key（/var/lib/qoder2api/settings.json 的 cn_token / global_token）"
