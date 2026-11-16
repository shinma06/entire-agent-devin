# Installs entire and entire-agent-devin into the user directories the Devin hook
# command searches ($HOME\.local\bin, $HOME\go\bin; Devin runs hooks with Git
# Bash, where they are ~/.local/bin and ~/go/bin), so no PATH change is needed.
# Usage: devin-setup.ps1 [--checkpoint-repo owner/repo]   (bootstrap args, run in a repo)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$env:PATH = "$env:PATH;$HOME\.local\bin;$HOME\go\bin;$HOME\go-sdk\go\bin"

if (-not (Get-Command entire -ErrorAction SilentlyContinue)) {
    $installer = Join-Path $env:TEMP 'entire-install.ps1'
    Invoke-WebRequest https://entire.com/install.ps1 -UseBasicParsing -OutFile $installer
    & $installer -InstallDir (Join-Path $HOME '.local\bin') -NoPathUpdate
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
    $version = ((Invoke-WebRequest 'https://go.dev/VERSION?m=text' -UseBasicParsing).Content -split "`n")[0].Trim()
    $zip = Join-Path $env:TEMP "$version.zip"
    Invoke-WebRequest "https://go.dev/dl/$version.windows-$arch.zip" -UseBasicParsing -OutFile $zip
    Expand-Archive $zip -DestinationPath (Join-Path $HOME 'go-sdk') -Force
}

$ref = if ($env:ENTIRE_AGENT_DEVIN_VERSION) { $env:ENTIRE_AGENT_DEVIN_VERSION } else { 'latest' }
go install "github.com/shinma06/entire-agent-devin/cmd/entire-agent-devin@$ref"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

entire version
(Get-Command entire-agent-devin).Source

if ($args.Count -gt 0) {
    entire-agent-devin bootstrap @args
    exit $LASTEXITCODE
}
