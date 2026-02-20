Name:           private-llm
Version:        1.0.0
Release:        1%{?dist}
Summary:        Private LLM Agent Proxy with mTLS and auto-scaling GPU VM
License:        MIT
URL:            https://github.com/stewartpark/private-llm
BuildArch:      x86_64

Requires:       systemd

%description
A local proxy that connects to a remote GCP VM running Ollama with
zero-trust mTLS authentication and auto-scale-to-zero infrastructure.

%prep
%setup -q

%build
# No build needed - binary is pre-built

%install
mkdir -p %{buildroot}/usr/bin
mkdir -p %{buildroot}/etc/private-llm
mkdir -p %{buildroot}/var/lib/private-llm
mkdir -p %{buildroot}/usr/lib/systemd/system

cp private-llm %{buildroot}/usr/bin/
cp systemd/private-llm.service %{buildroot}/usr/lib/systemd/system/

%pre
# Create the private-llm user and group if they don't exist
getent group private-llm > /dev/null || groupadd --system private-llm
getent passwd private-llm > /dev/null || useradd --system --gid private-llm --home-dir /var/lib/private-llm --no-create-home private-llm

%post
# Create directories with proper permissions
mkdir -p /var/lib/private-llm
chown private-llm:private-llm /var/lib/private-llm
chmod 755 /var/lib/private-llm

mkdir -p /etc/private-llm
chown private-llm:private-llm /etc/private-llm
chmod 755 /etc/private-llm

# Reload systemd daemon
systemctl daemon-reload 2>/dev/null || true
systemctl enable private-llm.service 2>/dev/null || true

%preun
systemctl stop private-llm.service 2>/dev/null || true
systemctl disable private-llm.service 2>/dev/null || true

%postun
systemctl daemon-reload 2>/dev/null || true

%files
/usr/bin/private-llm
%dir /etc/private-llm
%dir /var/lib/private-llm
/usr/lib/systemd/system/private-llm.service

%changelog
* Wed Feb 19 2026 Stewart Park <stewart@private-llm> - 1.0.0-1
- Initial package
