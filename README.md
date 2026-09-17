# CPA Quota Window Activator

`cpa-plugin-quota-window-activator` is a background maintenance plugin for CLIProxyAPI (CPA). It tracks each quota bucket of each credential, waits for the old window to expire, performs an authoritative quota precheck, and sends one tiny credential-bound model request only when the window still appears lazy. It then reads quota again and verifies every affected bucket.

This is not a periodic ping plugin. The default is `dry_run: true`.

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
| Codex / ChatGPT OAuth | Main 5h and long windows, code review, and additional limits from `wham/usage` | Main 5h and explicit 7–32 day windows can use `activate_if_lazy`; shared main windows use one request. Other buckets are observe-only |
| Antigravity | Stable `retrieveUserQuotaSummary` buckets | Observe-only until lazy behavior and bucket-to-model evidence exist |
| Claude OAuth | Five-hour, weekly, and model-specific weekly usage | Observe-only; no evidence that a request is needed to roll these windows |
| Gemini CLI OAuth | `retrieveUserQuota` model/token rows | Observe-only; rows are not assumed to be independent activation buckets |
| Kimi | Stable coding usage windows | Observe-only |
| Grok / xAI | Stable billing credit periods | Observe-only; no paid health ping |
| Unknown | None | Unsupported; no guessed request |

Observe-only adapters do not implement the activation interface, so a mistaken `activate_if_lazy` setting still cannot send a model request.

## Credential binding and minimal request

The Codex adapter reads the exact AuthIndex through `host.auth.get` and uses its account-bound headers with CPA `host.http.do`. It never calls CPA's public chat endpoint and never asks the scheduler to select an account. The request uses `gpt-5.4-mini`, `ping`, non-streaming behavior, low reasoning, and a one-token output budget declaration.

Activation requires a persisted baseline, expired reset plus grace, a still-lazy precheck, no existing cycle fence, a supported bucket, `activate_if_lazy`, and `dry_run: false`.

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
        codex: { enabled: true, mode: activate_if_lazy }
        antigravity: { enabled: true, mode: observe }
        claude: { enabled: true, mode: observe }
        gemini-cli: { enabled: true, mode: observe }
        kimi: { enabled: true, mode: observe }
        xai: { enabled: true, mode: observe }
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

The Codex compact endpoint and provider quota schemas are private and may change. No real account request was sent during this implementation. Dry-run is the default; validate on a test credential before production use.

The build and test baseline is CLIProxyAPI v7.2.154, plugin ABI 1, schema 5. It should remain compatible with later 7.2.x versions that preserve `host.auth.list/get`, `host.http.do`, and Management API schemas. Incomplete windows and unstable bucket IDs are rejected rather than activated.

The reset probe and crash recovery design was informed by the source of [cpa-plugin-codex-quota-scheduler](https://github.com/JefferyZhang2019/cpa-plugin-codex-quota-scheduler), including reset baselines, prechecks, ProbeAttempt send fences, suppression, and verify-first recovery.

中文说明见 [README.zh-CN.md](README.zh-CN.md).
