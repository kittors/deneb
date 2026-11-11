# Deneb: undo everything kernel mode may have changed on this Windows
# machine (M5 w3). Run as administrator:
#   powershell -ExecutionPolicy Bypass -File undo.ps1 [-Purge]
# Removes: the Deneb service and its engine in %ProgramFiles%\Deneb, Deneb's
# NRPT rule, Deneb's inbound firewall rules (Deneb-In for the adapter,
# Deneb-Process for the service's engine), an adapter named Deneb (with
# its routes). -Purge also removes the state (keys, network
# map, log) in %ProgramData%\Deneb; a service installed with -state
# elsewhere (the window's: %LOCALAPPDATA%\Deneb) keeps it there. Leaves
# alone: Tailscale, other NRPT rules, other adapters, the firewall's other
# rules (those Windows adds by itself for a program that listened, too),
# the networks and their categories (Deneb's network stays private, for
# the next time; with the adapter gone it is not in use).
param([switch]$Purge)
$ErrorActionPreference = 'Continue'
$rule = '{8F3B6C2E-1D4A-4E7B-9A5C-DE0EB2026001}'
$key = "HKLM:\SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig\$rule"

if (Get-Service -Name Deneb -ErrorAction SilentlyContinue) {
    Stop-Service -Name Deneb -Force -ErrorAction SilentlyContinue
    sc.exe delete Deneb | Out-Null
    Write-Output 'removed the Deneb service'
}
$engine = Join-Path $env:ProgramFiles 'Deneb'
if (Test-Path $engine) {
    # The service's process may take a moment to let go of its files.
    for ($i = 0; $i -lt 10 -and (Test-Path $engine); $i++) {
        Remove-Item -Path $engine -Recurse -Force -ErrorAction SilentlyContinue
        if (Test-Path $engine) { Start-Sleep -Milliseconds 500 }
    }
    if (Test-Path $engine) { Write-Output "could not remove $engine (in use?)" } else { Write-Output "removed $engine" }
}
if (Test-Path $key) {
    Remove-Item -Path $key -Recurse -Force
    Clear-DnsClientCache
    Write-Output "removed Deneb's NRPT rule $rule"
}
# Kernel mode removes Deneb-In when it stops; one is left only when the
# engine was killed. Deneb-Process stays as long as the service is
# installed.
foreach ($name in 'Deneb-In', 'Deneb-Process') {
    $fw = @(Get-NetFirewallRule -DisplayName $name -ErrorAction SilentlyContinue)
    if ($fw.Count -gt 0) {
        $fw | Remove-NetFirewallRule
        Write-Output "removed the firewall rule $name ($($fw.Count))"
    }
}
$adapter = Get-NetAdapter -Name Deneb -ErrorAction SilentlyContinue
if ($adapter) {
    # Left by an engine that was killed before it could close it.
    pnputil /remove-device $adapter.PnPDeviceID | Out-Null
    Write-Output 'removed the Deneb adapter'
}
if ($Purge -and (Test-Path "$env:ProgramData\Deneb")) {
    Remove-Item -Path "$env:ProgramData\Deneb" -Recurse -Force
    Write-Output "removed $env:ProgramData\Deneb"
}
$left = Get-DnsClientNrptRule | Where-Object { $_.Namespace -contains '.deneb.internal' }
if ($left) {
    Write-Output 'other NRPT rules still name .deneb.internal (not Deneb''s; left alone):'
    $left | Format-Table Name, Namespace, NameServers
}
Write-Output 'done. Get-NetAdapter, Get-DnsClientNrptRule, route print and Get-NetFirewallRule -DisplayName Deneb-* should match what they showed before.'
