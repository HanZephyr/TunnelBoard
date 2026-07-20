# Caddy 多路由复用失效诊断（443 端口被自身占用）

- 日期：2026-07-20
- 类型：问题诊断与修复建议（不包含代码改动）
- 关键词：Web Route、Caddy、443 端口冲突、热重载

## 1. 现象

Web 路由列表中，只能有一条路由的“经 Caddy 提供服务”选项真正生效。

复现步骤：

1. 创建并应用一条启用 Caddy 的 Web Route（例如 `a.test`）—— Caddy 正常启动，HTTPS 访问可用。
2. 再创建/编辑第二条 Web Route（例如 `b.test`），勾选“经 Caddy 提供服务”并应用。
3. 第二条路由不生效：界面弹出 tips：
   > 443 被占用，Caddy 未启动，可继续使用 域名+端口 访问。
4. 应用日志同时输出：
   > `time=... level=WARN msg="caddy port conflict, route stays hosts-only" err="caddy: 127.0.0.1:443 unavailable: listen tcp 127.0.0.1:443: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted."`

## 2. 预期行为

按 ADR 0002（本地域名路由与本地 CA）与产品设定：

- 没有任何路由启用 Caddy 接管时：停止 Caddy 进程，撤销本地 CA 信任。
- 任意一条路由启用 Caddy 接管时：启动 Caddy 进程，注册本地 CA 信任。
- 后续再有其他路由启用 Caddy：复用已经在运行的 Caddy 进程，通过 admin API 热重载新配置，**不应再次尝试冷启动**，更不应因 443 被占用而失败。

## 3. 根因定位

### 3.1 端口预检的实现

`internal/caddy/adapter.go` 中 `DiagnosePort` 通过 `net.Listen("tcp", "127.0.0.1:443")` 实测端口可绑定：

```go
func (a *Adapter) DiagnosePort() error {
    return CheckAddr("127.0.0.1:443")
}

func CheckAddr(addr string) error {
    ln, err := net.Listen("tcp", addr)
    if err != nil {
        return fmt.Errorf("caddy: %s unavailable: %w", addr, err)
    }
    return ln.Close()
}
```

这个预检的语义是“**443 当前是否空闲到可以让 Caddy 去绑定**”。它只在 Caddy 还没启动时有意义。

### 3.2 `applySystem` 的端口预检没有按运行态分支

`internal/biz/router.go` 的 `applySystem` 是所有路由变更统一的系统重推流程，关键片段：

```go
prevRunning := b.caddy.Running()
...
if len(caddyConfig) == 0 {
    // 无启用路由：停 Caddy、撤 CA
    ...
    return result, nil
}

if err := b.caddy.DiagnosePort(); err != nil {
    // 443 冲突：不启动 Caddy，保留 hosts-only 访问；非致命。
    slog.Warn("caddy port conflict, route stays hosts-only", "err", err)
    result.PortConflict = err.Error()
    return result, nil
}
prevConfig, _ := os.ReadFile(b.caddyConfigPth)
if err := b.caddy.Reload(caddyConfig); err != nil {
    ...
}
```

问题在于：`DiagnosePort` 这一步是**无条件执行**的，即使 `prevRunning == true`（Caddy 已经在跑）也会调用。但 Caddy 自己就监听着 443，再去 `net.Listen("127.0.0.1:443")` 必然报 `bind: Only one usage of each socket address ...`。于是流程提前 return，`Reload` 永远不会被调用，新增路由的 Caddy 配置永远写不进去。

### 3.3 同文件其他位置都已经做对了守卫

- `ResumeCaddy`：仅在 `!b.caddy.Running()` 分支内调用 `DiagnosePort`。
- `RouteStatus`：`portConflict := b.caddy.DiagnosePort() != nil && !running`。
- `PreviewRoute`：`if !b.caddy.Running() && b.caddy.DiagnosePort() != nil`。

也就是说，`applySystem` 是唯一一处漏掉 `!prevRunning` 守卫的地方，与项目其他位置的处理方式自相矛盾。这是一处一致性缺陷，而非设计上的歧义。

### 3.4 Caddy Adapter 自身已经能正确处理两种情况

`internal/caddy/adapter.go` 的 `Reload`：

```go
func (a *Adapter) Reload(config []byte) error {
    if err := a.writeConfigAtomic(config); err != nil {
        return err
    }
    if !a.Running() {
        return a.Start()
    }
    // 已运行：经 admin API /load 热重载，不会重新 bind 443
    resp, err := a.httpClient.Post(a.AdminURL+"/load", ...)
    ...
}
```

冷启动（`Start`）和热重载（admin API `/load`）已经在 `Reload` 内部按运行态分派。换句话说，业务层根本不需要在 Caddy 已运行时再做一次端口预检 —— 那次预检既无用又会误报。

### 3.5 现有单测为什么没拦住

`internal/biz/router_test.go` 中的 `fakeCaddyAdapter` 是一个无状态 fake：

- `DiagnosePort()` 永远返回 `f.diagnoseErr`，与 `Running()` 之间没有任何联动；
- `Reload()` 成功后会把 `f.running` 置为 `true`，但 `DiagnosePort` 的返回值不会因此改变。

所以“第一条路由启用 Caddy 后，第二条路由启用 Caddy”这个场景在测试里跑出来的 `DiagnosePort` 结果与生产完全不同：测试里仍是 `nil`（除非显式注入错误），生产里则是“443 已被自身占用”的硬错误。`TestApplyRouteCaddyTrustFlow` 只覆盖了从冷态启用的路径，没有覆盖“Caddy 已运行 + 再加一条 Caddy 路由”的路径，所以缺陷没被发现。

## 4. 推荐修复方案

### 4.1 最小修复：给 `applySystem` 的端口预检加上 `!prevRunning` 守卫

把 `applySystem` 中的端口预检改成只在 Caddy 未运行时执行：

```go
// 仅在 Caddy 未运行时预检 443；已运行时走 admin API 热重载，不会重新 bind。
if !prevRunning {
    if err := b.caddy.DiagnosePort(); err != nil {
        slog.Warn("caddy port conflict, route stays hosts-only", "err", err)
        result.PortConflict = err.Error()
        return result, nil
    }
}
prevConfig, _ := os.ReadFile(b.caddyConfigPth)
if err := b.caddy.Reload(caddyConfig); err != nil {
    b.rollbackHosts(prevEntries, hostsChanged)
    slog.Error("reload caddy failed", "err", err)
    return result, fmt.Errorf("reload caddy: %w", err)
}
```

这样：

- Caddy 未运行：先预检 443，再 `Reload` → 冷启动。冲突时仍按 hosts-only 降级，行为不变。
- Caddy 已运行：跳过预检，直接 `Reload` → 走 admin API 热重载，把新增路由合并进现有配置，443 由原进程继续持有，不会触发任何 bind。

这一改与 `ResumeCaddy`/`RouteStatus`/`PreviewRoute` 三处的现有守卫保持一致。

### 4.2 建议补充的回归测试

在 `internal/biz/router_test.go` 中新增一个测试，专门覆盖“Caddy 已运行 + 再应用一条 Caddy 路由”的场景：

- 初始状态：`fakeCaddyAdapter.running = true`，CA 已信任。
- 操作：再保存并应用一条 Caddy 启用的 Web Route。
- 断言：
  - `result.PortConflict == ""`；
  - `result.CaddyApplied == true`；
  - `len(fx.caddy.reloads) == 1` 且收到的配置包含两条路由的 host matcher；
  - 没有 `Stop` 调用。

为了让 fake 能真实反映生产行为，可以扩展 `fakeCaddyAdapter`，让 `DiagnosePort()` 在 `running == true` 时返回“端口被占用”错误（模拟 Caddy 自身占着 443），用以证明修复后流程不再被这个错误打断。

### 4.3 可选的更稳健改造（非必需）

若希望从源头消除“`DiagnosePort` 在 Caddy 已运行时的歧义”，可以在 `Adapter` 层把 `DiagnosePort` 改成 `portFreeForColdStart` 之类只在 `Running()==false` 时才被允许调用的语义；或者在 `DiagnosePort` 内部直接 `if a.Running() { return nil }` 短路。但这会改变 Adapter 接口语义，影响面比 4.1 大，建议先按 4.1 修复并补测试，再视情况考虑接口层改造。

## 5. 影响面与风险评估

- 修复点单一、改动量极小，且与同文件其他三处守卫方式完全一致，回归风险低。
- 不触及 hosts、CA 信任、特权 helper 任何路径，仅修正 Caddy 已运行分支的判断逻辑。
- 修复后，原先因“自身占用 443”误判为冲突的场景会正确走热重载，新增的 Caddy 路由立即生效；原先真正的外部 443 冲突（Caddy 未运行、但别的进程占着 443）仍然会被 `DiagnosePort` 拦下，hosts-only 降级行为不变。

## 6. 相关文件

- `internal/biz/router.go` —— `applySystem` 中端口预检缺守卫的位置；同文件 `ResumeCaddy`/`RouteStatus`/`PreviewRoute` 已有正确守卫，可作对照。
- `internal/caddy/adapter.go` —— `DiagnosePort`/`CheckAddr` 实现，以及 `Reload` 内部按 `Running()` 分派冷启动与热重载的逻辑。
- `internal/biz/router_test.go` —— 现有测试覆盖范围与缺口。
- `docs/adr/0002-local-domain-routing-and-tls.md` —— 本地域名路由与本地 CA 的设计依据。
