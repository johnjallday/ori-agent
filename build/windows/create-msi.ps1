# Ori Agent MSI Builder Script
# Creates Windows MSI installer using WiX Toolset
# Usage: .\create-msi.ps1 -Version "0.0.12" -Arch "amd64" -DistDir "dist"

param(
    [Parameter(Mandatory=$true)]
    [string]$Version,

    [Parameter(Mandatory=$true)]
    [string]$Arch,

    [string]$DistDir = "dist"
)

$ErrorActionPreference = "Stop"

Write-Host "🚀 Creating Windows MSI for Ori Agent v$Version ($Arch)" -ForegroundColor Blue
Write-Host "==========================================================" -ForegroundColor Blue

# Configuration
$ProjectName = "OriAgent"
$BuildDir = "build-msi-$Arch"
$WxsFile = "build/windows/ori-agent.wxs"
$OutputMsi = "$DistDir/ori-agent-$Version-$Arch.msi"

# Check for WiX Toolset
Write-Host ""
Write-Host "🔍 Checking for WiX Toolset..." -ForegroundColor Yellow

$wixPath = $null
$possiblePaths = @(
    "${env:WIX}bin",
    "${env:ProgramFiles(x86)}\WiX Toolset v3.14\bin",
    "${env:ProgramFiles(x86)}\WiX Toolset v3.11\bin",
    "${env:ProgramFiles}\WiX Toolset v3.14\bin",
    "${env:ProgramFiles}\WiX Toolset v3.11\bin"
)

foreach ($path in $possiblePaths) {
    if (Test-Path "$path\candle.exe") {
        $wixPath = $path
        break
    }
}

if (-not $wixPath) {
    Write-Host "❌ Error: WiX Toolset not found!" -ForegroundColor Red
    Write-Host "  Please install WiX Toolset from: https://wixtoolset.org/releases/" -ForegroundColor Red
    Write-Host "  Or install via Chocolatey: choco install wixtoolset" -ForegroundColor Red
    exit 1
}

Write-Host "  ✓ Found WiX at: $wixPath" -ForegroundColor Green

# Clean up previous builds
Write-Host ""
Write-Host "🧹 Cleaning up..." -ForegroundColor Yellow
if (Test-Path $BuildDir) {
    Remove-Item -Path $BuildDir -Recurse -Force
}
New-Item -ItemType Directory -Path $BuildDir -Force | Out-Null

# Find the binary
Write-Host ""
Write-Host "📦 Locating binary..." -ForegroundColor Yellow

$binaryPath = Get-ChildItem -Path $DistDir -Recurse -Filter "ori-agent.exe" |
              Where-Object { $_.FullName -match "server_windows_$Arch" } |
              Select-Object -First 1 -ExpandProperty FullName

if (-not $binaryPath) {
    Write-Host "❌ Error: ori-agent.exe not found for architecture $Arch" -ForegroundColor Red
    Write-Host "  Searched in: $DistDir/server_windows_$Arch*/" -ForegroundColor Red
    exit 1
}

Write-Host "  ✓ Found binary: $binaryPath" -ForegroundColor Green

# Copy WXS template to build directory
Write-Host ""
Write-Host "📄 Preparing WiX template..." -ForegroundColor Yellow
$wxsBuildFile = "$BuildDir\ori-agent.wxs"
Copy-Item -Path $WxsFile -Destination $wxsBuildFile

# Replace template variables in WXS file
Write-Host "  Replacing template variables..." -ForegroundColor Yellow
$wxsContent = Get-Content -Path $wxsBuildFile -Raw
$wxsContent = $wxsContent -replace '\{\{\.Version\}\}', $Version
$wxsContent = $wxsContent -replace '\{\{\.Binary\}\}', $binaryPath
$wxsContent = $wxsContent -replace '\{\{\.MsiArch\}\}', $(if ($Arch -eq "amd64") { "x64" } else { $Arch })
Set-Content -Path $wxsBuildFile -Value $wxsContent

Write-Host "  ✓ Template prepared" -ForegroundColor Green

# Compile with Candle
Write-Host ""
Write-Host "🔨 Compiling WiX source..." -ForegroundColor Yellow

$Platform = if ($Arch -eq "amd64") { "x64" } else { $Arch }
$Win64 = if ($Arch -eq "amd64") { "yes" } else { "no" }

$candleArgs = @(
    "-arch", $Platform,
    "-dPlatform=$Platform",
    "-dWin64=$Win64",
    "-out", "$BuildDir\ori-agent.wixobj",
    $wxsBuildFile
)

& "$wixPath\candle.exe" $candleArgs

if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ Error: Candle compilation failed" -ForegroundColor Red
    exit 1
}

Write-Host "  ✓ Compilation successful" -ForegroundColor Green

# Link with Light
Write-Host ""
Write-Host "🔗 Linking MSI installer..." -ForegroundColor Yellow

# Create output directory if it doesn't exist
$outputDir = Split-Path -Path $OutputMsi -Parent
if (-not (Test-Path $outputDir)) {
    New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
}

$lightArgs = @(
    "-ext", "WixUIExtension",
    "-out", $OutputMsi,
    "$BuildDir\ori-agent.wixobj"
)

& "$wixPath\light.exe" $lightArgs

if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ Error: Light linking failed" -ForegroundColor Red
    exit 1
}

Write-Host "  ✓ Linking successful" -ForegroundColor Green

# Clean up build directory
Write-Host ""
Write-Host "🧹 Cleaning up build artifacts..." -ForegroundColor Yellow
Remove-Item -Path $BuildDir -Recurse -Force

# Done!
Write-Host ""
Write-Host "==========================================" -ForegroundColor Green
Write-Host "✅ MSI created successfully!" -ForegroundColor Green
Write-Host "==========================================" -ForegroundColor Green
Write-Host ""
Write-Host "📍 Location: $OutputMsi" -ForegroundColor Cyan
if (Test-Path $OutputMsi) {
    $fileInfo = Get-Item $OutputMsi
    $sizeInMB = [math]::Round($fileInfo.Length / 1MB, 1)
    Write-Host "📊 Size: $sizeInMB MB" -ForegroundColor Cyan

    # Calculate SHA-256
    Write-Host ""
    Write-Host "📊 SHA-256 Checksum:" -ForegroundColor Cyan
    $hash = Get-FileHash -Path $OutputMsi -Algorithm SHA256
    Write-Host "  $($hash.Hash)" -ForegroundColor White
}
Write-Host ""
Write-Host "🎉 Ready to distribute!" -ForegroundColor Green
Write-Host ""
