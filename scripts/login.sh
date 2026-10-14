#!/usr/bin/env bash
# qoder2api-login — 一条命令走完某个区域的 OAuth 登录向导
#
#   qoder2api-login cn        # 登国内版（落 8963 那条桥）
#   qoder2api-login global    # 登国际版（落 8964 那条桥）
#
# 它只做 HTTP 调用：登录控制台 → 起设备授权流 → 打印授权链接 → 等你在浏览器点确认
# → 账号落盘并在该区域激活（该区域端口若之前因为没账号而没开，会自动上线）。
# 不会替你打开浏览器、也不碰你的桌面/键盘。
#
# 环境变量：
#   QODER2API_CONSOLE_PASSWORD  控制台密码（不设置则交互输入，不回显）
#   QODER2API_CONSOLE_PORT      控制台端口，默认 3588
set -euo pipefail

region="${1:-cn}"
port="${QODER2API_CONSOLE_PORT:-${2:-3588}}"
base="http://127.0.0.1:${port}"

case "$region" in
	cn|global) ;;
	*) echo "region 只能是 cn 或 global（收到：$region）" >&2; exit 2 ;;
esac
command -v curl >/dev/null || { echo "需要 curl" >&2; exit 1; }
command -v python3 >/dev/null || { echo "需要 python3（用于 JSON 编解码）" >&2; exit 1; }

json_field() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get(sys.argv[1],""))' "$1"; }
json_str() { python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"; }

pw="${QODER2API_CONSOLE_PASSWORD:-}"
if [ -z "$pw" ]; then
	read -rsp "控制台密码 (${base}) : " pw; echo
fi

tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT
ck="$tmp/ck"

if ! curl -sf -c "$ck" -X POST "$base/api/auth/login" \
	-H 'Content-Type: application/json' \
	-d "{\"password\":$(json_str "$pw")}" -o /dev/null; then
	echo "控制台登录失败：确认服务在跑（rc-service qoder2api status）且密码正确" >&2
	exit 1
fi

start="$(curl -sf -b "$ck" -X POST "$base/api/oauth/start" \
	-H 'Content-Type: application/json' \
	-d "{\"region\":$(json_str "$region")}")" || {
	echo "起 OAuth 流失败（服务没在跑？该区域端点不可达？）" >&2; exit 1; }

login_id="$(printf '%s' "$start" | json_field login_id)"
url="$(printf '%s' "$start" | json_field login_url)"
if [ -z "$login_id" ] || [ -z "$url" ]; then
	echo "响应缺少 login_id/login_url：$start" >&2; exit 1
fi

echo
echo "① 用浏览器打开这个链接，登录并授权【${region}】账号："
echo "     ${url}"
echo "② 授权完成后这里会自动继续（默认最多等 10 分钟，"
echo "   服务端 QODER2API_OAUTH_TIMEOUT 可调）..."
echo

acct="$(curl -sf -b "$ck" -X POST "$base/api/oauth/wait" \
	-H 'Content-Type: application/json' \
	-d "{\"login_id\":$(json_str "$login_id"),\"active\":true}")" || {
	echo "等待授权失败或超时（也可能是该区域端点不可达）" >&2; exit 1; }

echo "③ 已保存并激活："
printf '%s\n' "$acct" | python3 -m json.tool

echo
echo "④ 双桥当前状态："
curl -sf -b "$ck" "$base/api/status" | python3 -m json.tool
echo
echo "接入地址：cn → http://127.0.0.1:8963/v1 ，global → http://127.0.0.1:8964/v1（两区 API Key 各自独立）"
