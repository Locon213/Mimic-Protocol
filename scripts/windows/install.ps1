<#
.SYNOPSIS
Installs and configures Mimic Protocol Server on Windows.

.DESCRIPTION
This script downloads or copies the Mimic Protocol Server binary, creates a default configuration file, registers a scheduled task for auto-start, and adds the management CLI tool to the PATH.
#>

param(
    [string]$Version = "latest"
)

# Request Admin Privileges
$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Host "Please run this script as Administrator!" -ForegroundColor Red
    exit
}

$InstallDir = "C:\Program Files\Mimic-Server"
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host "   Mimic Protocol Server Installer (Windows) " -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan

# Create Install Directory
if (-not (Test-Path -Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir | Out-Null
}

$BinaryPath = "$InstallDir\mimic-server.exe"

# Copy local or download
if (Test-Path ".\server.exe") {
    Write-Host "=> Found local 'server.exe', copying..."
    Copy-Item ".\server.exe" -Destination $BinaryPath -Force
} else {
    Write-Host "=> Downloading Mimic Protocol Server ($Version)..."
    $DownloadUrl = "https://github.com/Locon213/Mimic-Protocol/releases/latest/download/mimic-server-windows-amd64.exe"
    if ($Version -ne "latest") {
        $DownloadUrl = "https://github.com/Locon213/Mimic-Protocol/releases/download/$Version/mimic-server-windows-amd64.exe"
    }

    try {
        Invoke-WebRequest -Uri $DownloadUrl -OutFile $BinaryPath -ErrorAction Stop
    } catch {
        Write-Host "Error: Failed to download binary from $DownloadUrl" -ForegroundColor Red
        Write-Host $_.Exception.Message -ForegroundColor Red
        exit
    }
}

# Generate Config
$ConfigPath = "$InstallDir\server.yaml"
$DomainsFile = "$InstallDir\domains.txt"

if (-not (Test-Path $DomainsFile)) {
    Write-Host "=> Generating domains.txt..."
    Set-Content -Path $DomainsFile -Value "vk.com`nrutube.ru`nyandex.ru" -Encoding UTF8
}

if (-not (Test-Path $ConfigPath)) {
    Write-Host "=> Generating default server.yaml..."
    
    # Generate UUID natively in PowerShell
    $UUID = [guid]::NewGuid().ToString()

    $YamlContent = @"
port: 443
uuid: "$UUID"
domains_file: "$($DomainsFile -replace '\\', '\\')"
max_clients: 100
rate_limit: 0
transport: "mtp"
"@
    Set-Content -Path $ConfigPath -Value $YamlContent -Encoding UTF8
    Write-Host "Generated server UUID: $UUID"
}

# Install CLI wrapper
$CliPath = "$InstallDir\mimic.ps1"
if (Test-Path ".\scripts\windows\mimic.ps1") {
    Copy-Item ".\scripts\windows\mimic.ps1" -Destination $CliPath -Force
} else {
    Write-Host "Warning: mimic.ps1 not found in .\scripts\windows, CLI commands will not be installed." -ForegroundColor Yellow
}

# Add to system PATH
$CurrentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($CurrentPath -notlike "*$InstallDir*") {
    Write-Host "=> Adding $InstallDir to system PATH..."
    $NewPath = "$CurrentPath;$InstallDir"
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "Machine")
    Write-Host "Info: You may need to restart your terminal for 'mimic.ps1' to take effect globally." -ForegroundColor Yellow
}

# Create a Scheduled Task to run as a Background Service
Write-Host "=> Registering Background Service (Scheduled Task)..."
$TaskName = "MimicProtocolServer"

# Unregister old task if exists
$ExistingTask = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($ExistingTask) {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$False
}

$Action = New-ScheduledTaskAction -Execute $BinaryPath -Argument "-config `"$ConfigPath`"" -WorkingDirectory $InstallDir
$Principal = New-ScheduledTaskPrincipal -UserId "NT AUTHORITY\SYSTEM" -LogonType ServiceAccount -RunLevel Highest
$Trigger = New-ScheduledTaskTrigger -AtStartup

# Settings (don't stop on idle, restart if fails)
$Settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -DontStopOnIdleEnd -ExecutionTimeLimit (New-TimeSpan -Days 0) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName $TaskName -Action $Action -Principal $Principal -Trigger $Trigger -Settings $Settings -Description "Mimic Protocol Server Background Task" | Out-Null

Write-Host "=> Starting Server..."
Start-ScheduledTask -TaskName $TaskName

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host " Installation Complete!" -ForegroundColor Green
Write-Host " Server configuration: $ConfigPath"
Write-Host " Sub-domains allowed: $DomainsFile"
Write-Host " "
Write-Host " You can use the CLI tool to manage the server:"
Write-Host "   powershell -File mimic.ps1 start-server"
Write-Host "   powershell -File mimic.ps1 stop-server"
Write-Host "   powershell -File mimic.ps1 status-server"
Write-Host "   powershell -File mimic.ps1 generate-uuid"
Write-Host "=============================================" -ForegroundColor Cyan
