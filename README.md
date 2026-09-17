# CPA Quota Window Activator

`cpa-plugin-quota-window-activator` is a background maintenance plugin for CLIProxyAPI (CPA). It tracks each quota bucket of each credential, waits for the old window to expire, performs an authoritative quota precheck, and sends one tiny credential-bound model request only when the window still appears lazy. It then reads quota again and verifies every affected bucket.

This is not a periodic ping plugin. The default is `dry_run: true`.

Version 0.2.2 fixes activation after installation: a strict first-observation
lazy window now receives an immediate second quota precheck instead of waiting
one full 5-hour or long window. State written by 0.2.1 is migrated in place,
and persisted `NextCheck` deadlines now wake the scheduler at reset + grace.

## State machine

The durable key is `adapter + AuthIndex + credential fingerprint + BucketID`:

```text
persist old baseline
  -> wait for reset + grace
  -> read quota again (precheck)
  -> normal rollover: record normal_reset and do not send
  -> same old cycle or strict adapter-specific lazy evidence
  -> persist Prepared
  -> atomically persist Sending + per-cycle send fence
  -> send one minimal request with the target credential
  -> wait briefly
  -> read quota and verify each bucket
  -> confirmed or verify-only/retry_wait
```

The `Sending` fence is durable before the network call. Once it exists, the old cycle can never authorize another send. A restart from `Sending`, `Sent`, or `SentUnknown` verifies first and never resends. A request shared by 5-hour and long windows is sent once while verification remains per bucket.

State uses checksummed snapshots plus an fsynced write-ahead log. The in-process controller provides single-flight behavior. Use one state directory per CPA process.

## Provider support

| Adapter | Observed quota | Activation |
|---|---|---|
| Codex / ChatGPT OAuth | Main 5h and long windows, code review, and additional limits from `wham/usage` | Main 5h and explicit 7–32 day windows can use `activate_if_lazy`; shared main windows use one request. Other buckets cannot activate |
| Antigravity | Stable `retrieveUserQuotaSummary` buckets | `gemini-5h` + `gemini-weekly` share `antigravity-gemini`; `3p-5h` + `3p-weekly` share `antigravity-third-party`. Each group receives at most one credential-bound request per expired cycle. Unknown buckets cannot activate |
| Claude OAuth | Not polled | Unsupported until there is evidence that a request is needed to roll its windows |
| Gemini CLI OAuth | Not polled | Unsupported; model/token rows are not assumed to be independent activation buckets |
| Kimi | Not polled | Unsupported; no proven lazy-reset activation contract |
| Grok / xAI | Not polled | Unsupported; no paid health request |
| Unknown | Not polled | Unsupported; no guessed request |

The runtime registers only credential-bound activation adapters. Unsupported providers are logged once and receive no background quota requests. Antigravity only activates its four canonical bucket IDs; new or model-specific bucket IDs returned alongside them cannot pass `CanActivate` until their sharing and reset behavior are known.

## Credential binding and minimal request

Both activation adapters read the exact AuthIndex through `host.auth.get` and use its account-bound headers with CPA `host.http.do`. They never call CPA's public chat endpoint and never ask the scheduler to select an account.

Codex uses the reference scheduler's proven compact request shape: `gpt-5.4-mini`, empty instructions, and one `ping` input, with no streaming or tools. Antigravity uses CPA's direct `v1internal:generateContent` envelope: `gemini-3.1-flash-lite` for the Gemini group and non-thinking `claude-sonnet-4-6` for the Claude/GPT group, with `ping`, one candidate, zero temperature, and a one-token output limit.

Activation requires a still-lazy authoritative precheck, no existing cycle fence, a supported bucket, `activate_if_lazy`, and `dry_run: false`. Normally the precheck runs after a persisted baseline reaches reset + grace. A strict adapter-specific first-observation lazy window, or an already-expired first observation, is persisted and then prechecked immediately before any send.

## Build and install

Requirements: Go 1.26+, CGO, and a C compiler.

```bash
go test ./...
make build
```

On Windows:

```powershell
.\build.ps1
```

The repository includes `.github/workflows/build.yml`. Pushes, pull requests,
and manual **Build** runs first execute `go vet` and the full test suite, then
upload Linux amd64/arm64, macOS amd64/arm64, and Windows amd64 artifacts. GitHub wraps
each artifact as a downloadable ZIP containing the library, documentation,
license, and example configuration. The workflow does not publish a Release.

Install the resulting `quota-window-activator.dll`, `.so`, or `.dylib` under CPA's `plugins/<GOOS>/<GOARCH>/` or `plugins/` directory. The basename is the plugin ID.

## Configuration

Keep dry-run enabled across at least one real reset before allowing sends:

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
        codex: { enabled: true }
        antigravity: { enabled: true }
      disabled_credentials: []
```

`max_retries` applies to control-plane observation/backoff and never permits a second activation for the same credential, bucket, and baseline cycle.

State defaults to the operating-system user config directory under `CLIProxyAPI/quota-window-activator/`. `state_dir` can select a dedicated directory. State contains auth IDs and quota status, but no tokens or complete credential JSON.

## Status and logs

- UI: `/v0/resource/plugins/quota-window-activator/status`
- Authenticated JSON: `GET /v0/management/plugins/quota-window-activator/status`

The browser resource is a combined configuration and status panel. After a
CPA Management key is entered, it loads and saves the complete plugin config
through CPA's own authenticated plugin-config API, while preserving unknown
top-level and provider fields. The key stays in page memory and is never
written to plugin state or browser storage. Disabling dry-run requires an
explicit confirmation in the panel.

The status reports provider, credential, bucket, usage, reset, state, checks, activation time, and result. Logs use the `[quota-activator]` prefix and omit tokens, authorization headers, credential JSON, and upstream bodies.

## CPA v7.2.154 boundaries

CPA v7.2.154 can enumerate file-backed credentials, read one by AuthIndex, and issue Host HTTP calls. Its `host.model.execute` cannot select an AuthID. `host.http.do` does not provide credential generation admission, does not inherit a credential's own `proxy_url`, and context timeout cannot prove that the upstream request was cancelled.

The plugin therefore blocks runtime-only credentials and credentials with a per-auth proxy, treats every timeout as an unknown send result, and verifies before doing anything else. It does not change routing, scheduler state, or credential priority. Token refresh remains CPA's responsibility.

## Risk and compatibility

The Codex compact endpoint, Antigravity internal endpoints, model IDs, and provider quota schemas are private and may change. No real account request was sent during this implementation. Dry-run is the default; validate each provider group on a test credential before production use. A legacy configuration that explicitly sets `mode: observe` is treated as disabled and makes no quota request; enable that provider in the panel to opt into activation.

The build and test baseline is CLIProxyAPI v7.2.154, plugin ABI 1, schema 5. It should remain compatible with later 7.2.x versions that preserve `host.auth.list/get`, `host.http.do`, and Management API schemas. Incomplete windows and unstable bucket IDs are rejected rather than activated.

The reset probe and crash recovery design was informed by the source of [cpa-plugin-codex-quota-scheduler](https://github.com/JefferyZhang2019/cpa-plugin-codex-quota-scheduler), including reset baselines, prechecks, ProbeAttempt send fences, suppression, and verify-first recovery.

中文说明见 [README.zh-CN.md](README.zh-CN.md).
