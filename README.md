# Private LLM

Deploy enterprise-grade private LLM inference with **zero data leakage**. Send your ideas, research, code, trade secrets - whatever you need. Your data stays yours.

## 🏛️ Problem

You want privacy, but every option is impractical:

**❌ Public APIs?**
- You send sensitive data in every request
- You don't know who owns the provider or their data practices
- Every request is exposed to potential data collection
- The privacy risk outweighs any convenience

**❌ Mini Mac or home server?**
- Buy hardware (mini Mac = $1000+, GPU server = $3000+)
- Maintain it: electricity, cooling, network, hardware failures
- Your home IP may leak via outgoing connections
- Single point of failure, no redundancy
- Time investment outweighs benefits

**❌ DIY GPU setup?**
- Research GPU compatibility for weeks
- Configure Linux, drivers, CUDA - months of tinkering
- Deal with instability, updates, supply chains
- Same data center ownership problems, less scale

**You need:**
- ✅ **Enterprise privacy**: Data never leaves your infrastructure
- ✅ **Production-grade reliability**: Built for 99.9%+ uptime
- ✅ **Operational simplicity**: Deploy and forget
- ✅ **Cost-efficiency**: No idle waste

## 💰 The Freedom You Get

- **Literal privacy**: Send ideas, code, research, and sensitive information securely
- **Zero data collection**: No logs, no telemetry, no user tracking
- **No vendor lock-in**: Your data exists only in your infrastructure
- **No maintenance burden**: No servers to manage, update, or replace
- **Low cost**: Only pay when you actually use the model

## 🏛️ What Makes This Enterprise-Grade

**Industry-standard security, made simple:**

- **Defense in depth**: Multi-layer authentication (token + mTLS)
  - External token authenticates you to the service
  - Internal certificates encrypt the actual request
  - Both required - if one is compromised, the system stays safe

- **Zero trust architecture**: Nothing assumed trusted
  - Cloud providers can't access your data
  - Network attacks prevented at multiple layers
  - Hardware security (TPM, Secure Boot) validates every boot

- **Compliance-ready**: SOC 2, ISO 27001-aligned controls
  - Audit trails without data logging
  - Secure key management with KMS
  - Immutable infrastructure prevents drift

- **Production reliability**: Not just "works sometimes"
  - State tracking prevents premature VM shutdown during active requests
  - Auto-recovery from failures
  - First-boot optimization (skip installation on subsequent runs)

## 🛡️ Architecture

```
Your Application
      ▼
[External API Token]
      ▼
┌─────────────────────────┐
│ Cloud Functions (Proxy) │
│ - Validate token        │
│ - Check VM availability  │
│ - HTTPS → mTLS Tunnel   │
└───────────┬─────────────┘
            ▼ mTLS (TLS 1.3, 4096-bit RSA)
┌─────────────────────────┐
│ VM (Spot L4 GPU)        │
│ - mTLS Server Cert      │
│ - Models on local SSD   │
│ - Shielded VM (Secure Boot+TPM)
└─────────────────────────┘
      ▼
Your Response (prompt and results stay private)
```

## 🛡️ Architecture

```
Your Application
      ▼
[External API Token]
      ▼
┌─────────────────────────┐
│ Cloud Functions (Proxy) │
│ - Validate token        │
│ - Check VM availability  │
│ - HTTPS → mTLS Tunnel   │
└───────────┬─────────────┘
            ▼ mTLS (TLS 1.3, 4096-bit RSA)
┌─────────────────────────┐
│ VM (Spot L4 GPU)        │
│ - mTLS Server Cert      │
│ - Models on local SSD   │
│ - Shielded VM (Secure Boot+TPM)
└─────────────────────────┘
      ▼
Your Response (prompt and results stay private)
```

## 🚀 Quick Start

```bash
# Deploy infrastructure
make init-terraform
make deploy

# Configure your environment
export LLM_PROXY_URL=$(terraform output -raw function_url)
export LLM_API_TOKEN=$(terraform output -raw api_token)

# Use it with anything
curl -H "Authorization: Bearer $LLM_API_TOKEN" $LLM_PROXY_URL/api/generate \
  -d '{"prompt":"Analyze this bank statement:","content":"<paste your sensitive data here>"}'

# Pull your own models
curl -X POST $LLM_PROXY_URL/api/pull \
  -H "Authorization: Bearer $LLM_API_TOKEN" \
  -d '{"name":"llama3.2:1b"}'
```

## 🏛️ Architecture

```
Your Application
      ▼
[External API Token]
      ▼
┌─────────────────────────┐
│ Cloud Functions (Proxy) │
│ - Validate token        │
│ - Check VM availability  │
│ - HTTPS → mTLS Tunnel   │
└───────────┬─────────────┘
            ▼ mTLS (TLS 1.3, 4096-bit RSA)
┌─────────────────────────┐
│ VM (Spot L4 GPU)        │
│ - mTLS Server Cert      │
│ - Models on local SSD   │
│ - Shielded VM (Secure Boot+TPM)
└─────────────────────────┘
      ▼
Your Response (prompt and results stay private)
```

## 🛡️ Security Guarantees

- ✅ **Data Sovereignty**: Your prompts and inference results never leave your infrastructure
- ✅ **Zero Data Logging**: No user data stored, no logs, no metrics captured
- ✅ **HSM-Protected Secrets**: Customer-managed keys with hardware encryption
- ✅ **mTLS Everywhere**: Mutual authentication for all connections
- ✅ **At-Rest Encryption**: Secrets encrypted with hardware-managed keys
- ✅ **Spot VM Savings**: Up to 80% reduction vs on-demand pricing

## 🚀 Quick Start

```bash
# Deploy infrastructure
make init-terraform
make deploy

# Configure your environment
export LLM_PROXY_URL=$(terraform output -raw function_url)
export LLM_API_TOKEN=$(terraform output -raw api_token)

# Use it
curl -H "Authorization: Bearer $LLM_API_TOKEN" $LLM_PROXY_URL/api/generate \
  -d '{"prompt":"Hello","model":"glm-4.7-flash"}'

# Pull models
curl -X POST $LLM_PROXY_URL/api/pull \
  -H "Authorization: Bearer $LLM_API_TOKEN" \
  -d '{"name":"llama3.2:1b"}'
```

## 📦 Features

### Privacy First
- **Zero data logging**: No prompts, no responses, no user data stored anywhere
- **Total sovereignty**: Your data exists only in your infrastructure
- **No data training**: Your inputs never become another company's model outputs
- **No vendor access**: Even cloud providers can't access your decrypted data

### Automatic Operation
- **Idle shutdown**: VM stays off between requests - no electricity waste
- **First-boot setup**: 6-11 minutes once, subsequent boots take 30-60 seconds
- **Auto-rotation**: Secrets and certificates updated automatically
- **State management**: VM starts instantly when you need it again

### Real Security
- **mTLS everywhere**: Every request authenticated end-to-end
- **Dual-layer security**: External token AND internal certificate required
- **Hardware encryption**: HSM-protected keys, at-rest and in-transit
- **Network isolation**: Dedicated infrastructure, no public exposure

## 📁 Project Structure

```
private-llm/
├── terraform.tf              # Version constraints
├── variables.tf              # Cloud-agnostic variables
├── modules/gcp/              # GCP implementation
│   ├── compute.tf           # VM with GPU and Shielded config
│   ├── secrets.tf           # KMS and Secret Manager
│   ├── network.tf           # VPC with dedicated subnet
│   └── functions.tf         # Cloud Functions Gen2
├── function/                 # Go proxy + rotation
│   ├── main.go              # API proxy with token/mTLS validation
│   └── rotation.go          # Automatic secret rotation
└── config/                   # VM startup + Caddyfile
```

*Architecture designed for portability across clouds: GCP supported now, AWS coming soon*

## 📊 Monthly Cost

- Spot VM: Low hourly rate ($1-2/hour depending on configuration)
- Storage: Fixed monthly (~$17/100GB)
- Functions: Minimal (~$0.50)
- Total: $20-80/month (varies by usage)

**ROI**: Pay only when you use it. No idle costs.

## 🔧 Troubleshooting

### VM not responding?
```bash
gcloud compute instances tail-serial-port-output private-llm-vm --zone=us-central1-a
```

### Check provisioning status
```bash
gcloud firestore documents describe \
  projects/YOUR-PROJECT/databases/private-llm/documents/vm_state/private-llm-vm
```

### Pull models manually
```bash
curl -X POST $LLM_PROXY_URL/api/pull \
  -H "Authorization: Bearer $LLM_API_TOKEN" \
  -d '{"name":"your-model-name"}'
```

## 🌐 Cloud Support

- ✅ **Google Cloud Platform**: Fully supported
- 🚧 **AWS**: Coming soon
- 🚧 **Azure**: Coming soon

## 📖 Technical Details

### Token Lifecycle
- **External token** (user): You generate it, Terraform outputs it
- **Internal token** (infrastructure): Auto-rotated every 2 hours
- **mTLS CA**: 10-year validity, immutable
- **Server/client certs**: 1-week validity, auto-rotated

### First Boot Timeline
1. VM boot: ~30s
2. Cloud-init: ~5s
3. Installation: 6-11 min (once)
4. Ollama: ~15s
5. Model warmup: Background

**Subsequent boots**: 30-60s (skips installation, ready in seconds)

## 🎯 Usage Patterns

**Daily developer**: Runs briefly, VM stays idle - minimal cost
**Research**: Works multiple hours - model always ready
**Enterprise**: Continuous access - cost-efficient scaling

Whatever your use case, you only pay for when you use it.

## 🤝 Contributing

1. Ensure code passes CI checks: `make test`
2. Verify Terraform formatting: `terraform fmt -check .`
3. No secrets in code - use Environment variables
4. Follow existing security patterns

## 📝 License

Reference implementation for private LLM deployment. Configure your own cloud project for production use.

---

**One-line promise**: Your data stays private, your time stays yours. Deploy and forget with zero data leakage.