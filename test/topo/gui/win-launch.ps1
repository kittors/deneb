# Starts the Deneb window in the desktop session it runs in (shoot.sh runs
# it there through a scheduled task that runs only when the user is logged
# on, with a limited token, as a person double-clicking it would). ASCII
# only: Windows PowerShell 5.1 reads a file without a BOM as ANSI.
#
#   win-launch.ps1 -Exe <Deneb.exe> -Run <folder for the PID and log> [-Fake ready|fresh] [-Demo]
param(
	[Parameter(Mandatory = $true)][string]$Exe,
	[Parameter(Mandatory = $true)][string]$Run,
	[string]$Fake = '',
	[switch]$Demo
)
$ErrorActionPreference = 'Stop'
# The fake world (client/fakeworld) or the design's sample: the same on
# every system, so that the screenshots compare, and no engine is touched.
if ($Fake) { $env:DENEB_FAKE = $Fake }
if ($Demo) { $env:DENEB_DEMO = '1' }
try {
	$p = Start-Process -FilePath $Exe -WorkingDirectory (Split-Path $Exe) -PassThru `
		-RedirectStandardOutput (Join-Path $Run 'window.out.log') -RedirectStandardError (Join-Path $Run 'window.err.log')
	Set-Content -Path (Join-Path $Run 'window.pid') -Value $p.Id -Encoding ascii
} catch {
	Set-Content -Path (Join-Path $Run 'launch-error.txt') -Value $_.ToString() -Encoding utf8
}
