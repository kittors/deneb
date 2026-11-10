# Runs a script in the desktop session of the user logged on at the
# console: a scheduled task that runs only while that user is logged on,
# with a limited token, as a person starting a program would. It waits for
# the file -WaitFor to appear (the script writes it when done), then
# removes the task; what the script started keeps running. ASCII only
# (see win-launch.ps1). shoot.sh runs it over ssh.
#
#   win-task.ps1 -Name <task> -Script <ps1> -Arguments <string> -WaitFor <file> [-Timeout <s>]
param(
	[Parameter(Mandatory = $true)][string]$Name,
	[Parameter(Mandatory = $true)][string]$Script,
	[string]$Arguments = '',
	[Parameter(Mandatory = $true)][string]$WaitFor,
	[int]$Timeout = 40
)
$ErrorActionPreference = 'Stop'
$user = (Get-CimInstance Win32_ComputerSystem).UserName
if (-not $user) {
	Write-Output 'no one is logged on at the console: no desktop to show the window in'
	exit 2
}
$action = New-ScheduledTaskAction -Execute 'powershell.exe' `
	-Argument "-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$Script`" $Arguments"
$principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
# No time limit: at one, the scheduler would end what the task started.
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan)
Register-ScheduledTask -TaskName $Name -Action $action -Principal $principal -Settings $settings -Force | Out-Null
try {
	Start-ScheduledTask -TaskName $Name
	$deadline = (Get-Date).AddSeconds($Timeout)
	while (-not (Test-Path $WaitFor) -and (Get-Date) -lt $deadline) {
		Start-Sleep -Milliseconds 300
	}
	if (-not (Test-Path $WaitFor)) {
		Write-Output "the task $Name did not finish in $Timeout s"
		exit 1
	}
} finally {
	Unregister-ScheduledTask -TaskName $Name -Confirm:$false -ErrorAction SilentlyContinue
}
