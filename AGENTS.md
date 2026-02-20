# Private LLM AGENTS.md

## Overview

Enterprise-grade local LLM proxy with mTLS, zero-trust security, auto-rotating certs, and dynamic firewall. Single Go binary + macOS app + Linux systemd service. No third parties in data path.

**Deployed Infrastructure**: Go/Pulumi GCP provisioning (no Terraform). State stored locally at `~/.config/private-llm/state/`.

---

## Technical Decisions & Architecture

### 1. Security Model: Split-Trust mTLS

**Decision**: CA private key never leaves local machine; server certs stored in GCP Secret Manager (HSM-protected).

**Why**:
- Even if GCP fully compromised, attackers cannot forge certificates (CA key local-only)
- Cert fingerprint pinned in memory (SHA-256), detecting impersonation even if CA cert replaced
- 4096-bit RSA keys, TLS 1.3 minimum
- Zero-trust: every request validated with mTLS + Bearer token

**Key Files**:
- Local only: `~/.config/private-llm/certs/{ca.key, ca.crt, client.crt, client.key, token}`
- Cloud (Secret Manager): `{server-cert, server-key, ca-cert, internal-token}` encrypted with KMS

**Rotation**: On every VM cold start (stopped → running):
1. CLI opens dynamic firewall (IP-locked to user's public IP)
2. Generate new server cert + key (7-day validity), client cert + key, bearer token
3. Server artifacts → Secret Manager; client artifacts → local disk
4. Pin server cert fingerprint in memory
5. VM boots, fetches fresh certs + token from Secret Manager

**Emergency CA Rotation**:
```bash
private-llm rotate-mtls-ca  # Deletes CA, regenerates everything, pushes to Secret Manager
```

---

### 2. Auto-Scaling to Zero

**Decision**: VM starts on first request, auto-stops after idle timeout (5 min default). Dynamic firewall rule deleted on shutdown.

**Why**:
- Cost optimization: $0 when idle (only persistent storage: ~$18/mo)
- No manual management: scale to zero eliminates idle GPU costs
- Firewall security: only current public IP allowed (deleted on exit)

**Flow**:
```
Request → Proxy → VM off? → [Open firewall] → [Rotate certs] → [Start VM] → [Wait for Ollama] → Request proxied
Idle > timeout? → Stop VM (scale to zero)
Quit → Delete firewall rule
```

**Grace Periods**:
- First boot: 30-min grace for package installation
- Subsequent boots: idle check immediate

---

### 3. Infrastructure Provisioning: Embedded Pulumi

**Decision**: Use Pulumi Automation API embedded in Go binary (no external CLI, no Terraform).

**Why**:
- Single binary deployment (no dependency installing pulumi CLI)
- Full Go programmatic control, error handling, and testing
- Local state backend (no remote backend needed, simplified)

**Resources Provisioned**:
- VPC + Subnet (10.10.0.0/24 default)
- KMS KeyRing + CryptoKey (HSM-backed, 90-day rotation)
- 4 Secrets in Secret Manager (CA cert, server cert, server key, bearer token)
- Service Account (minimal: logging + monitoring only)
- Shielded GPU VM (Spot, Secure Boot, vTPM, Hyperdisk Balanced)

**Import/Refresh**:
- Detects existing GCP resources on first run
- Imports into Pulumi state, then refreshes
- No manual state migration needed

---

### 4. Zero-Maintenance Deployment: Immutable Infrastructure

**Decision**: No configuration drift. Every deploy creates identical resources.

**Why**:
- Predictable behavior (no "works on my machine")
- Easier debugging (no hidden state drift)
- Simpler rollback (destroy + recreate)

**Pattern**: `down` → `up` for any configuration change.

---

### 5. Real-Time Token Counting: Content-Agnostic Parsing

**Decision**: Parse streaming responses to count tokens per API style (Ollama, OpenAI Chat, Anthropic, OpenAI Responses).

**Why**:
- Live dashboards: tok/sec, total tokens
- Per-request token stats for TUI
- Works with any Ollama-compatible tool

**Implementation** (`tokens.go`):
- Line-by-line SSE parsing with buffering
- Per-request counters (not global)
- `countOutput()` thread-safe via `atomic.Int64`
- Live rate calculation using nanosecond timestamps

**API Styles**:
- **Ollama**: `/api/generate`, `/api/chat` — non-done lines count as output
- **OpenAI Chat**: `/v1/chat/completions` — delta.content = 1 token
- **Anthropic**: `/v1/messages` — event-based (message_start, content_block_delta, message_delta)
- **OpenAI Responses**: `/v1/responses` — event-based (output_text.delta, completed)

---

### 6. Proxy Architecture: Lazy Boot + Recovery

**Decision**: Proxy blocks requests until VM ready. Recovery loop on failures.

**Why**:
- Transparency: tools don't know VM is remote
- Reliability: auto-recovers from transient failures
- User-friendly: no manual VM start needed

**Pattern**:
```
proxyHandler (concurrent) → ops.EnsureSetup() → [gate closed?] → ops.DoSetup() (serialized)

Ops Loop (single goroutine):
  - Boot/recovery signals (deduplicated via buffered channel)
  - TUI actions (start/stop/restart/reset)
  - Serialized infra mutations (no race conditions)
```

**Retry Logic** (`proxy.go:151`):
- 12 retries on 502s or connection errors (5s sleep between)
- On first failure: trigger recovery, re-fetch certs, retry
- After max retries: return 502/503 to client

---

### 7. macOS App: System Menu Bar + Terminal Wrapper

**Decision**: Native Swift app (Cocoa + SwiftTerm) wrapping Go CLI.

**Why**:
- System integration: menu bar icon, status polling
- No terminal tab left open
- Better UX for desktop users

**How It Works**:
- `AppDelegate`: status polling (5s), icon updates, menu management
- `TerminalWindowController`: LocalProcessTerminalView, env extraction (login shell)
- Binary search: bundled → sibling → ~/.local/bin → PATH
- Auto-starts `private-llm` CLI on launch (hidden)
- Status file: `~/.config/private-llm/status` (consumed by both CLI and app)

**Key Design**:
- apps get minimal env via launchd → launch login shell to capture user PATH
- Cmd+W / Escape → hide window (not close) for quick toggle
- `setActivationPolicy(.accessory)` when hidden (no Dock icon)

---

### 7b. Linux Support: Systemd Service

**Decision**: Run as systemd service (`private-llm` user) on Linux servers for shared multi-user access.

**Why**:
- Shared resource: multiple users can connect to the same proxy
- Service management: automatic restart, logging, dependency handling
- Listen address configurable via `private-llm configure` (set `0.0.0.0` for multi-user access)

**How It Works**:
- Service runs as `private-llm` system user (not root)
- Config: `/etc/private-llm/agent.json` (created via `private-llm configure --config /etc/private-llm/agent.json`)
- Data directory: `/var/lib/private-llm/`

**Package Contents**:
| Path | Purpose |
|------|-------|
| `/usr/bin/private-llm` | CLI binary |
| `/etc/private-llm/` | Configuration directory |
| `/var/lib/private-llm/` | Runtime data (Pulumi state, certs) |
| `/usr/lib/systemd/system/private-llm.service` | Systemd unit file |

**Installation**:
```bash
# Debian/Ubuntu
sudo dpkg -i private-llm_*.deb

# RHEL/CentOS/Fedora
sudo rpm -ivh private-llm-*.rpm

# Create config (interactive setup)
sudo -u private-llm private-llm configure --config /etc/private-llm/agent.json

# Start service
sudo systemctl start private-llm
sudo systemctl status private-llm
```

**Configuration**:
```bash
# Reconfigure
sudo -u private-llm private-llm configure --config /etc/private-llm/agent.json

# Restart service
sudo systemctl restart private-llm

# View logs
journalctl -u private-llm -f
```

---

### 8. Interactive Setup: Styled CLI with Fallbacks

**Decision**: Rich arrow-key navigation with numbered fallback.

**Why**:
- User-friendly onboarding
- Works in terminals without ANSI support

**Features**:
- Machine type → zones mapping (GPU availability per region)
- HSM encryption toggle
- Default persistence (press Enter)
- Input validation required fields
- Listen address prompt (default: `127.0.0.1`, set `0.0.0.0` for shared access)

---

## File Layout & Navigation

### CLI Core (`cli/`)

| File | Purpose |
|------|-------|
| `main.go` | Entry point: `up`, `down`, `configure`, Serve (default) |
| `ops.go` | Serialized infra ops loop (boot, recovery, TUI actions) |
| `proxy.go` | HTTP proxy handler (Ollama port 11434 → VM 8080) |
| `vm.go` | GCP VM operations (start/stop/delete/status, Ollama health) |
| `certs.go` | TLS config loading, cert validation, fingerprint pinning |
| `rotation.go` | Auto-rotation of CA, server/client certs, tokens on VM boot |
| `firewall.go` | Dynamic firewall rule (IP-locked, deleted on shutdown) |
| `config.go` | JSON config loading/saving, default values, interactive prompts |
| `tokens.go` | Streaming token parser (Ollama, OpenAI, Anthropic, Responses) |
| `setup.go` | Interactive setup UI with arrow-key navigation |

### Infrastructure (`cli/infra/`)

| File | Purpose |
|------|-------|
| `program.go` | Pulumi program definition (infrastructure resource graph) |
| `stack.go` | Pulumi Automation API wrapper (up/down/preview/import/refresh) |
| `types.go` | Config structs and resource result types |
| `apis.go` | Enable required GCP APIs (Compute, Secret Manager, KMS) |
| `network.go` | VPC, Subnet, Private Google Access |
| `kms.go` | KMS KeyRing, CryptoKey (HSM), auto-rotation |
| `secrets.go` | Secret Manager secrets (4 secrets encrypted with KMS) |
| `iam.go` | Service Account, IAM bindings (minimal permissions) |
| `compute.go` | Shielded VM instance (Spot, Secure Boot, vTPM, startup script) |
| `import.go` | Detect existing GCP resources, import into Pulumi state |

### TUI (`cli/tui/`)

| File | Purpose |
|------|-------|
| `tui.go` | Main TUI program (bubbletea), status updates, event dispatch |
| `model.go` | Dashboard model (VM status, network info, token counts) |
| `view.go` | Renderers for boot animation, dashboard, logs |
| `op_model.go` | Operation modal (up/down, preview/confirm/apply) |
| `op_view.go` | Renderers for operation phases |
| `op_program.go` | Operation program wrapper (confirm dialog) |

### macOS App (`app/`)

| File | Purpose |
|------|-------|
| `main.swift` | NSApplication entry point |
| `AppDelegate.swift` | Status polling, menu bar icon, window visibility |
| `TerminalWindowController.swift` | TerminalView, process management, env extraction |

### Linux Packaging (`packaging/linux/`)

| Path | Purpose |
|------|-------|
| `systemd/private-llm.service` | Systemd unit template |
| `deb/control` | DEB package control file |
| `deb/postinst` | DEB post-install script |
| `deb/postrm` | DEB post-remove script |
| `rpm/private-llm.spec` | RPM spec file |
| `build.sh` | Build script for packaging |

### Embedded Assets

| File | Purpose |
|------|-------|
| `cli/embed.go` | Embeds startup script and Caddyfile |
| `cli/config/vm-startup.sh` | VM startup script (Ollama install, GPU setup, idle monitor) |
| `cli/config/Caddyfile` | Reverse proxy config (mTLS validation, bearer token) |

### Configuration Paths

| Path | Purpose |
|------|-------|
| `~/.config/private-llm/agent.json` | CLI configuration (project, zone, VM name, models, etc.) |
| `~/.config/private-llm/certs/` | Local mTLS certs + tokens |
| `~/.config/private-llm/state/` | Pulumi state (local file backend) |
| `~/.config/private-llm/status` | VM status for macOS app |
| `/etc/private-llm/agent.json` | Linux system config (via `--config`) |
| `/var/lib/private-llm/` | Linux runtime data directory |

---

## Key Constants & Defaults

| Setting | Default | Purpose |
|---------|---------|---------|
| Port | 11434 | Ollama-compatible endpoint (localhost) |
| VM Machine Type | g2-standard-48 | 4x NVIDIA L4 GPUs |
| Default Model | qwen3-coder-next:q8_0 | Pre-warmed on VM boot |
| Context Length | 262144 | Ollama context window |
| Idle Timeout | 300s | VM auto-stop after idle |
| Subnet CIDR | 10.10.0.0/24 | Private subnet |
| Cert TTL | 7 days | Server/client cert expiry |
| CA TTL | 10 years | Root CA validity |
| Cache TTL | 30 min | TLS config cache |
| Retry Max | 12 | Proxy retries on failure |
| Retry Delay | 5s | Between retries |
| Poll Interval | 5s | Status polling |
| Grace Period | 30 min | First boot (package install) |
| Listen Addr | 127.0.0.1 | Bind address (set `0.0.0.0` for shared access) |

---

## GCP GPU Availability (Zones per Machine Type)

| GPU Family | Zones | GPU |
|------------|-------|-----|
| g2 | 30+ zones | NVIDIA L4 (24GB) |
| g4 | 18+ zones | RTX PRO 6000 (96GB) |
| a2 | 10+ zones | A100 (40GB) |
| a3 | 18+ zones | H100 (80GB) |
| a4 | 6 zones | B200 (180GB) |

*Source: cli/setup.go:zonesForFamily (2026-02-05)*

---

## Debugging & Maintenance

### Check VM Status
```bash
cat ~/.config/private-llm/status  # Prints: RUNNING / STOPPED / PROVISIONING / AUTH ERROR
```

### Manual Rotation (if cert compromised)
```bash
private-llm rotate-mtls-ca
private-llm down && private-llm up  # Recreate VM with new certs
```

### View Pulumi Logs
```bash
# Inspect Pulumi state
ls -la ~/.config/private-llm/state/
```

### Reset Everything
```bash
private-llm down  # Destroy infrastructure
rm -rf ~/.config/private-llm/{state,certs}  # Clean local state
private-llm up    # Fresh provisioning
```

### Test Connectivity
```bash
# Check proxy is listening
curl http://localhost:11434/api/tags

# Check VM reachable (via proxy)
curl -k https://<vm-ip>:8080/api/tags  # Only works after proxy starts
```

---

## Common Scenarios

### Proxy Not Responding
1. Check VM status: `cat ~/.config/private-llm/status`
2. If "NOT FOUND": `private-llm up`
3. If "AUTH ERROR": `gcloud auth application-default login`

### VM Stuck Booting
- First boot only: wait 30 min (installing packages)
- Subsequent boots: 3-5 min normally
- If > 10 min: `private-llm down && private-llm up` (reset)

### Certificate Errors
- Clear local certs: `rm ~/.config/private-llm/certs/*`
- Restart proxy

### Firewall Issues
- Ensure external IP detection works: `curl https://api.ipify.org`
- Temporarily allow all: `private-llm -allow-all` (not recommended)

### Linux Service Issues
- Check service status: `sudo systemctl status private-llm`
- View logs: `journalctl -u private-llm -f`
- Restart service: `sudo systemctl restart private-llm`

---

## Future Roadmap

- [ ] AWS support (same architecture)
- [ ] Azure support (same architecture)
- [ ] Multi-VM load balancing
- [ ] Model hot-swapping without VM restart
- [ ] Prometheus metrics endpoint
- [ ] Web UI dashboard

---

## Key Design Principles

1. **Zero Maintenance**: Infrastructure immutable, auto-scale to zero
2. **End-to-End Security**: mTLS + cert pinning + HSM-protected secrets
3. **Transparency**: Ollama-compatible API, no config changes needed
4. **Reliability**: Retry loops, recovery signals, fallbacks
5. **User Experience**: Rich CLI setup, macOS menu bar integration, live TUI

---

## Contributing

1. Read `AGENTS.md` before making changes
2. Maintain this file when adding/altering technical decisions
3. Keep dense/brief — avoid walls of text
4. Update examples if commands change
5. Document trade-offs made (not just "what")

---

*Last updated: 2026-02-19 - Added Linux systemd service support*
