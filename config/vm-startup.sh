#!/bin/bash
# Cloud-init installation script for Private LLM VM
# Merges all installation steps (01-09) with Firestore state tracking
# Idempotent with step markers for resilience

set -e

export DEBIAN_FRONTEND=noninteractive
INSTALL_DIR="/var/lib/private-llm"

echo "========================================="
echo "Private LLM VM Installation Started"
echo "Time: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "========================================="

# Get project ID and VM name from metadata
PROJECT_ID=$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/project/project-id)
VM_NAME=$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/name)

# Create installation directory
mkdir -p "$INSTALL_DIR"

# ============================================
# Prevent dpkg lock contention (runs every boot)
# ============================================
echo "[INIT] Stopping apt/dpkg background services..."
sudo systemctl stop unattended-upgrades 2>/dev/null || true
sudo systemctl stop apt-daily.service 2>/dev/null || true
sudo systemctl stop apt-daily-upgrade.service 2>/dev/null || true

echo "[INIT] Waiting for dpkg lock to be available..."
for i in {1..60}; do
    if ! sudo fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 && \
       ! sudo fuser /var/lib/dpkg/lock >/dev/null 2>&1 && \
       ! sudo fuser /var/lib/apt/lists/lock >/dev/null 2>&1; then
        echo "[INIT] dpkg lock is available"
        break
    fi
    echo "[INIT] Waiting for dpkg lock... attempt $i/60"
    sleep 5
done

# Kill any remaining apt/dpkg processes
sudo pkill -9 unattended-upgr 2>/dev/null || true
sudo pkill -9 apt 2>/dev/null || true
sleep 1

# Disable unattended-upgrades permanently (idempotent)
sudo systemctl disable unattended-upgrades 2>/dev/null || true
sudo systemctl mask unattended-upgrades 2>/dev/null || true
sudo systemctl disable apt-daily.timer 2>/dev/null || true
sudo systemctl disable apt-daily-upgrade.timer 2>/dev/null || true

# Pre-step: Ensure Firestore document exists with correct state
# Wait for gcloud to be ready and authenticate
echo "[PRE] Waiting for gcloud authentication..."
for i in {1..30}; do
    if gcloud auth list --filter=status:ACTIVE --format="value(account)" 2>/dev/null | grep -q "gserviceaccount.com"; then
        echo "[PRE] gcloud authenticated"
        break
    fi
    echo "[PRE] Waiting for gcloud auth... attempt $i/30"
    sleep 2
done

# Get access token for Firestore API calls
ACCESS_TOKEN=$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token | grep -o '"access_token":"[^"]*' | cut -d'"' -f4)

# Check if already provisioned (installation complete marker exists)
if [ -f "$INSTALL_DIR/.installation-complete" ]; then
    echo "[PRE] VM already provisioned, ensuring Firestore doc exists with provisioned=true..."
    # Just ensure document exists with provisioned=true
    curl -X PATCH \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -H "Content-Type: application/json" \
        "https://firestore.googleapis.com/v1/projects/$PROJECT_ID/databases/private-llm/documents/vm_state/$VM_NAME?updateMask.fieldPaths=provisioned" \
        -d '{
          "fields": {
            "provisioned": {"booleanValue": true}
          }
        }'
elif [ ! -f "$INSTALL_DIR/.provisioning-started" ]; then
    echo "[PRE] First boot - creating Firestore document for provisioning..."
    # First time provisioning - set provisioning fields only
    curl -X PATCH \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -H "Content-Type: application/json" \
        "https://firestore.googleapis.com/v1/projects/$PROJECT_ID/databases/private-llm/documents/vm_state/$VM_NAME?updateMask.fieldPaths=provisioned&updateMask.fieldPaths=provisioning_started_unix&updateMask.fieldPaths=provisioning_completed_unix" \
        -d '{
          "fields": {
            "provisioned": {"booleanValue": false},
            "provisioning_started_unix": {"integerValue": "'$(date +%s)'"},
            "provisioning_completed_unix": {"integerValue": "0"}
          }
        }'
    touch "$INSTALL_DIR/.provisioning-started"
else
    echo "[PRE] Provisioning already in progress, skipping Firestore update..."
    # Provisioning in progress - do nothing
fi

# Helper function to wait for apt lock
wait_for_apt_lock() {
    echo "[APT] Stopping apt background services..."
    sudo systemctl stop unattended-upgrades 2>/dev/null || true
    sudo systemctl stop apt-daily.service 2>/dev/null || true
    sudo systemctl stop apt-daily-upgrade.service 2>/dev/null || true

    echo "[APT] Waiting for dpkg lock..."
    for i in {1..60}; do
        if ! sudo fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 && \
           ! sudo fuser /var/lib/dpkg/lock >/dev/null 2>&1 && \
           ! sudo fuser /var/lib/apt/lists/lock >/dev/null 2>&1; then
            echo "[APT] Lock acquired"
            return 0
        fi
        echo "[APT] Waiting... attempt $i/60"
        sleep 5
    done

    # Force kill if still locked
    echo "[APT] Force killing apt processes..."
    sudo pkill -9 -f 'apt|dpkg' 2>/dev/null || true
    sleep 2
}

# Step 1: Install base packages
if [ ! -f "$INSTALL_DIR/.step1-base-packages" ]; then
    echo "[STEP 1] Installing base packages (gcsfuse, Caddy, Ollama, Cloud SDK, Ops Agent)..."

    # Remove bullseye-backports (no longer available)
    sudo rm -f /etc/apt/sources.list.d/bullseye-backports.list
    sudo sed -i '/bullseye-backports/d' /etc/apt/sources.list

    # Install gcsfuse repository (kept for potential future use)
    export GCSFUSE_REPO=gcsfuse-$(lsb_release -c -s)
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.asc] https://packages.cloud.google.com/apt $GCSFUSE_REPO main" | sudo tee /etc/apt/sources.list.d/gcsfuse.list
    curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | sudo tee /usr/share/keyrings/cloud.google.asc > /dev/null

    # Install Caddy
    sudo mkdir -p /usr/share/keyrings
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list

    # Install Google Cloud SDK repository
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" | sudo tee /etc/apt/sources.list.d/google-cloud-sdk.list
    curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | sudo gpg --batch --yes --dearmor -o /usr/share/keyrings/cloud.google.gpg

    # Wait for apt lock immediately before using apt
    wait_for_apt_lock

    # Install packages
    sudo apt-get update
    sudo apt-get install -y --no-install-recommends \
        caddy \
        google-cloud-sdk

    # Install Google Cloud Ops Agent (uses apt internally)
    wait_for_apt_lock
    curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh
    sudo bash add-google-cloud-ops-agent-repo.sh --also-install
    rm -f add-google-cloud-ops-agent-repo.sh

    # Install Ollama pre-release (output logged to prevent metadata runner buffer overflow)
    if ! curl -fsSL https://ollama.com/install.sh | sh > /var/log/ollama-install.log 2>&1; then
        echo "[STEP 1] ERROR: Ollama installation failed"
        echo "[STEP 1] Last 100 lines of /var/log/ollama-install.log:"
        tail -n 100 /var/log/ollama-install.log
        exit 1
    fi

    touch "$INSTALL_DIR/.step1-base-packages"
    echo "[STEP 1] Complete"
fi

# Step 2: Install bootstrap service
if [ ! -f "$INSTALL_DIR/.step2-bootstrap-service" ]; then
    echo "[STEP 2] Installing bootstrap service (fetch secrets, template configs)..."

    # Create bootstrap script
    sudo tee /usr/local/bin/private-llm-bootstrap.sh > /dev/null <<'EOF'
#!/bin/bash
# Fetch secrets from Secret Manager and template configs
PROJECT_ID=$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/project/project-id)

# Fetch mTLS certificates and internal token
gcloud secrets versions access latest --secret=private-llm-ca-cert --project=$PROJECT_ID > /tmp/ca.crt
gcloud secrets versions access latest --secret=private-llm-server-cert --project=$PROJECT_ID > /tmp/server.crt
gcloud secrets versions access latest --secret=private-llm-server-key --project=$PROJECT_ID > /tmp/server.key
gcloud secrets versions access latest --secret=private-llm-internal-token --project=$PROJECT_ID > /tmp/internal-token

# Move certs to proper location
sudo mkdir -p /etc/caddy/certs
sudo mv /tmp/ca.crt /etc/caddy/certs/
sudo mv /tmp/server.crt /etc/caddy/certs/
sudo mv /tmp/server.key /etc/caddy/certs/
sudo chown -R caddy:caddy /etc/caddy/certs
sudo chmod 600 /etc/caddy/certs/server.key
sudo chmod 644 /etc/caddy/certs/server.crt
sudo chmod 644 /etc/caddy/certs/ca.crt

# Template Caddyfile with internal token
INTERNAL_TOKEN=$(cat /tmp/internal-token)
sudo mkdir -p /etc/caddy
curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/attributes/caddyfile | \
    sed "s|{{INTERNAL_TOKEN}}|${INTERNAL_TOKEN}|g" | \
    sudo tee /etc/caddy/Caddyfile > /dev/null
sudo chown caddy:caddy /etc/caddy/Caddyfile
sudo chmod 644 /etc/caddy/Caddyfile

rm -f /tmp/internal-token

# Fetch context length and max batch size from metadata and update Ollama service environment
CONTEXT_LENGTH=$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/attributes/context-length)
sudo mkdir -p /etc/systemd/system/ollama.service.d
sudo tee /etc/systemd/system/ollama.service.d/override.conf > /dev/null <<ENVEOF
[Unit]
After=private-llm-bootstrap.service
Requires=private-llm-bootstrap.service

[Service]
Environment="OLLAMA_CONTEXT_LENGTH=${CONTEXT_LENGTH}"
Environment="OLLAMA_KEEP_ALIVE=-1"
Environment="OLLAMA_CUDA_GRAPHS=1"
Environment="OLLAMA_NUM_THREADS=8"
ENVEOF

sudo systemctl daemon-reload

# Don't restart services - they will start AFTER bootstrap completes (Before= directive)
EOF

    sudo chmod +x /usr/local/bin/private-llm-bootstrap.sh

    # Create systemd service
    sudo tee /etc/systemd/system/private-llm-bootstrap.service > /dev/null <<'EOF'
[Unit]
Description=Private LLM Bootstrap (fetch secrets, template configs)
Before=caddy.service ollama.service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/private-llm-bootstrap.sh
RemainAfterExit=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    sudo systemctl enable private-llm-bootstrap.service

    touch "$INSTALL_DIR/.step2-bootstrap-service"
    echo "[STEP 2] Complete"
fi

# Step 3: Configure service dependencies (skip GCS mount service - using local disk)
if [ ! -f "$INSTALL_DIR/.step3-service-dependencies" ]; then
    echo "[STEP 3] Configuring service dependencies..."

    # Note: Ollama environment (context length) is set dynamically by bootstrap service
    # This ensures fresh values on every boot without reinstallation

    # Configure Caddy to start after bootstrap
    sudo mkdir -p /etc/systemd/system/caddy.service.d
    sudo tee /etc/systemd/system/caddy.service.d/override.conf > /dev/null <<'EOF'
[Unit]
After=private-llm-bootstrap.service
Requires=private-llm-bootstrap.service
EOF

    sudo systemctl daemon-reload

    touch "$INSTALL_DIR/.step3-service-dependencies"
    echo "[STEP 3] Complete"
fi

# Step 4: Install integrity monitoring
if [ ! -f "$INSTALL_DIR/.step4-integrity-monitor" ]; then
    echo "[STEP 4] Installing integrity monitoring..."

    # Create baseline script (updated to remove gcsfuse references)
    sudo tee /usr/local/bin/integrity-baseline.sh > /dev/null <<'EOF'
#!/bin/bash
# Create baseline checksums for critical binaries
BASELINE_FILE="/var/lib/integrity/baseline.txt"
mkdir -p /var/lib/integrity

# Only checksum files that exist
> $BASELINE_FILE
for file in /usr/local/bin/ollama /usr/bin/caddy /usr/local/bin/private-llm-bootstrap.sh; do
    if [ -f "$file" ]; then
        sha256sum "$file" >> $BASELINE_FILE
    fi
done

chmod 600 $BASELINE_FILE
EOF

    # Create monitor script
    sudo tee /usr/local/bin/integrity-monitor.sh > /dev/null <<'EOF'
#!/bin/bash
# Check binary integrity against baseline
BASELINE_FILE="/var/lib/integrity/baseline.txt"

if [ ! -f "$BASELINE_FILE" ]; then
    echo "Baseline not found, creating..."
    /usr/local/bin/integrity-baseline.sh
    exit 0
fi

# Verify checksums
sha256sum -c $BASELINE_FILE --quiet || {
    echo "ALERT: Binary integrity check failed!"
    logger -t integrity-monitor "CRITICAL: Binary integrity violation detected"
    exit 1
}

echo "Integrity check passed"
EOF

    sudo chmod +x /usr/local/bin/integrity-baseline.sh
    sudo chmod +x /usr/local/bin/integrity-monitor.sh

    # Create systemd service
    sudo tee /etc/systemd/system/integrity-monitor.service > /dev/null <<'EOF'
[Unit]
Description=Binary Integrity Monitor
After=network.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/integrity-monitor.sh
StandardOutput=journal
StandardError=journal
EOF

    # Create systemd timer (run every 5 minutes)
    sudo tee /etc/systemd/system/integrity-monitor.timer > /dev/null <<'EOF'
[Unit]
Description=Binary Integrity Monitor Timer

[Timer]
OnBootSec=5min
OnUnitActiveSec=5min
Unit=integrity-monitor.service

[Install]
WantedBy=timers.target
EOF

    sudo systemctl daemon-reload
    sudo systemctl enable integrity-monitor.timer

    touch "$INSTALL_DIR/.step4-integrity-monitor"
    echo "[STEP 4] Complete"
fi

# Step 5: Remove legacy agents
if [ ! -f "$INSTALL_DIR/.step5-remove-legacy" ]; then
    echo "[STEP 5] Removing legacy logging agents..."

    wait_for_apt_lock

    # Remove legacy fluentd if present (replaced by ops-agent)
    sudo apt-get purge -y google-fluentd 2>/dev/null || true
    sudo systemctl mask google-fluentd 2>/dev/null || true

    # Remove stackdriver-agent if present (replaced by ops-agent)
    sudo apt-get purge -y stackdriver-agent 2>/dev/null || true

    touch "$INSTALL_DIR/.step5-remove-legacy"
    echo "[STEP 5] Complete"
fi

# Step 6: Optimize boot + Disable SSH
if [ ! -f "$INSTALL_DIR/.step6-optimize-boot" ]; then
    echo "[STEP 6] Optimizing boot time and disabling SSH..."

    # Reduce GRUB timeout to 0 (saves ~5s)
    sudo sed -i 's/GRUB_TIMEOUT=.*/GRUB_TIMEOUT=0/' /etc/default/grub
    sudo update-grub

    # Disable unnecessary services that slow boot
    sudo systemctl disable snapd 2>/dev/null || true
    sudo systemctl disable snapd.seeded 2>/dev/null || true
    sudo systemctl disable ModemManager 2>/dev/null || true
    sudo systemctl disable multipathd 2>/dev/null || true
    sudo systemctl disable NetworkManager-wait-online 2>/dev/null || true

    # Reduce systemd default timeout (from 90s to 15s)
    sudo mkdir -p /etc/systemd/system.conf.d
    sudo tee /etc/systemd/system.conf.d/timeout.conf > /dev/null <<EOF
[Manager]
DefaultTimeoutStartSec=15s
DefaultTimeoutStopSec=15s
EOF

    # Optimize network timeout (GCE provides DHCP instantly)
    sudo mkdir -p /etc/systemd/network
    sudo tee /etc/systemd/network/dhcp.network > /dev/null <<EOF
[Match]
Name=en*

[Network]
DHCP=yes

[DHCP]
ClientIdentifier=mac
Anonymize=no
RouteMetric=100
EOF

    # Disable SSH for security (serial console only for debugging)
    sudo systemctl stop ssh || true
    sudo systemctl disable ssh || true
    sudo systemctl mask ssh || true

    touch "$INSTALL_DIR/.step6-optimize-boot"
    echo "[STEP 6] Complete"
fi

# Step 7: Cleanup
if [ ! -f "$INSTALL_DIR/.step7-cleanup" ]; then
    echo "[STEP 7] Cleanup..."

    wait_for_apt_lock

    # Remove unnecessary packages
    sudo apt-get autoremove --purge -y
    sudo apt-get clean
    sudo rm -rf /var/lib/apt/lists/*

    # Remove documentation and man pages (saves ~200MB)
    sudo rm -rf /usr/share/doc/* /usr/share/man/*

    # Remove CUDA samples and extras (not needed, saves ~1GB)
    sudo rm -rf /usr/local/cuda-12.8/samples || true
    sudo rm -rf /usr/local/cuda-12.8/extras || true

    # Clear logs
    sudo find /var/log -type f -exec truncate -s 0 {} \;

    # Clear bash history
    sudo rm -f /root/.bash_history /home/*/.bash_history

    # Clear package cache
    sudo rm -rf /var/cache/apt/archives/*

    # Clear temp files
    sudo rm -rf /tmp/* || true
    sudo rm -rf /var/tmp/* || true

    touch "$INSTALL_DIR/.step7-cleanup"
    echo "[STEP 7] Complete"
fi

# Step 8: Enable and start services
if [ ! -f "$INSTALL_DIR/.step8-start-services" ]; then
    echo "[STEP 8] Enabling and starting services..."

    # Start bootstrap service (already enabled in step 2)
    sudo systemctl start private-llm-bootstrap.service

    # Ensure systemd picks up the override.conf created by bootstrap
    sudo systemctl daemon-reload

    # Enable and restart Ollama and Caddy to pick up templated configs
    # Use restart instead of start to force reload of configs
    sudo systemctl enable ollama.service
    sudo systemctl restart ollama.service
    sudo systemctl enable caddy.service
    sudo systemctl restart caddy.service

    # Start integrity monitor timer (already enabled in step 4)
    sudo systemctl start integrity-monitor.timer

    # Wait for Ollama to be fully ready
    echo "[STEP 8] Waiting for Ollama to be ready..."
    MAX_RETRIES=60
    RETRY_COUNT=0
    while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
        if curl -s http://localhost:11434/api/tags >/dev/null 2>&1; then
            echo "[STEP 8] Ollama is ready"
            break
        fi
        echo "[STEP 8] Waiting for Ollama... attempt $((RETRY_COUNT + 1))/$MAX_RETRIES"
        sleep 2
        RETRY_COUNT=$((RETRY_COUNT + 1))
    done

    if [ $RETRY_COUNT -ge $MAX_RETRIES ]; then
        echo "[STEP 8] WARNING: Ollama did not become ready within expected time"
    else
        # Pull the default model from metadata
        MODEL=$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/attributes/model)
        if [ -n "$MODEL" ]; then
            echo "[STEP 8] Pulling default model: $MODEL"
            # Pull model in background to avoid blocking startup completion
            # Run as ollama user (HOME=/usr/share/ollama) to avoid $HOME panic
            (sudo -u ollama ollama pull "$MODEL" && echo "[STEP 8] Model $MODEL pulled successfully") &
        else
            echo "[STEP 8] No default model specified in metadata, skipping model pull"
        fi
    fi

    touch "$INSTALL_DIR/.step8-start-services"
    echo "[STEP 8] Complete"
fi

# Post-step: Mark as provisioned in Firestore
if [ ! -f "$INSTALL_DIR/.installation-complete" ]; then
    echo "[POST] Updating Firestore: Mark as provisioned..."
    curl -X PATCH \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -H "Content-Type: application/json" \
        "https://firestore.googleapis.com/v1/projects/$PROJECT_ID/databases/private-llm/documents/vm_state/$VM_NAME?updateMask.fieldPaths=provisioned&updateMask.fieldPaths=provisioning_completed_unix" \
        -d '{
          "fields": {
            "provisioned": {"booleanValue": true},
            "provisioning_completed_unix": {"integerValue": "'$(date +%s)'"}
          }
        }'

    touch "$INSTALL_DIR/.installation-complete"
fi

# ============================================
# Model Warming (runs in background on every boot)
# ============================================
(
    # Get model name from VM metadata
    MODEL=$(curl -s -H "Metadata-Flavor: Google" \
      http://metadata.google.internal/computeMetadata/v1/instance/attributes/model)

    if [ -n "$MODEL" ]; then
        # Wait for Ollama to be ready
        MAX_RETRIES=30
        RETRY_COUNT=0
        while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
            if curl -s http://localhost:11434/api/tags >/dev/null 2>&1; then
                # Warm the model with minimal request
                curl -s -X POST http://localhost:11434/api/generate \
                    -H "Content-Type: application/json" \
                    -d "{\"model\": \"$MODEL\", \"prompt\": \"hi\", \"stream\": false, \"options\": {\"num_predict\": 1}}" \
                    >/dev/null 2>&1
                break
            fi
            sleep 5
            RETRY_COUNT=$((RETRY_COUNT + 1))
        done
    fi
) &

echo "========================================="
echo "Private LLM VM Installation Complete"
echo "Time: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "========================================="
