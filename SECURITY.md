# Security Architecture

Private LLM is built for **zero trust**. Every request is authenticated, encrypted, and validated — no implicit trust anywhere.

---

## Threat Model

### Adversaries We Defend Against

| Threat Actor | Goal | What They Control |
|------|---------|-------------|
| **Cloud Provider (GCP)** | Mass surveillance, data harvesting | All VMs, network, storage, KMS hardware |
| **Supply Chain Attacker** | Compromise via dependencies | Ollama, Caddy, Go runtime, gcloud SDK |
| **Network MITM** | Intercept or modify traffic | Internet between laptop and GCP |
| **Stolen Credentials** | Impersonate user | API keys, tokens if leaked |
| **Compromised VM** | Extract CA key, forge certs | Instance access (should be impossible) |
| **Rogue Insider** | Log prompts, leak data | GCP service account permissions |

---

## Defense Layers

### 1. Cryptographic Boundaries

**Threat:** Cloud provider or network MITM reads your prompts

**Controls:**
- **mTLS encryption** — 4096-bit RSA (exceeds enterprise standard of 2048-bit)
- **TLS 1.3 minimum** — No downgrade attacks
- **End-to-end mTLS** — Both client and server present certs
- **Certificate fingerprint pinning** — Server cert SHA-256 hash verified in memory

**Why It Works:**
```
Local Proxy → TLS 1.3 → mTLS validate → SHA-256 fingerprint check → Proceed or reject
```

Even if GCP hands you a fake server cert, the fingerprint won't match. Attack detected.

---

### 2. CA Key Isolation

**Threat:** Cloud provider or supply chain attacker forges valid certificates

**Control:** CA private key (`~/.config/private-llm/certs/ca.key`) **never leaves your machine**

**Why It Works:**
- CA key never uploaded to GCP, GitHub, or any cloud service
- Server certs are signed by your local CA, then uploaded (not the CA itself)
- If GCP is fully compromised, they still cannot forge certs
- Emergency rotation: `private-llm rotate-mtls-ca` invalidates everything

**Attack Surface:**
```
Your Machine                  GCP
┌──────────────────┐         ┌──────────────────┐
│  CA Private Key  │         │  Secrets Manager │
│  (NEVER leaves)  │────────>│  Server Certs    │
└──────────────────┘  signs  │  (HSM-encrypted) │
         │                    └──────────────────┘
         │                    ┌──────────────────┐
         │                    │  VM (gets certs) │
         └────────────────────┴──────────────────┘
                      never shared
```

---

### 3. HSM-Backed Secrets

**Threat:** Secrets Manager credentials leaked, secrets exfiltrated

**Controls:**
- **KMS HSM key** — Hardware Security Module encrypts all secrets
- **90-day auto-rotation** — KMS key automatically rotated
- **Secret Manager** — Server certs, bearer token stored encrypted
- **VM pulls at boot only** — No persistent creds on disk

**Why It Works:**
```
Secrets Manager → AtRestEncryption → KMS HSM Key (90-day rotation)
```

If Secret Manager is compromised at the API level, attacker needs IAM permissions to read secrets. KMS encryption protects against raw storage-level access (e.g., disk theft).

---

### 4. Shielded VM + vTPM

**Threat:** VM tampering, bootkit injection, malicious image replacement

**Controls:**
- **Secure Boot** — Only signed OS kernels load
- **vTPM** — Virtual Trusted Platform Module for attestation
- **Integrity Monitoring** — VM state verified by GCP
- **Debian GPU image** — Verified, reproducible build

**Why It Works:**
```
Boot → Verify signature (Secure Boot) → vTPM attestation → GCP integrity check → Proceed
```

If someone replaces the boot image or injects a rootkit, Secure Boot rejects it. No boot, no VM.

---

### 5. Dynamic Firewall

**Threat:** External attackers scan and exploit exposed ports

**Controls:**
- **IP-locked firewall rule** — Only your public IP can reach the VM
- **Auto-created on VM start** — Fresh rule each session
- **Auto-deleted on quit** — No dangling firewall rules
- **No SSH** — SSH daemon masked, no shell access

**Why It Works:**
```
Your IP → Firewall (ALLOW) → VM
Other IPs → Firewall (DROP) → Never reaches VM
```

If you change networks, new IP detected, old rule recreated. If you quit, rule deleted immediately. Zero attack surface when idle.

---

### 6. Certificate Aggressive Rotation

**Threat:** Long-lived secrets exfiltrated and used later

**Controls:**
- **Cert rotation on every VM boot** — New server cert, client cert, bearer token each time
- **7-day validity** — Even if VM runs continuously, certs expire weekly
- **CA 10-year validity** — CA rotates only on `rotate-mtls-ca` command

**Why It Works:**
```
VM Boot → Detect cert expiry or first boot → Generate new certs → Upload to Secret Manager → VM restarts with fresh certs
```

Even if attacker steals a server cert today, it's useless tomorrow (VM rebooted, new cert generated).

---

### 7. Binary Integrity Monitoring

**Threat:** Supply chain compromise (Ollama, Caddy, gcloud), binary tampering

**Controls:**
- **Baseline checksums** — SHA-256 hashes of critical binaries at install time
- **5-minute integrity checks** — Timer service verifies binaries match baseline
- **Alert on mismatch** — Logs critical event if binary modified

**Why It Works:**
```
Install → Create baseline (sha256sum) → 5-min timer → Verify checksums → Alert on failure
```

If Ollama or Caddy is updated maliciously (supply chain attack), checksum won't match baseline. Alert logged.

---

### 8. Principle of Least Privilege

**Threat:** Compromised service account escalates to full project access

**Controls:**
- **Minimal service account** — Only `compute`, `secretmanager`, `monitoring` IAM roles
- **No billing/admin access** — Cannot modify billing, delete project, or view other projects
- **OAuth scopes** — `cloud-platform` scope needed for Secret Manager access at boot (actual permissions limited by IAM roles)

**Why It Works:**
```
Service Account → IAM bindings → compute + secretmanager + monitoring only
```

If VM service account is compromised, attacker cannot delete project, access other resources, or modify billing.

---

### 9. No Persistent State

**Threat:** Forensic analysis of VM disk reveals secrets, prompts, or API keys

**Controls:**
- **Certs fetched at boot only** — Not written permanently
- **Bearer token in memory** — Never written to disk
- **Caddy access log** — Metadata only (URLs, status codes, timing), no prompt/response bodies
- **VM auto-stops after idle** — Disk encrypted when stopped

**Why It Works:**
```
Boot → Fetch certs from Secret Manager → Load into memory → Serve requests → Stop VM → Discard ephemeral data
```

If attacker gains disk access after VM stops, secrets and prompts are gone (only encrypted boot disk remains).

---

### 10. Recovery & Incident Response

**Threat:** Compromise detected, need to revoke and rotate

**Controls:**
- **`private-llm rotate-mtls-ca`** — Regenerates CA, invalidates all existing certs
- **`private-llm down && private-llm up`** — Destroys and rebuilds VM from scratch
- **Local state reset** — `rm -rf ~/.config/private-llm/{state,certs}` cleans everything

**Incident Response Timeline:**
```
1. Suspect compromise → Run `rotate-mtls-ca` (invalidates all certs, 2 min)
2. Revoke access → Run `down` (destroys VM, 5 min)
3. Rebuild fresh → Run `up` (provides new VM with new certs, 30 min first boot)
```

Total recovery time: ~1 hour from compromise suspicion → clean state.

---

## Security Controls Matrix

| Threat | Control | Mitigation |
|---------|---------|----------|
| GCP compromise | CA local-only, cert pinning | Cannot forge certs, MITM detected |
| Network MITM | TLS 1.3, fingerprint pinning | Cannot decrypt or inject |
| Stolen credentials | 7-day cert expiry, rotation | Credentials quickly useless |
| VM compromise | Shielded VM, vTPM, integrity monitoring | Boot tampering detected |
| Supply chain attack | Binary checksums, 5-min verification | Tampered binaries detected |
| Excessive permissions | Least privilege SA, scoped OAuth | Cannot escalate beyond VM |
| Disk forensics | No persistent secrets, auto-stop | Nothing to extract when stopped |

---

## Reporting Security Issues

**Do NOT file public issues for security vulnerabilities.**

Email: [hello@stewartjpark.com](mailto:hello@stewartjpark.com)

Please include:
- Description of vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We will respond within 72 hours with acknowledgment and timeline.

---

## Acknowledgments

Security architecture influenced by:
- Stripe's security model (zero trust, aggressive rotation)
- HashiCorp Vault (sealed mode, HSM requirements)
- Tor Project (fingerprint pinning, identity isolation)

---

*Last updated: 2026-03-02*
