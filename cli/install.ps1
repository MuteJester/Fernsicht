# fernsicht CLI installer for Windows.
#
# Usage:
#   irm https://github.com/MuteJester/Fernsicht/releases/latest/download/install.ps1 | iex
#
# Environment / parameters:
#   $env:VERSION       — release tag (default: latest stable, e.g. cli/v0.1.0).
#   $env:INSTALL_DIR   — install location (default: $env:LOCALAPPDATA\fernsicht\bin).
#   $env:SKIP_VERIFY   — if "1", skip SHA256 verification (NOT recommended).
#   $env:BASE_URL      — override the GitHub release host (testing only).
#
# Behavior parity with install.sh:
#   1. Detect arch (Windows is amd64 only for now).
#   2. Resolve install dir; create if missing.
#   3. Download SHA256SUMS for the chosen release.
#   4. Download the platform binary.
#   5. Verify SHA256.
#   6. Smoke-test the binary (--version returns 0).
#   7. Atomic install.
#   8. PATH guidance.
#
# Cosign verification (parity with .sh): currently NOT implemented in
# .ps1 — most Windows users don't have cosign on PATH and adding it
# adds friction. SHA256 + HTTPS to GitHub gives strong-enough
# integrity for v1; full cosign on Windows lands in Phase 6 polish.

$ErrorActionPreference = 'Stop'

# --- Configuration ---

$Repo            = 'MuteJester/Fernsicht'
$BinName         = 'fernsicht'
$DefaultInstallDir = Join-Path $env:LOCALAPPDATA 'fernsicht\bin'

$Version         = if ($env:VERSION)       { $env:VERSION }       else { '' }
$InstallDir      = if ($env:INSTALL_DIR)   { $env:INSTALL_DIR }   else { $DefaultInstallDir }
$SkipVerify      = if ($env:SKIP_VERIFY)   { $env:SKIP_VERIFY }   else { '' }
$BaseUrl         = if ($env:BASE_URL)      { $env:BASE_URL }      else { "https://github.com/$Repo/releases" }

# --- Helpers ---

function Step($msg)  { Write-Host "→ $msg" -ForegroundColor Green }
function Warn($msg)  { Write-Host "! $msg" -ForegroundColor Yellow }
function Fail($msg)  { Write-Host "✘ $msg" -ForegroundColor Red; exit 1 }
function Ok($msg)    { Write-Host "✓ $msg" -ForegroundColor Green }

# --- Detect platform ---

$arch = (Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue).OSArchitecture
if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
if ($arch -notmatch 'x64|AMD64|64') {
    Fail "Unsupported architecture: $arch (only amd64 / x86_64 supported on Windows for now)"
}
$Platform = 'windows-amd64'
$AssetName = "$BinName-$Platform.exe"
$BinTarget = Join-Path $InstallDir "$BinName.exe"

Write-Host "Fernsicht CLI installer" -ForegroundColor Cyan
Write-Host "  repo: github.com/$Repo"
Write-Host ""

# --- Resolve version ---

if (-not $Version) {
    Step "Resolving latest release..."
    try {
        $resp = Invoke-WebRequest -Uri "$BaseUrl/latest/download/SHA256SUMS" -MaximumRedirection 5 -UseBasicParsing
        # The response URL after redirect contains the version tag.
        $finalUrl = $resp.BaseResponse.ResponseUri.AbsoluteUri
        if ($finalUrl -match '/releases/download/(cli/v[^/]+)/') {
            $Tag = $Matches[1]
        } else {
            Fail "Could not parse release tag from redirect: $finalUrl"
        }
    } catch {
        Fail "Could not resolve latest release: $_"
    }
    Ok "Latest release: $Tag"
} else {
    # Normalize: accept "v0.1.0", "0.1.0", "cli/v0.1.0".
    switch -Wildcard ($Version) {
        'cli/*' { $Tag = $Version }
        'v*'    { $Tag = "cli/$Version" }
        default { $Tag = "cli/v$Version" }
    }
}

$ReleaseBase = "$BaseUrl/download/$Tag"

# --- Prepare temp dir + install dir ---

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

try {
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    # --- Download SHA256SUMS ---

    Step "Downloading SHA256SUMS..."
    Invoke-WebRequest -Uri "$ReleaseBase/SHA256SUMS" -OutFile "$tmp\SHA256SUMS" -UseBasicParsing

    # --- Download binary ---

    Step "Downloading binary ($AssetName)..."
    Invoke-WebRequest -Uri "$ReleaseBase/$AssetName" -OutFile "$tmp\$AssetName" -UseBasicParsing

    # --- Verify SHA256 ---

    if (-not $SkipVerify) {
        Step "Verifying SHA256..."
        $expected = $null
        # Pre-compute the escaped asset name OUTSIDE the regex string.
        # Windows PowerShell 5.1 can't parse `[regex]::Escape(...)`
        # inside a double-quoted string's subexpression — it misreads
        # the leading `[` as a type-cast token and the whole script
        # fails at parse time.
        $assetPattern = [regex]::Escape($AssetName)
        Get-Content "$tmp\SHA256SUMS" | ForEach-Object {
            if ($_ -match "^([0-9a-f]+)\s+$assetPattern$") {
                $expected = $Matches[1]
            }
        }
        if (-not $expected) {
            Fail "No checksum entry for $AssetName in SHA256SUMS"
        }
        $actual = (Get-FileHash "$tmp\$AssetName" -Algorithm SHA256).Hash.ToLower()
        if ($expected -ne $actual) {
            Write-Host "✘ SHA256 MISMATCH for $AssetName" -ForegroundColor Red
            Write-Host "  expected: $expected"
            Write-Host "  actual:   $actual"
            Fail "DO NOT USE THIS BINARY — re-download or report at github.com/$Repo/issues"
        }
    } else {
        Warn "SKIP_VERIFY=1 — bypassing SHA256 verification"
    }

    # --- Smoke test ---

    Step "Smoke-testing binary..."
    $proc = Start-Process -FilePath "$tmp\$AssetName" -ArgumentList '--version' -Wait -PassThru -NoNewWindow -RedirectStandardOutput "$tmp\smoke.out" -RedirectStandardError "$tmp\smoke.err"
    if ($proc.ExitCode -ne 0) {
        Fail "Downloaded binary failed --version smoke test (exit $($proc.ExitCode))"
    }

    # --- Install (atomic-ish: move to target) ---

    Step "Installing to $BinTarget..."
    if (Test-Path $BinTarget) {
        Remove-Item $BinTarget -Force
    }
    Move-Item "$tmp\$AssetName" $BinTarget

    # --- Post-install ---

    Write-Host ""
    Ok "fernsicht installed at $BinTarget"
    Write-Host ""

    # PATH check.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$InstallDir*") {
        Warn "$InstallDir is not on your PATH"
        Warn "to add it permanently:"
        # Print the snippet as a single-quoted string literal to avoid
        # PowerShell 5.1 parsing the embedded [Environment]::... as a
        # real type cast when interpolating the double-quoted form.
        $pathSnippet = '  [Environment]::SetEnvironmentVariable(''Path'', $env:Path + '';' + $InstallDir + ''', ''User'')'
        Write-Host $pathSnippet -ForegroundColor Cyan
        Write-Host "  (then open a new shell for the change to take effect)"
        Write-Host ""
    }

    Write-Host "Quick start:" -ForegroundColor Cyan
    Write-Host "  fernsicht run -- echo hello"
    Write-Host ""
    Write-Host "More:" -ForegroundColor Cyan
    Write-Host "  fernsicht --help"
    Write-Host "  fernsicht run --help"
    Write-Host ""
    Write-Host "Privacy: no telemetry, no accounts. Progress flows P2P via WebRTC." -ForegroundColor DarkGray
} finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
