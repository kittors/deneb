# Takes the screenshots in the desktop session it runs in (shoot.sh runs
# it there through a scheduled task, as it does win-launch.ps1): the whole
# screen, the window of the process given (its frame, without the shadow),
# and the taskbar with its notification area. Physical pixels: the script
# is DPI-aware per monitor. ASCII only (see win-launch.ps1).
#
#   win-shot.ps1 -Run <folder> [-WindowPid <window process>]
#
# With -WindowPid it also opens the hidden icons (Win+B, Enter), where
# Windows 11 keeps a new program's tray icon, takes them, and closes them
# (Esc): tray-hidden.png.
param(
	[Parameter(Mandatory = $true)][string]$Run,
	[int]$WindowPid = 0
)
$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force -Path $Run | Out-Null
$info = @()
try {
	Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class DenebShot {
	[StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
	[DllImport("user32.dll")] public static extern bool SetProcessDpiAwarenessContext(IntPtr v);
	[DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindow(string c, string n);
	[DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindowEx(IntPtr p, IntPtr a, string c, string n);
	[DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
	[DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
	[DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr h);
	[DllImport("dwmapi.dll")] public static extern int DwmGetWindowAttribute(IntPtr h, int a, out RECT r, int size);
	[DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
	[DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);
	public static void Key(byte vk, bool up) { keybd_event(vk, 0, up ? 2u : 0u, UIntPtr.Zero); }
}
'@
	# DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2: coordinates and captures
	# in physical pixels, whatever the scale.
	[DenebShot]::SetProcessDpiAwarenessContext([IntPtr](-4)) | Out-Null
	Add-Type -AssemblyName System.Drawing
	Add-Type -AssemblyName System.Windows.Forms

	function Save-Area([int]$x, [int]$y, [int]$w, [int]$h, [string]$name) {
		if ($w -le 0 -or $h -le 0) { return }
		$bmp = New-Object System.Drawing.Bitmap $w, $h
		$g = [System.Drawing.Graphics]::FromImage($bmp)
		$g.CopyFromScreen($x, $y, 0, 0, (New-Object System.Drawing.Size $w, $h))
		$bmp.Save((Join-Path $Run $name), [System.Drawing.Imaging.ImageFormat]::Png)
		$g.Dispose()
		$bmp.Dispose()
	}

	if ($WindowPid -ne 0) {
		$p = Get-Process -Id $WindowPid -ErrorAction SilentlyContinue
		if ($p -and $p.MainWindowHandle -ne [IntPtr]::Zero) {
			$h = $p.MainWindowHandle
			[DenebShot]::SetForegroundWindow($h) | Out-Null
			Start-Sleep -Milliseconds 600
			$r = New-Object DenebShot+RECT
			# DWMWA_EXTENDED_FRAME_BOUNDS: the frame, without the shadow.
			if ([DenebShot]::DwmGetWindowAttribute($h, 9, [ref]$r, 16) -ne 0) {
				[DenebShot]::GetWindowRect($h, [ref]$r) | Out-Null
			}
			Save-Area $r.L $r.T ($r.R - $r.L) ($r.B - $r.T) 'window.png'
			$info += "window $($r.L),$($r.T) $($r.R - $r.L)x$($r.B - $r.T) dpi $([DenebShot]::GetDpiForWindow($h))"
		} else {
			$info += "window: process $WindowPid has no window"
		}
	}

	$vs = [System.Windows.Forms.SystemInformation]::VirtualScreen
	Save-Area $vs.X $vs.Y $vs.Width $vs.Height 'screen.png'
	$info += "screen $($vs.Width)x$($vs.Height)"

	# The taskbar, and its notification area (the tray icons, the clock).
	$tb = [DenebShot]::FindWindow('Shell_TrayWnd', $null)
	if ($tb -ne [IntPtr]::Zero) {
		$r = New-Object DenebShot+RECT
		[DenebShot]::GetWindowRect($tb, [ref]$r) | Out-Null
		Save-Area $r.L $r.T ($r.R - $r.L) ($r.B - $r.T) 'taskbar.png'
		$info += "taskbar $($r.L),$($r.T) $($r.R - $r.L)x$($r.B - $r.T)"
		$tn = [DenebShot]::FindWindowEx($tb, [IntPtr]::Zero, 'TrayNotifyWnd', $null)
		if ($tn -ne [IntPtr]::Zero) {
			$n = New-Object DenebShot+RECT
			[DenebShot]::GetWindowRect($tn, [ref]$n) | Out-Null
			Save-Area $n.L $n.T ($n.R - $n.L) ($n.B - $n.T) 'tray.png'
			$info += "tray $($n.L),$($n.T) $($n.R - $n.L)x$($n.B - $n.T)"
		}
	}

	if ($WindowPid -ne 0) {
		# The hidden icons: Win+B puts the focus on their button, Enter opens them.
		[DenebShot]::Key(0x5B, $false); [DenebShot]::Key(0x42, $false)
		[DenebShot]::Key(0x42, $true); [DenebShot]::Key(0x5B, $true)
		Start-Sleep -Milliseconds 400
		[DenebShot]::Key(0x0D, $false); [DenebShot]::Key(0x0D, $true)
		Start-Sleep -Milliseconds 900
		$ov = [DenebShot]::FindWindow('TopLevelWindowForOverflowXamlIsland', $null)
		if ($ov -eq [IntPtr]::Zero) { $ov = [DenebShot]::FindWindow('NotifyIconOverflowWindow', $null) }
		if ($ov -ne [IntPtr]::Zero -and [DenebShot]::IsWindowVisible($ov)) {
			$o = New-Object DenebShot+RECT
			[DenebShot]::GetWindowRect($ov, [ref]$o) | Out-Null
			Save-Area $o.L $o.T ($o.R - $o.L) ($o.B - $o.T) 'tray-hidden.png'
			$info += "hidden icons $($o.L),$($o.T) $($o.R - $o.L)x$($o.B - $o.T)"
		} else {
			# No window of a known class: the corner above the notification
			# area, where they open, whatever shows there.
			$w = [int]($vs.Width * 0.32); $h = [int]($vs.Height * 0.45)
			Save-Area ($vs.X + $vs.Width - $w) ($vs.Y + $vs.Height - $h) $w $h 'tray-hidden.png'
			$info += 'hidden icons: no window of a known class; tray-hidden.png is the corner above the notification area'
		}
		[DenebShot]::Key(0x1B, $false); [DenebShot]::Key(0x1B, $true)
	}
} catch {
	$info += "error: $_"
}
Set-Content -Path (Join-Path $Run 'shot.txt') -Value $info -Encoding utf8
Set-Content -Path (Join-Path $Run 'shot.done') -Value 'done' -Encoding ascii
