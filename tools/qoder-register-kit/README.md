# qoder-register-kit — Qoder 注册链路研究工具包

> 从 2026-10-20 的注册链路逆向中沉淀出的可复用工具。
> **仅用于协议研究**，不建议用于生产环境的批量注册。

---

## 目录

```
tools/qoder-register-kit/
├── cdp.js                  # CDP 驱动（人类节奏、拟人鼠标轨迹、逐键输入）
├── incog.js                # 无痕 browserContext 管理（隔离登录态，避免污染）
├── mailbox.py              # Cloudflare temp-email worker 客户端
├── detect_gap.py           # 滑块缺口检测（模板匹配，PIL）
└── flows/                  # 流程脚本（按执行顺序）
    ├── page-submit.js      # Step1：填姓名/邮箱 → 提交（antd Form 绕过）
    ├── step2-submit.js     # Step2：填密码 → 提交 → 触发滑块
    ├── probe-captcha.js    # 观察 AliyunCaptcha 滑块 DOM 结构
    ├── solve-slider6.js    # 滑块闭环求解（缺口检测 + 自适应拖拽）
    ├── type-code.js        # 填邮箱验证码（6 位）
    ├── watch-baxia2.js     # 监听 baxia 弹窗出现
    ├── analyze-baxia.js    # 只读分析 baxia iframe / canvas
    ├── capture-success.js  # 全量网络监听（请求+响应+bx-ua）存档
    └── net-observe.js      # 单次请求监听（定位提交接口）
```

## 依赖

```bash
# Node 18+（原生 WebSocket），另需 ws 包
cd tools/qoder-register-kit && npm i ws

# Python
pip install pillow pytesseract   # detect_gap.py 只需 pillow
```

## 前置：启动带调试端口的 Chrome

```bash
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome \
  --remote-debugging-port=9366 \
  --user-data-dir="$HOME/.chrome-cdp-profile"
```

> Chrome 90+ 必须用独立 `--user-data-dir`，否则调试端口不生效。

## 核心用法

### 1. 无痕上下文（强烈推荐）

```bash
node incog.js            # 输出 browserContextId / targetId / sessionId → .incog.json
```

**为什么必须用无痕**：已登录的 Chrome profile 打开 `users/sign-up` 会被重定向到
`/account/profile`；且失败尝试会污染上下文，导致后续表单提交静默失效
（必须新建 browserContext 重试）。

### 2. 走完注册流程到滑块

```bash
node flows/page-submit.js    # Step1 → 密码步
node flows/step2-submit.js   # Step2 → 滑块弹出
```

> 两个脚本顶部的 `PAGE_WS` 需替换为 `incog.js` 输出的 targetId。

### 3. 滑块求解

```bash
node flows/solve-slider6.js
```

原理：读取 `#aliyunCaptcha-img` / `#aliyunCaptcha-puzzle` → 下载 → Python 模板匹配
→ 计算拖拽距离 → CDP 拟人拖拽（闭环自适应实测比例）。

**已知限制**：位置算准也可能被拒（行为评分），成功率不稳定。

### 4. baxia 只读分析

```bash
node flows/watch-baxia2.js    # 等弹窗出现
node flows/analyze-baxia.js   # 导出题目图 + 9 张网格图到 artifacts/
```

## ★ 关键经验（踩过的坑）

| # | 坑 | 解法 |
|---|---|---|
| 1 | **antd Form 无法用 DOM 事件注入** | 从 `__reactFiber$` 向上找 Form 实例，调 `setFieldsValue` |
| 2 | **注册接口是 `/api/v1/users`** | 不是 `/users/sign-up`（前端路由）|
| 3 | **CSRF 用 `Sec-Fetch-Site` 校验** | curl 需带 `Sec-Fetch-Site: same-origin` |
| 4 | **无痕上下文会被污染** | 每次失败后新建 browserContext |
| 5 | **AliyunCaptcha 只认 JS `.click()`** | CDP 鼠标事件无法触发滑块 |
| 6 | **baxia iframe 同源可访问** | 无需逆向 WASM，`getImageData` 直读 canvas |
| 7 | **题目文字是 anti-OCR 像素图** | tesseract 全败，需多模态视觉模型 |
| 8 | **网络分流** | qoder.com 需直连；邮件 API 需代理（视环境）|

## 协助者注意事项

1. **合规边界**
   - 本工具包面向**协议研究与学习**；请勿用于批量注册、薅羊毛、绕过风控等用途。
   - 请遵守目标站点的服务条款与当地法律法规。
2. **不要提交凭证**
   - `mailbox.py` 中的 admin passkey、账号密码、`bx-ua` 指纹等**不得写入仓库**。
   - 抓包存档（`artifacts/*.jsonl`）提交前请脱敏。
3. **人类节奏**
   - `cdp.js` 内置了随机延迟与拟人轨迹，请勿移除——高频请求会触发风控并影响他人研究。
4. **可回滚**
   - 所有实验仅在浏览器无痕上下文内进行，不改动系统配置与硬件信息。
5. **贡献方向**
   - 多模态识别（题目文字 + 网格图分类）
   - `bx-ua` 指纹生成算法
   - `program.wasm` 导出函数 `f/g/h/i/j/l/m/n` 的语义
   - 设备 ↔ 账号绑定接口的参数细节
