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
    "config_path" {
        Write-Host "Opening $ConfigPath in Notepad..."
        Start-Process "notepad.exe" -ArgumentList "`"$ConfigPath`""
    }
    default {
        Show-Help
    }
}
