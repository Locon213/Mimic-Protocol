<#
.SYNOPSIS
Mimic Protocol Server Management Script
#>

$InstallDir = "C:\Program Files\Mimic-Server"
$ConfigPath = "$InstallDir\server.yaml"
$BinaryPath = "$InstallDir\mimic-server.exe"
$TaskName   = "MimicProtocolServer"

$Action = $args[0]

function Show-Help {
    Write-Host "Usage: mimic.ps1 <command>"
    Write-Host "Commands:"
    Write-Host "  start-server    - Starts the Mimic server background service"
    Write-Host "  stop-server     - Stops the Mimic server service"
    Write-Host "  restart-server  - Restarts the Mimic server service"
    Write-Host "  status-server   - Checks if the server is running"
    Write-Host "  generate-uuid   - Generates a new random UUID"
    Write-Host "  generate-link   - Generates a mimic:// connection URI for clients"
    Write-Host "  config_path     - Prints the config file path or opens it if possible"
}

# Require Admin check for Scheduled Task management
function Require-Admin {
    $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if (-not $isAdmin) {
        Write-Host "Please run this command from an Administrator PowerShell!" -ForegroundColor Red
        exit
    }
}

switch ($Action) {
    "start-server" {
        Require-Admin
        Write-Host "Starting $TaskName..." -ForegroundColor Cyan
        Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        Write-Host "Done." -ForegroundColor Green
    }
    "stop-server" {
        Require-Admin
        Write-Host "Stopping $TaskName..." -ForegroundColor Cyan
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        $Process = Get-Process -Name "mimic-server" -ErrorAction SilentlyContinue
        if ($Process) {
            Stop-Process -Name "mimic-server" -Force
        }
        Write-Host "Done." -ForegroundColor Green
    }
    "restart-server" {
        Require-Admin
        Write-Host "Restarting $TaskName..." -ForegroundColor Cyan
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        $Process = Get-Process -Name "mimic-server" -ErrorAction SilentlyContinue
        if ($Process) {
            Stop-Process -Name "mimic-server" -Force
        }
        Start-Sleep -Seconds 1
        Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        Write-Host "Done." -ForegroundColor Green
    }
    "status-server" {
        $Task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        if ($Task) {
            Write-Host "Service Status: $($Task.State)"
        } else {
            Write-Host "Service $TaskName is not registered." -ForegroundColor Yellow
        }
        $Process = Get-Process -Name "mimic-server" -ErrorAction SilentlyContinue
        if ($Process) {
            Write-Host "Process mimic-server is RUNNING (PID: $($Process.Id))" -ForegroundColor Green
        } else {
            Write-Host "Process mimic-server is STOPPED" -ForegroundColor Red
        }
    }
    "generate-uuid" {
        $UUID = [guid]::NewGuid().ToString()
        Write-Host "New UUID generated: $UUID" -ForegroundColor Cyan
        Write-Host "Remember to update your config in: $ConfigPath" -ForegroundColor Yellow
    }
    "generate-link" {
        # Parse optional --host parameter
        $HostIP = ""
        for ($i = 1; $i -lt $args.Count; $i++) {
            if ($args[$i] -eq "--host" -and $i + 1 -lt $args.Count) {
                $HostIP = $args[$i + 1]
                break
            }
        }
        
        if (Test-Path $BinaryPath) {
            if ($HostIP -eq "") {
                # Try to auto-detect public IP
                Write-Host "Detecting public IP..." -ForegroundColor Cyan
                try {
                    $PublicIP = Invoke-RestMethod -Uri "https://api.ipify.org?format=json" -TimeoutSec 5 -ErrorAction SilentlyContinue
                    if ($PublicIP.ip) {
                        $HostIP = $PublicIP.ip
                        Write-Host "Detected public IP: $HostIP" -ForegroundColor Green
                    }
                } catch {
                    Write-Host "Could not auto-detect public IP." -ForegroundColor Yellow
                }
            }
            
            if ($HostIP -ne "") {
                & $BinaryPath generate-link $ConfigPath --host $HostIP
            } else {
                & $BinaryPath generate-link $ConfigPath
                Write-Host "`n⚠️  To specify IP manually, use:" -ForegroundColor Yellow
                Write-Host "   .\mimic.ps1 generate-link --host YOUR_IP" -ForegroundColor Cyan
            }
        } else {
            Write-Host "Error: mimic-server.exe not found at $BinaryPath." -ForegroundColor Red
        }
    }
    "config_path" {
        Write-Host "Opening $ConfigPath in Notepad..."
        Start-Process "notepad.exe" -ArgumentList "`"$ConfigPath`""
    }
    default {
        Show-Help
    }
}
