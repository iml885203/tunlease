$ErrorActionPreference = "Stop"

$BaseUrl = if ($env:TUNLEASE_BASE_URL) { $env:TUNLEASE_BASE_URL.TrimEnd('/') } else { "https://tunlease.example.com/install" }
$InstallDir = if ($env:TUNLEASE_INSTALL_DIR) { $env:TUNLEASE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "tunlease" }

if (-not [Environment]::Is64BitOperatingSystem) {
    throw "tunlease requires 64-bit Windows"
}

$Name = "tunle-windows-amd64.exe"
$Temp = Join-Path ([IO.Path]::GetTempPath()) ("tunle-" + [Guid]::NewGuid().ToString("N") + ".exe")
$ChecksumTemp = $Temp + ".sha256"

try {
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$Name" -OutFile $Temp
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$Name.sha256" -OutFile $ChecksumTemp

    $Expected = ((Get-Content -Raw $ChecksumTemp).Trim() -split '\s+')[0].ToLowerInvariant()
    $Actual = (Get-FileHash -Algorithm SHA256 $Temp).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) {
        throw "checksum mismatch"
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $Target = Join-Path $InstallDir "tunle.exe"
    if (Test-Path $Target) {
        Copy-Item -LiteralPath $Target -Destination "$Target.prev" -Force
    }
    Move-Item -LiteralPath $Temp -Destination $Target -Force

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $Entries = @($UserPath -split ';' | Where-Object { $_ })
    if ($Entries -notcontains $InstallDir) {
        [Environment]::SetEnvironmentVariable("Path", (@($Entries) + $InstallDir) -join ';', "User")
        Write-Host "Added $InstallDir to your user PATH."
    }
    if (@($env:Path -split ';' | Where-Object { $_ }) -notcontains $InstallDir) {
        $env:Path = "$InstallDir;$env:Path"
    }

    Write-Host "Installed: $Target"
    & $Target --version
}
finally {
    Remove-Item -LiteralPath $Temp -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $ChecksumTemp -Force -ErrorAction SilentlyContinue
}
