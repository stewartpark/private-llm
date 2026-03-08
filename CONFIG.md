# Configuration Guide

This guide documents all available configuration options for `agent.json`, which is automatically created during `private-llm up` or `private-llm configure`.

## File Locations

| Platform | Config Path |
|----------|------------|-
| macOS / Linux (CLI) | `~/.config/private-llm/agent.json` |
| Linux (systemd service) | `/etc/private-llm/agent.json` |

---

## Configuration Options

### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `project_id` | string | **Required.** Your GCP project ID. Auto-detected from gcloud if available. |

### Compute Settings

| Field | Type | Default | Description |
|-------|------|-|------------|
| `zone` | string | `us-central1-a` | GCP zone where the VM will be created. Must match availability for selected machine type. |
| `vm_name` | string | `private-llm-vm` | Name of the Compute Engine instance. |
| `machine_type` | string | `g4-standard-48` | GPU machine type. See [GPU Machine Types](#gpu-machine-types) below. |

### Ollama Settings

| Field | Type | Default | Description |
|-------|------|-|------------|
| `default_model` | string | `stewartpark/qwen3.5` | Model to pull and load on VM boot. |
| `context_length` | int | `262144` | Context window size (tokens). |
| `kv_cache_type` | string | `bf16` | KV cache type: `bf16`, `q8_0`, `q4_0`, or `f16`. Higher precision = more VRAM. |
| `num_batch` | int | `1024` | Batch size for prompt processing (`OLLAMA_NUM_BATCH`). |
| `num_instances` | int | `2` | Number of Ollama instances (1-4). More instances = higher concurrency. |
| `num_parallel` | int | `1` | Parallel requests per instance (`OLLAMA_NUM_PARALLEL`). Set to 1 for large models with `num_instances > 1`. |

### Network Settings

| Field | Type | Default | Description |
|-------|------|-|------------|
| `network` | string | `private-llm` | VPC network name. |
| `subnet` | string | `private-llm-subnet` | Subnet name within the VPC. |
| `subnet_cidr` | string | `10.10.0.0/24` | CIDR range for the subnet. |
| `listen_addr` | string | `127.0.0.1` | Address the proxy binds to. Use `0.0.0.0` for multi-user/shared access (Linux systemd). |

### Runtime Settings

| Field | Type | Default | Description |
|-------|------|-|------------|
| `idle_timeout` | int | `300` | Seconds of idle time before VM auto-stops. Set higher for long-running sessions. |

### Security Settings

| Field | Type | Default | Description |
|-------|------|-|------------|
| `disable_hsm` | bool | `false` | Disable HSM-backed KMS encryption. **Warning:** Only disable for debugging; HSM provides hardware-level key protection. |

---

## GPU Machine Types

Select a machine type based on your model size and VRAM needs:

### G2 — NVIDIA L4 (24 GB per GPU)

| Type | GPUs | vCPU | RAM | Best For |
|------|-----|-|-|-------------|
| `g2-standard-4` | 1x L4 | 4 | 16 GB | 7B-13B models |
| `g2-standard-8` | 1x L4 | 8 | 32 GB | 13B-20B models |
| `g2-standard-12` | 1x L4 | 12 | 48 GB | 20B-35B models |
| `g2-standard-16` | 1x L4 | 16 | 64 GB | 35B models |
| `g2-standard-24` | 2x L4 | 24 | 96 GB | 70B models |
| `g2-standard-32` | 1x L4 | 32 | 128 GB | Fine-tuning small models |
| `g2-standard-48` | 4x L4 | 48 | 192 GB | Multiple concurrent 70B sessions |
| `g2-standard-96` | 8x L4 | 96 | 384 GB | Heavy parallel workloads |

### G4 — NVIDIA RTX PRO 6000 (96 GB per GPU)

| Type | GPUs | vCPU | RAM | Best For |
|------|-----|-|-|-------------|
| `g4-standard-48` | 1x RTX 6000 | 48 | 180 GB | **Default:** 70B+ models |
| `g4-standard-96` | 2x RTX 6000 | 96 | 360 GB | Concurrent sessions, large contexts |
| `g4-standard-192` | 4x RTX 6000 | 192 | 720 GB | Heavy multi-session workloads |
| `g4-standard-384` | 8x RTX 6000 | 384 | 1440 GB | Maximum parallel capacity |

### A2 — NVIDIA A100 (40 GB per GPU)

| Type | GPUs | vCPU | RAM | Best For |
|------|-----|-|-|-------------|
| `a2-highgpu-1g` | 1x A100 | 12 | 85 GB | Legacy workloads |
| `a2-highgpu-2g` | 2x A100 | 24 | 170 GB | Medium parallel workloads |
| `a2-highgpu-4g` | 4x A100 | 48 | 340 GB | Heavy training/inference |
| `a2-highgpu-8g` | 8x A100 | 96 | 680 GB | Maximum A100 capacity |

### A3 — NVIDIA H100 (80 GB per GPU)

| Type | GPUs | vCPU | RAM | Best For |
|------|-----|-|-|-------------|
| `a3-highgpu-1g` | 1x H100 | 26 | 234 GB | Latest hardware, single session |
| `a3-highgpu-2g` | 2x H100 | 52 | 468 GB | Dual-session workloads |
| `a3-highgpu-4g` | 4x H100 | 104 | 936 GB | High-throughput inference |
| `a3-highgpu-8g` | 8x H100 | 208 | 1872 GB | Maximum performance |

### A4 — NVIDIA B200 (180 GB per GPU)

| Type | GPUs | vCPU | RAM | Best For |
|------|-----|-|-|-------------|
| `a4-highgpu-8g` | 8x B200 | 224 | 3968 GB | Cutting-edge, massive models |

---

## Zone Availability

Zones are automatically filtered based on your selected machine type. Run `private-llm up` without arguments to see available zones for each GPU family.

**Common zones:**
- **g2:** 30+ zones worldwide
- **g4:** 18+ zones (US, Europe, Asia)
- **a2:** 10+ zones
- **a3:** 18+ zones
- **a4:** 6 zones (limited availability)

---

## Example Configurations

### Minimal 7B Models

```json
{
  "project_id": "my-gcp-project",
  "zone": "us-central1-a",
  "machine_type": "g2-standard-4",
  "default_model": "llama3.2:7b",
  "context_length": 65536,
  "num_instances": 1,
  "idle_timeout": 300
}
```

### Production 70B Models

```json
{
  "project_id": "my-gcp-project",
  "zone": "us-central1-b",
  "machine_type": "g4-standard-48",
  "default_model": "stewartpark/qwen3.5",
  "context_length": 262144,
  "kv_cache_type": "bf16",
  "num_instances": 2,
  "num_parallel": 1,
  "idle_timeout": 600
}
```

### Multi-User Shared Access (Linux)

```json
{
  "project_id": "my-gcp-project",
  "zone": "us-central1-b",
  "machine_type": "g4-standard-96",
  "default_model": "stewartpark/qwen3.5",
  "context_length": 262144,
  "num_instances": 4,
  "num_parallel": 1,
  "idle_timeout": 900,
  "listen_addr": "0.0.0.0"
}
```

---

## Modifying Configuration

### Interactive Method (Recommended)

```bash
private-llm up              # Re-run setup with current values pre-filled
# or
private-llm configure       # Reconfigure without provisioning
```

### Manual Edit

1. Stop the proxy: `pkill private-llm` or press `q` in TUI
2. Edit `~/.config/private-llm/agent.json`
3. Restart: `private-llm`

**Note:** Infrastructure changes (`machine_type`, `zone`, `network`) require running `private-llm down && private-llm up` to apply.

---

## Environment Variables

The following environment variables are passed to Ollama on the VM:

| Variable | Value From Config |
|----------|-----------------|
| `OLLAMA_NUM_BATCH` | `num_batch` |
| `OLLAMA_NUM_PARALLEL` | `num_parallel` |
| `OLLAMA_CONTEXT_LENGTH` | `context_length` |
| `KV_CACHE_TYPE` | `kv_cache_type` |

---

## Troubleshooting

**"Project ID is required"** — Run `gcloud auth application-default login` first or manually set `project_id`.

**Zone not available for machine type** — Some GPUs are region-restricted. Re-run `private-llm up` to see valid combinations.

**KV cache OOM errors** — Reduce `context_length`, switch `kv_cache_type` to `q4_0`, or use a larger machine type.

**"num_instances > 4"** — Limited to 4 instances maximum (hardcoded). Increase `idle_timeout` instead for longer sessions.

---

## More Information

- **Architecture:** See [`AGENTS.md`](AGENTS.md) for system design
- **Security:** See [`SECURITY.md`](SECURITY.md) for threat model
- **Linux Setup:** See [`packaging/linux/`](packaging/linux/) for systemd installation
