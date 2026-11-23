# Release entry template for Windows (PowerShell 5.1+ and 7+).
# Publisher tooling pins exact release version and SHA-256 archive digests.
[CmdletBinding()]
param(
  [switch]$Local,
  [switch]$Server,
  [switch]$Connect,
  [string]$Transport = '',
  [string]$Domain = '',
  [string]$Listen = '127.0.0.1:7331',
  [switch]$Open,
  [switch]$Start,
  [switch]$Stop,
  [switch]$Status,
  [switch]$Uninstall,
  [switch]$Purge,
  [switch]$NoAutostart,
  [switch]$Help
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$EntryVersion = '@ENTRY_VERSION@'
$DevelopmentOnly = '@DEVELOPMENT_ONLY@'

function Fail {
  param([Parameter(Mandatory = $true)][string]$Message)
  [Console]::Error.WriteLine("Fased: $Message")
  exit 1
}

function Show-Usage {
  Write-Output "Usage: install.ps1 [-Local|-Server|-Connect] [-Transport ssh|tailscale|cloudflare|domain] [-Domain HOST] [-Open|-Start|-Stop|-Status|-Uninstall] [-Purge] [-NoAutostart] [-Listen 127.0.0.1:PORT]"
}

if ($Help) {
  Show-Usage
  exit 0
}

if (-not ($EntryVersion -match '^[0-9]+\.[0-9]+\.[0-9]+([-+][a-zA-Z0-9.-]+)?$') -or ($DevelopmentOnly -notin @('0', '1'))) {
  Fail 'No trusted installer release is published yet.'
}

$selectedProfiles = @(@($Local, $Server, $Connect) | Where-Object { $_ })
if ($selectedProfiles.Count -gt 1) {
  Fail 'Choose at most one of -Local, -Server, or -Connect.'
}
$Profile = if ($Local) { 'local' } elseif ($Server) { 'server' } elseif ($Connect) { 'connect' } else { '' }

$selectedActions = @(@($Open, $Start, $Stop, $Status, $Uninstall) | Where-Object { $_ })
if ($selectedActions.Count -gt 1) {
  Fail 'Choose only one management action.'
}
$Action = if ($Open) { 'open' } elseif ($Start) { 'start' } elseif ($Stop) { 'stop' } elseif ($Status) { 'status' } elseif ($Uninstall) { 'uninstall' } else { 'install' }

if ($Profile -eq 'connect' -and $Action -ne 'install') {
  Fail 'Connect does not manage an installation.'
}
if ($Purge -and $Action -ne 'uninstall') {
  Fail '-Purge requires -Uninstall.'
}
if ($Action -eq 'uninstall' -and ($Profile -notin @('local', 'server'))) {
  Fail '-Uninstall requires -Local or -Server.'
}
if ($Action -eq 'uninstall' -and $Profile -eq 'local' -and $Purge) {
  Fail '-Purge is only supported with -Server -Uninstall.'
}
if (-not [string]::IsNullOrWhiteSpace($Transport) -or -not [string]::IsNullOrWhiteSpace($Domain)) {
  if ($Profile -ne 'server' -or $Action -ne 'install') {
    Fail '-Transport and -Domain require -Server.'
  }
}
if (-not [string]::IsNullOrWhiteSpace($Transport)) {
  $Transport = $Transport.Trim().ToLowerInvariant()
  if ($Transport -notin @('ssh', 'tailscale', 'cloudflare', 'domain')) {
    Fail 'Server transport must be tailscale, cloudflare, domain, or ssh.'
  }
}
if (-not [string]::IsNullOrWhiteSpace($Domain)) {
  $Domain = $Domain.Trim()
  if ($Transport -eq 'ssh') {
    Fail 'SSH tunnel transport does not take -Domain.'
  }
  if ($Domain.Length -gt 253 -or -not ($Domain -match '\.') -or -not ($Domain -match '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$')) {
    Fail 'Invalid server HTTPS domain.'
  }
}
if (-not ($Listen -match '^127\.0\.0\.1:([0-9]{4,5})$')) {
  Fail 'Use a fixed loopback port (127.0.0.1:PORT).'
}
$ListenPort = [int]$Matches[1]
if ($ListenPort -lt 1024 -or $ListenPort -gt 65535) {
  Fail 'Invalid listener port.'
}

$TargetArch = ''
try {
  $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
  switch ($osArch) {
    'x64'   { $TargetArch = 'amd64' }
    'arm64' { $TargetArch = 'arm64' }
  }
} catch {
}
if ([string]::IsNullOrEmpty($TargetArch)) {
  $procArch = @($env:PROCESSOR_ARCHITEW6432, $env:PROCESSOR_ARCHITECTURE) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -First 1
  if ($null -ne $procArch) {
    switch ($procArch.ToUpperInvariant()) {
      'AMD64' { $TargetArch = 'amd64' }
      'ARM64' { $TargetArch = 'arm64' }
    }
  }
}
if ($TargetArch -notin @('amd64', 'arm64')) {
  Fail 'No verified package for this Windows CPU architecture.'
}

$ArchiveUrl = ''
$ArchiveSha256 = ''
@WINDOWS_TARGET_BINDINGS@
if (-not ($ArchiveSha256 -match '^[a-f0-9]{64}$')) {
  Fail 'No authenticated Windows package checksum is pinned.'
}
if ($DevelopmentOnly -eq '0') {
  $ExpectedUrl = "https://github.com/fased-ai/fased-app/releases/download/v$EntryVersion/fased-app-windows-$TargetArch.zip"
  if ($ArchiveUrl -ne $ExpectedUrl) {
    Fail 'Untrusted Windows installer source.'
  }
} elseif ($DevelopmentOnly -eq '1') {
  if (-not ($ArchiveUrl -match '^https://127\.0\.0\.1:[0-9]+/')) {
    Fail 'Invalid controlled Windows fixture.'
  }
} else {
  Fail 'Invalid installer binding.'
}

$tlsProtocols = [System.Net.SecurityProtocolType]::Tls12
if ([System.Enum]::GetNames([System.Net.SecurityProtocolType]) -contains 'Tls13') {
  $tlsProtocols = $tlsProtocols -bor [System.Net.SecurityProtocolType]::Tls13
}
[System.Net.ServicePointManager]::SecurityProtocol = $tlsProtocols

$LocalAppData = $env:LOCALAPPDATA
if ([string]::IsNullOrWhiteSpace($LocalAppData)) {
  $LocalAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
}
if ([string]::IsNullOrWhiteSpace($LocalAppData)) {
  Fail 'Cannot determine LOCALAPPDATA directory.'
}

$BaseRoot = Join-Path $LocalAppData 'Fased App'
$AppBin = Join-Path (Join-Path $BaseRoot 'app') 'fased-app.exe'
$ServerBin = Join-Path (Join-Path $BaseRoot 'server') 'fased-app.exe'
$UserBinDir = Join-Path $BaseRoot 'bin'

function Ensure-UserPath {
  param([Parameter(Mandatory = $true)][string]$Dir)
  $current = [Environment]::GetEnvironmentVariable('Path', 'User')
  $parts = @()
  if (-not [string]::IsNullOrWhiteSpace($current)) {
    $parts = @($current -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
  }
  $alreadyPresent = $false
  foreach ($part in $parts) {
    if ($part.TrimEnd('\').Equals($Dir.TrimEnd('\'), [System.StringComparison]::OrdinalIgnoreCase)) {
      $alreadyPresent = $true
      break
    }
  }
  if (-not $alreadyPresent) {
    $updated = if ([string]::IsNullOrWhiteSpace($current)) { $Dir } else { "$($current.TrimEnd(';'));$Dir" }
    [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
    if (-not (($env:Path -split ';') -contains $Dir)) {
      $env:Path = "$($env:Path.TrimEnd(';'));$Dir"
    }
  }
}

function Install-FasedCmdShim {
  param([Parameter(Mandatory = $true)][string]$TargetExe)
  if (-not (Test-Path -LiteralPath $TargetExe)) {
    return
  }
  if (-not (Test-Path -LiteralPath $UserBinDir)) {
    New-Item -ItemType Directory -Path $UserBinDir -Force | Out-Null
  }
  $shimPath = Join-Path $UserBinDir 'fased.cmd'
  if (-not (Test-Path -LiteralPath $shimPath)) {
    $shimBody = "@echo off`r`n`"$TargetExe`" %*`r`n"
    Set-Content -LiteralPath $shimPath -Value $shimBody -Encoding Ascii -NoNewline
  }
}

function Invoke-DetachedUninstall {
  param(
    [Parameter(Mandatory = $true)][string]$BinaryPath,
    [Parameter(Mandatory = $true)][string[]]$Arguments
  )
  # Copy the executable to a temporary staging directory before uninstalling so Windows does not lock the target .exe during removal.
  $tmpRoot = if (-not [string]::IsNullOrWhiteSpace($env:TEMP)) { $env:TEMP } else { [System.IO.Path]::GetTempPath() }
  $stageDir = Join-Path $tmpRoot ('fased-uninstall-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $stageDir -Force | Out-Null
  $exitCode = 1
  try {
    $tmpExe = Join-Path $stageDir ([System.IO.Path]::GetFileName($BinaryPath))
    Copy-Item -LiteralPath $BinaryPath -Destination $tmpExe -Force
    & $tmpExe @Arguments
    $exitCode = $LASTEXITCODE
  } finally {
    if (Test-Path -LiteralPath $stageDir) {
      Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
    }
  }
  exit $exitCode
}

if ($Action -ne 'install') {
  if ($Profile -eq 'server') {
    if (-not (Test-Path -LiteralPath $ServerBin)) {
      Fail 'No installed Fased server binary was found.'
    }
    if ($Action -eq 'uninstall') {
      $uArgs = @('server', 'uninstall')
      if ($Purge) { $uArgs += '--purge' }
      Invoke-DetachedUninstall -BinaryPath $ServerBin -Arguments $uArgs
    }
    if ($Action -eq 'status') {
      & $ServerBin 'server' 'status'
      exit $LASTEXITCODE
    }
    Fail "Use '$ServerBin server status|uninstall' or Task Scheduler for Windows Server."
  } else {
    if (-not (Test-Path -LiteralPath $AppBin)) {
      if ([string]::IsNullOrEmpty($Profile) -and $Action -eq 'status' -and (Test-Path -LiteralPath $ServerBin)) {
        & $ServerBin 'server' 'status'
        exit $LASTEXITCODE
      }
      Fail 'No installed Fased Local binary was found.'
    }
    switch ($Action) {
      'uninstall' { Invoke-DetachedUninstall -BinaryPath $AppBin -Arguments @('uninstall') }
      'open'      { & $AppBin 'open'; exit $LASTEXITCODE }
      'start'     { & $AppBin 'service' 'start'; exit $LASTEXITCODE }
      'stop'      { & $AppBin 'service' 'stop'; exit $LASTEXITCODE }
      'status'    { & $AppBin 'service' 'status'; exit $LASTEXITCODE }
    }
  }
}

if ([string]::IsNullOrEmpty($Profile)) {
  Write-Host 'Fased'
  Write-Host '  Local'
  Write-Host '  Server'
  Write-Host '  Connect'
  $choice = (Read-Host 'Choose [local/server/connect, default local]').Trim().ToLowerInvariant()
  switch ($choice) {
    ''        { $Profile = 'local' }
    'local'   { $Profile = 'local' }
    'server'  { $Profile = 'server' }
    'connect' { $Profile = 'connect' }
    default   { Fail 'Choose Local, Server, or Connect.' }
  }
}

if ($Profile -eq 'server') {
  if ([string]::IsNullOrEmpty($Transport)) {
    Write-Host 'Fased Server'
    Write-Host '  ssh        - SSH tunnel (loopback only)'
    Write-Host '  tailscale  - Tailscale private HTTPS'
    Write-Host '  cloudflare - Cloudflare Tunnel HTTPS'
    Write-Host '  domain     - Your own domain (Caddy auto-HTTPS)'
    $choice = (Read-Host 'Transport [ssh/tailscale/cloudflare/domain, default ssh]').Trim().ToLowerInvariant()
    switch ($choice) {
      ''           { $Transport = 'ssh' }
      'ssh'        { $Transport = 'ssh' }
      'tailscale'  { $Transport = 'tailscale' }
      'cloudflare' { $Transport = 'cloudflare' }
      'domain'     { $Transport = 'domain' }
      default      { Fail 'Choose ssh, tailscale, cloudflare, or domain.' }
    }
  }
  if (($Transport -in @('cloudflare', 'domain')) -and [string]::IsNullOrEmpty($Domain)) {
    $Domain = (Read-Host 'HTTPS domain (e.g. fased.example.com)').Trim()
    if ($Domain.Length -gt 253 -or -not ($Domain -match '\.') -or -not ($Domain -match '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$')) {
      Fail 'Invalid server HTTPS domain.'
    }
  }
}

$CacheRoot = Join-Path $BaseRoot 'cache'
New-Item -ItemType Directory -Path $CacheRoot -Force | Out-Null
$WorkDir = Join-Path $CacheRoot ('entry-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null

try {
  $ZipPath = Join-Path $WorkDir "fased-app-windows-$TargetArch.zip"
  $ExtractDir = Join-Path $WorkDir 'extract'
  Write-Host 'Downloading and verifying Fased for Windows...'
  Invoke-WebRequest -UseBasicParsing -Uri $ArchiveUrl -OutFile $ZipPath
  $ActualHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($ActualHash -ne $ArchiveSha256) {
    Fail 'Windows archive checksum differs; nothing was executed.'
  }
  Expand-Archive -LiteralPath $ZipPath -DestinationPath $ExtractDir -Force

  $BundleDir = Join-Path $ExtractDir "fased-app-windows-$TargetArch"
  if (-not (Test-Path -LiteralPath (Join-Path $BundleDir 'bundle.json'))) {
    if (Test-Path -LiteralPath (Join-Path $ExtractDir 'bundle.json')) {
      $BundleDir = $ExtractDir
    } else {
      Fail 'Verified Windows archive is missing bundle.json.'
    }
  }
  $Engine = Join-Path $BundleDir 'fased-app.exe'
  if (-not (Test-Path -LiteralPath $Engine)) {
    Fail 'Verified Windows archive is missing fased-app.exe.'
  }

  switch ($Profile) {
    'local' {
      $installArgs = @('install', '--bundle', $BundleDir, '--listen', $Listen)
      if (-not $NoAutostart) { $installArgs += '--autostart' }
      & $Engine @installArgs
      if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
      Install-FasedCmdShim -TargetExe $AppBin
      Ensure-UserPath -Dir $UserBinDir
    }
    'server' {
      $srvArgs = @('server', 'install', '--transport', $Transport, '--listen', $Listen)
      if (-not [string]::IsNullOrEmpty($Domain)) { $srvArgs += @('--domain', $Domain) }
      & $Engine @srvArgs
      if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
      Ensure-UserPath -Dir $UserBinDir
    }
    'connect' {
      & $Engine 'connect'
      if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }
  }
} finally {
  if (Test-Path -LiteralPath $WorkDir) {
    Remove-Item -LiteralPath $WorkDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}
