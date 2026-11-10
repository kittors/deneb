# Puts the test machine back after shoot.sh: the window it started is
# closed, its tasks are gone, and the user-space engine runs as start.ps1
# starts it, if it was stopped. The folder of the run goes too. ASCII only
# (see win-launch.ps1).
#
#   win-cleanup.ps1 -Run <folder>
param([Parameter(Mandatory = $true)][string]$Run)
$ErrorActionPreference = 'Continue'
$pidFile = Join-Path $Run 'window.pid'
if (Test-Path $pidFile) {
	$id = [int](Get-Content $pidFile)
	Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
}
# Anything else still running from the run's folder.
Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($Run, [StringComparison]::OrdinalIgnoreCase) } |
	ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
Get-ScheduledTask -TaskName 'DenebGUI*' -ErrorAction SilentlyContinue | Unregister-ScheduledTask -Confirm:$false -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500
$dir = Join-Path $env:USERPROFILE 'deneb'
$engine = Get-Process denebd -ErrorAction SilentlyContinue
if ($engine) {
	Write-Output "engine running (pid $($engine.Id -join ','))"
} elseif (Test-Path (Join-Path $dir 'start.ps1')) {
	& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $dir 'start.ps1')
} else {
	Write-Output 'no engine running, and no start.ps1 to start it'
}
Remove-Item -Recurse -Force $Run -ErrorAction SilentlyContinue
