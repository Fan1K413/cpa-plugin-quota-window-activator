# CPA 通用额度窗口自动激活器

`cpa-plugin-quota-window-activator` 是 CLIProxyAPI（CPA）的后台维护插件。它观察每个 credential 的每个 quota bucket，在旧窗口已经到期、再次读取 quota 后仍确认窗口没有正常滚动时，才向该 credential 发送一次极小的真实模型请求，并再次读取 quota 验证新窗口。

它不是定时 ping 插件。默认配置为 `dry_run: true`。

v0.2.2 修复安装后无法及时激活的问题：首次观测若符合严格 lazy 特征，
会立即进行第二次 quota precheck，不再错误等待完整的 5h/长周期。v0.2.1
已经写入的 `waiting_reset` 状态会原地迁移；持久化的 `NextCheck` 也会在
reset + grace 到点时真正唤醒调度器。

## 工作原理

每个窗口使用 `adapter + AuthIndex + credential fingerprint + BucketID` 独立跟踪：

```text
记录旧窗口 baseline
  → 等待 reset + grace
  → 再读一次 quota（precheck）
  → 已自然滚动：记录 normal_reset，不发送
  → 仍是旧周期或 adapter 给出严格 lazy 证据
  → 持久化 Prepared
  → 原子持久化 Sending + cycle send fence
  → 对具体 credential 发送一次最小请求
  → 等待传播
  → 再读 quota 并逐 bucket 验证
  → confirmed 或 verify-only/retry_wait
```

`Sending` fence 在网络调用之前落盘。从这个时刻起，该旧周期不会再获得第二次发送授权。进程若在发送期间崩溃，重启后把结果视为 unknown，先 verify，绝不立即重发。一个请求可覆盖同一 activation group 内的 5h 与长周期窗口，验证仍按 bucket 分别进行。

持久层使用校验和快照和 fsync 的 write-ahead log。WAL 中序号更高的完整记录可以恢复被截断或尚未替换的快照。进程内控制器还提供 single-flight；同一状态目录只应由一个 CPA 进程使用。

## Provider 支持

| Provider adapter | 观察的数据 | 激活策略 |
|---|---|---|
| Codex / ChatGPT OAuth | `wham/usage` 主 5h、主长周期、code review、additional rate limits | 仅主 5h 与 7–32 天长周期可进入 `activate_if_lazy`；共享 `codex-main` 的窗口合并为一个请求。其他 bucket 不可激活 |
| Antigravity | `retrieveUserQuotaSummary` 中有稳定 `bucketId`、window、remainingFraction、resetTime 的 bucket | `gemini-5h` + `gemini-weekly` 共享 `antigravity-gemini`；`3p-5h` + `3p-weekly` 共享 `antigravity-third-party`。每个 group 在过期 cycle 内最多发送一个绑定 credential 的请求；未知 bucket 不可激活 |
| Claude OAuth | 不轮询 | 暂不支持；没有证据表明需要请求才能滚动窗口 |
| Gemini CLI OAuth | 不轮询 | 暂不支持；model/token 行不被假设为独立可激活池 |
| Kimi | 不轮询 | 暂不支持；尚无已验证的 lazy-reset 激活约定 |
| Grok / xAI | 不轮询 | 暂不支持；不会调用付费 health 请求 |
| 未知 provider | 不轮询 | unsupported；记录一次日志，不构造未知请求 |

运行时只注册能够绑定 credential 的 activation adapter。不支持的 provider 只记录一次日志，不产生后台 quota 请求。Antigravity 只有上述四个标准 bucket ID 能通过 `CanActivate`；同一 quota 响应中新出现或 model-specific 的 bucket 在共享关系与 reset 行为明确前不会激活。

## Credential 绑定与最小请求

两个 activation adapter 都绑定从 `host.auth.get` 读取的目标 AuthIndex，直接通过 CPA `host.http.do` 调用 provider endpoint。它们不经过 CPA 普通 `/v1/chat/completions`，不会让 scheduler 再选择账号。

Codex 使用参考插件已经验证的 compact 请求结构：`gpt-5.4-mini`、空 instructions 和一个 `ping` 输入，不启用 streaming 或 tools。Antigravity 复用 CPA 的 `v1internal:generateContent` 请求封装：Gemini 组使用 `gemini-3.1-flash-lite`，Claude/GPT 组使用非 thinking 的 `claude-sonnet-4-6`；请求只包含 `ping`，候选数为 1、temperature 为 0、输出限制为 1 token。

只有以下条件全部满足时才可能发送：

- credential 未被 CPA 或插件禁用；
- credential 具有稳定 ChatGPT account ID；
- 已有先前持久化 baseline，或首次观测本身符合严格 lazy/已过期特征；
- 普通 baseline 的 reset + grace 已过去；首次严格 lazy 候选则立即进入第二次 precheck；
- precheck 仍指向同一旧 reset，或符合 Codex 的严格 lazy 特征；
- 当前 cycle 没有 durable send fence；
- provider 已启用自动激活；
- `dry_run` 为 false；
- activation plan 有明确的小输入和输出预算。

## 安装与构建

要求 Go 1.26+、CGO 和 C 编译器。运行：

```bash
go test ./...
make build
```

Windows 可运行：

```powershell
.\build.ps1
```

仓库包含 `.github/workflows/build.yml`。推送、Pull Request 或在 Actions 页面手动运行 **Build** 后，会先执行 `go vet` 和完整测试，再生成以下 artifacts：

- `quota-window-activator_linux_amd64`
- `quota-window-activator_linux_arm64`
- `quota-window-activator_darwin_amd64`
- `quota-window-activator_darwin_arm64`
- `quota-window-activator_windows_amd64`

GitHub 会把每个 artifact 自动打成下载 ZIP；内部包含对应动态库、README、License 和示例配置。该 workflow 只构建 artifact，不自动创建 Release。

把生成的动态库放在 CPA 的 `plugins/<GOOS>/<GOARCH>/` 或 `plugins/` 目录。文件名必须是：

- Windows：`quota-window-activator.dll`
- Linux/FreeBSD：`quota-window-activator.so`
- macOS：`quota-window-activator.dylib`

动态库 basename 是插件 ID，因此配置键必须为 `quota-window-activator`。

## 配置

建议先保持 dry-run 至少跨过一个真实 reset，核对状态页与日志后再开启发送：

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    quota-window-activator:
      enabled: true
      priority: 10

      dry_run: true
      observation_interval: 30m
      reset_grace_period: 60s
      verify_delay: 3s
      suppression_duration: 10m
      request_timeout: 30s
      max_retries: 5
      max_concurrency: 2

      providers:
        codex:
          enabled: true
        antigravity:
          enabled: true

      disabled_credentials:
        # 使用 host.auth.list 返回的 auth ID
        # - account-to-disable
```

`max_retries` 只约束控制面重读/回退策略，不会授权同一周期再次发送 activation。安全规则始终是每个 credential + bucket + baseline cycle 最多一次 send fence。

状态默认写入操作系统用户配置目录下的 `CLIProxyAPI/quota-window-activator/runtime-state.json` 和 `.wal`。可通过 `state_dir` 指定独占目录。状态文件含 AuthID、quota 数值与操作状态，不含 token 或完整 credential JSON。

## 状态页和日志

浏览器资源：

```text
/v0/resource/plugins/quota-window-activator/status
```

受 CPA Management key 保护的 JSON：

```text
GET /v0/management/plugins/quota-window-activator/status
```

浏览器资源是“配置 + 状态”一体面板。输入 CPA Management key 后，可以通过
CPA 自带的插件配置 API 读取和保存全部参数；页面会保留未知的顶层字段和
provider 字段，方便后续版本扩展。Management key 只存在当前页面内存中，
不会写入插件状态或浏览器存储。通过面板关闭 dry-run 时还会要求再次确认。

状态包括 provider、credential、bucket、used、reset、baseline、probe state、last probe、next check、last activation 和 result。典型状态为：

```text
waiting_reset → pending_precheck → lazy_reset_detected
→ activation_sending → activation_sent → confirmed
```

正常自动滚动显示 `normal_reset`。超时或发送时崩溃显示 `sent_unknown` 并只进行验证。日志以 `[quota-activator]` 开头，不记录 access token、refresh token、Authorization header 或完整上游响应 body。

## CPA v7.2.154 的能力边界

该版本足以枚举文件型 credential、按 AuthIndex 读取 JSON，并通过 Host HTTP 发出绑定了该 credential header 的请求，因此不会经过普通 scheduler 误选账号。

但 v7.2.154 的 `host.model.execute` 不能指定 AuthID，`host.http.do` 也没有 credential generation/admission 参数，不会自动继承 credential 自己的 `proxy_url`，且 context timeout 只能让插件停止等待，不能证明 upstream 请求已经取消。因此插件采取以下 fail-closed 行为：

- runtime-only credential 不访问；
- 配置了 per-credential proxy 的 credential 不访问，状态标为 `auth_blocked`；
- timeout 一律视为 send result unknown，写入 suppression 并 verify-first；
- 不调用 CPA scheduler、不改 priority、不改 routing；
- token refresh 依赖 CPA 已经持久化的最新 auth JSON，插件自身不刷新或保存 token。

若后续 CPA 增加“按 AuthIndex 执行 provider request”、继承 per-auth transport、可取消 HTTP 和原子 credential generation admission，应优先迁移到该 Host API。

## 风险与上线步骤

Codex compact endpoint、Antigravity internal endpoint、模型 ID 和 quota schema 都属于 provider 私有接口，可能随服务端变化。默认仍为 dry-run，开发时没有对真实账号发送验收请求。旧配置若明确写了 `mode: observe`，现在会被视为禁用且不读取 quota；在面板中勾选对应 provider 才会加入自动激活。推荐：

1. 在测试账号上运行 dry-run，至少观察一个 reset；
2. 确认自动滚动账号始终是 `normal_reset`；
3. 只对确认存在 lazy reset 的 Codex 或 Antigravity credential 关闭 dry-run；
4. 检查一次 activation 后是否进入 `confirmed`，并确认同 cycle 没有第二次发送；
5. 如出现 401、403、未知 quota schema 或 proxy block，保持 dry-run 或禁用 provider，并升级 adapter，不要改成定时 ping。

## 兼容性

- 设计、编译和测试基线：CLIProxyAPI v7.2.154，plugin ABI 1，schema 5。
- 尽量兼容保持相同 ABI、`host.auth.list/get`、`host.http.do` 与 Management API schema 的后续 7.2.x。
- Provider 私有 quota schema 不在 CPA ABI 兼容承诺内；解析器遇到不完整窗口或不稳定 bucket ID 时拒绝激活。

## 致谢

状态机与崩溃恢复原则重点参考了 [cpa-plugin-codex-quota-scheduler](https://github.com/JefferyZhang2019/cpa-plugin-codex-quota-scheduler) 的源码实现，包括 reset baseline、lazy reset 判定、precheck、ProbeAttempt、send fence、suppression 与 verify-first 恢复。该项目采用 MIT License。

英文说明见 [README.md](README.md)。
