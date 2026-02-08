#!/bin/sh
set -eu

REPO="stewartpark/private-llm"
INSTALL_DIR="${HOME}/.local/bin"

# Detect OS
OS="$(uname -s)"
case "${OS}" in
  Darwin)  OS="darwin" ;;
  Linux)   OS="linux" ;;
  MINGW*|MSYS*|CYGWIN*) OS="windows" ;;
  *) echo "Unsupported OS: ${OS}" >&2; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac

# Windows only ships amd64
if [ "${OS}" = "windows" ] && [ "${ARCH}" != "amd64" ]; then
  echo "Unsupported: windows/${ARCH} (only amd64 available)" >&2
  exit 1
fi

EXT="tar.gz"
if [ "${OS}" = "windows" ]; then
  EXT="zip"
fi

echo "Detecting latest release..."
TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d '"' -f 4)"
if [ -z "${TAG}" ]; then
  echo "Failed to fetch latest release tag" >&2
  exit 1
fi
VERSION="${TAG#v}"

ARCHIVE="private-llm_${VERSION}_${OS}_${ARCH}.${EXT}"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ARCHIVE}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

echo "Downloading ${ARCHIVE}..."
curl -fsSL -o "${TMPDIR}/${ARCHIVE}" "${URL}"

echo "Extracting..."
if [ "${EXT}" = "zip" ]; then
  unzip -q "${TMPDIR}/${ARCHIVE}" -d "${TMPDIR}/out"
else
  mkdir -p "${TMPDIR}/out"
  tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}/out"
fi

BINARY="private-llm"
if [ "${OS}" = "windows" ]; then
  BINARY="private-llm.exe"
fi

mkdir -p "${INSTALL_DIR}"
mv "${TMPDIR}/out/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

echo "Installed private-llm ${TAG} to ${INSTALL_DIR}/${BINARY}"

if ! echo "${PATH}" | tr ':' '\n' | grep -qx "${INSTALL_DIR}"; then
  echo ""
  echo "NOTE: ${INSTALL_DIR} is not in your PATH."
  echo "Add it with:  export PATH=\"${INSTALL_DIR}:\${PATH}\""
fi
