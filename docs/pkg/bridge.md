# pkg/bridge operational facade

`pkg/bridge` exposes the Go SDK surface for embedding the bridge:

For a task-oriented guide with service, embedded-runtime, lark-cli, card, and
telemetry examples, see [Go SDK Usage](../go-sdk-usage.md).

The Go SDK is consumed as a Go module:

```sh
go get github.com/GatewayJ/lark-bridge-agent-sdk/pkg/bridge@latest
```

```go
type Bridge struct{}

type Options struct {
    Home    string
    Profile string

    Logger    Logger
    Telemetry TelemetryAdapter

    Client       *Client
    CodexClient  *CodexClientOptions
    ClaudeClient *ClaudeClientOptions

    LarkTransport              LarkTransport
    LarkAdapter                *LarkAdapter
    LarkIntake                 LarkIntakeSink
    LarkCardActions            LarkCardActionDispatcher
    LarkComments               CommentSurface
    LarkCommentOptions         CommentOptions
    LarkManaged                LarkManagedOptions
    LarkProfileProjection      LarkProfileProjectionHook
    ForwardCardPromptsToIntake bool
    RuntimeAdapter             RuntimeAdapter
    AppID                      string
    Tenant                     RuntimeTenant
    AgentKind                  RuntimeAgentKind
    Version                    string
    ConfigPath                 string
}

func New(opts Options) (*Bridge, error)
func (b *Bridge) Start(ctx context.Context) error
func (b *Bridge) Shutdown(ctx context.Context) error
func (b *Bridge) Status(ctx context.Context) (Status, error)
```

Use `StartProfileService`, `NewProfileServiceController`, and
`BootstrapProfileConfig` for CLI-equivalent production integration. Those APIs
assemble the profile config, app-secret materialization, lark-cli projection,
`PreflightLarkCLI`, runtime locks, and OS service wiring around the same bridge
runtime. `New` is intentionally lower-level: it starts only the injected client,
transport/intake, or custom runtime adapter, so the host owns any surrounding
profile, credentials, lark-cli, and service lifecycle.

Supported embedding modes:

- agent-only: pass `Client`, `CodexClient`, or `ClaudeClient`; `Start` loads state and `Shutdown` stops and flushes the client;
- injected Lark channel: pass `LarkTransport` plus `AppID`; `Start` wraps it in `LarkAdapter` and `RuntimeCoordinator` for profile/app locks and registry status. If a `Client` is also provided and no custom `LarkIntake` is supplied, the SDK auto-wires a managed intake for IM messages, slash commands, and document comments when a `LarkComments` surface or OAPI transport is available;
- custom runtime: pass `RuntimeAdapter` plus `AppID` when the host owns channel startup. `RuntimeAdapterFunc` is available for small host-side adapters.

Public lark-cli helpers use the canonical all-caps `CLI` spelling:

```go
func BuildLarkChannelEnv(context LarkCLIEnvContext) map[string]string
func BuildLarkCLISourceProjection(cfg LarkCLIAppConfig, paths LarkCLIProjectionPaths) LarkCLISourceProjection
func WriteLarkCLISourceProjection(cfg LarkCLIAppConfig, paths LarkCLIProjectionPaths) (string, error)
func PreflightLarkCLI(ctx context.Context, options LarkCLIPreflightOptions) (LarkCLIPreflightResult, error)
```

Command handling can be used directly through `Client.HandleCommand` for
backwards-compatible state, or through `NewCommandHandler(client)` when a host
wants an explicit per-handler lifetime for `/cd`, `/ws`, resume, and timeout
state. Hosts that use `Client.HandleCommand` while rotating clients can call
`client.ReleaseCommandState()` before discarding a client. Managed OAPI/Fake
Lark transports provide `/new chat [name]` automatically; direct command hosts
can pass `CommandOptions.ChatCreator` to enable group creation with sender
invitation.
If the host already knows the app creator/owner `open_id`, pass
`LarkManagedOptions.InitialOwnerOpenID` as the startup fallback; the managed
intake still refreshes the canonical owner from Feishu/Lark runtime info.

Minimal external embedding example:

```go
instance, err := bridge.New(bridge.Options{
    Home:          homeDir,
    Profile:       "codex",
    AppID:         "cli_xxx",
    AgentKind:     bridge.RuntimeAgentCodex,
    LarkTransport: bridge.NewFakeLarkTransport(bridge.LarkBotIdentity{OpenID: "ou_bot"}),
    LarkIntake: bridge.LarkIntakeSinkFunc(func(context.Context, bridge.LarkNormalizedEvent) error {
        return nil
    }),
})
if err != nil {
    return err
}
if err := instance.Start(ctx); err != nil {
    return err
}
defer instance.Shutdown(context.Background())
```

## Managed Feishu/Lark Runtime

The module includes a production Feishu/Lark OpenAPI transport and a managed IM
intake. The managed path covers:

- access checks, group mention policy, slash-command-first handling, debounce
  batching, and per-scope `/timeout` overrides;
- Codex / Claude runs with bridge prompt injection and Lark CLI bridge
  environment projection;
- media attachment resolution, quote context, interactive-card context,
  multi-sender prompt annotations, and attachment-reference cleanup;
- `markdown`, `text`, and `card` reply modes, including single-message
  streaming updates when the transport supports message patching;
- optional COT process messages through `LarkCOTClient`, followed by a separate
  final-answer reply;
- best-effort `Typing` reactions for non-card reply modes when reactions are
  supported.

`ProfileBridgeOptions.LogDir` overrides the default
`<Home>/profiles/<Profile>/logs` JSONL log directory.

## Managed Commands And Account Forms

Managed `/config` and `/account` responses render Go CardKit forms and update
the originating card action when possible. Submit actions detach from the
callback and wait for the CardKit settle window before updating the original
card, preserving the behavior proven by the original JavaScript bridge.

`/config submit` can apply and roll back the lark-cli identity policy through
the SDK hook. `/account submit` can validate self-built app credentials, persist
the secret through the exec secret provider, and request a deduplicated
reconnect through the SDK reconnector hook. Failed account submits turn the
original form card into a static failure record and send a fresh retry form
without carrying the submitted secret forward.

The OAPI transport supplies the default quote resolver, scope checker,
incremental scope-grant requester, app-owner refresh, and known-chat refresh.
Custom transports can provide `LarkManaged.ScopeChecker`,
`LarkManaged.ScopeGrant`, and `LarkManaged.RuntimeInfo` for equivalent behavior.

## CLI And Service Facades

The Go CLI can bootstrap a profile v2 config from either an interactive Lark
registration link or supplied `--app-id` / `--app-secret` credentials. It writes
the active profile, profile-local encrypted app secret, root `bridge` exec
secret provider, default workspace, and Codex binary metadata for explicit
Codex profiles. Supplied app credentials are validated before being stored.

The CLI also exposes foreground-process `ps` and `kill`, managed `/ps` and
`/exit`, basic profile operations, encrypted secret management, and legacy v1
config/state migration. Initialized profiles reject mismatched `--app-id`
overrides instead of running against one app while the profile records another.

The service facade uses public SDK types for adapter results, service
definitions, process listing, and runtime-lock hooks. `StartProfileService`
loads the profile config, materializes env-backed app secrets into the profile
keystore for daemon use, runs the same lark-cli preflight/bind path as the CLI
unless skipped, and starts the OS-managed daemon through launchd, systemd user
units, or Windows Task Scheduler.

The package also mirrors the historical JavaScript bridge helpers for card
rendering and telemetry. Callers can use the JS-compatible names `InitialState`,
`Reduce`, `FinalizeIfRunning`, `MarkInterrupted`, `MarkIdleTimeout`,
`RenderCard`, and `RenderText`, or the richer Go `RunCardState` facades.
Telemetry adapters receive the JavaScript-shaped `TelemetryEvent` fields
(`level`, `phase`, `event`, `fields`, `ctx`, `ts`) as well as Go convenience
fields (`name`, `at`). Callers can install package-level telemetry with
`SetDefaultTelemetry`, `ReportEvent`, `ReportMetric`, and `ReportError`.
`SetDefaultTelemetry` is process-global; embedded SDK users should call the
returned restore function when a scoped adapter is no longer active.
