#!/usr/bin/env bash
# Installs entire and entire-agent-devin into the user directories the Devin hook
# command searches (~/.local/bin, ~/go/bin), so no PATH or profile change is
# needed. Linux and macOS; on Windows use devin-setup.ps1.
# Usage: devin-setup.sh [--checkpoint-repo owner/repo]   (bootstrap args, run in a repo)
set -euo pipefail

export PATH="$PATH:$HOME/.local/bin:$HOME/go/bin:$HOME/go-sdk/go/bin:/opt/homebrew/bin:/usr/local/bin"

if ! command -v entire >/dev/null 2>&1; then
  curl -fsSL https://entire.com/install.sh | bash
fi

if ! command -v go >/dev/null 2>&1; then
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  version="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n1)"
  mkdir -p "$HOME/go-sdk"
  curl -fsSL "https://go.dev/dl/${version}.${os}-${arch}.tar.gz" | tar -xz -C "$HOME/go-sdk"
fi

export GOBIN="$HOME/go/bin"
go install "github.com/shinma06/entire-agent-devin/cmd/entire-agent-devin@${ENTIRE_AGENT_DEVIN_VERSION:-latest}"

entire version
command -v entire-agent-devin

if [ "$#" -gt 0 ]; then
  entire-agent-devin bootstrap "$@"
fi
