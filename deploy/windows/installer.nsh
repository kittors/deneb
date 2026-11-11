; Deneb: the engine's part of the Windows installer (M6 i2), included by
; MyGo's NSIS installer (mygo.json: windows.installerInclude). UTF-8 with
; a BOM, which is how NSIS knows.
;
; The engine is $INSTDIR\denebd.exe, beside Deneb.exe, with wintun.dll:
; MyGo's own updates rename each file and folder of the app aside before
; putting the new ones in, and Windows lets a running executable be
; renamed, not a folder holding one.
;
; The installer runs as the user, without administrator rights, so that
; the app updates itself; it installs no service. The window installs it,
; in the first run's "install the adapter" step, asking once for
; administrator rights (runas):
;
;   "$INSTDIR\denebd.exe" service install -state <its state> -wait-lock 30s
;
; Installing over an installed app, and uninstalling it, first stop the
; user's own engine: it runs on after the window closes and holds
; $INSTDIR\denebd.exe. `denebd shutdown` returns once its process is gone;
; a system service's engine refuses, and runs from %ProgramFiles%\Deneb
; anyway.
;
; The uninstaller then takes the service away. When there is one, or only
; its firewall rules are left (Deneb-Process, Deneb-In: the service was
; deleted some other way), it asks once for administrator rights and runs
; `denebd.exe service uninstall`: the engine stops (taking its adapter,
; routes, NRPT rule and Deneb-In down as it does), the service is deleted,
; whatever a crash left is undone, Deneb-Process and %ProgramFiles%\Deneb
; are removed. The device's state stays, as the app's other data does.
; Declined, the service stays as it is, the person is told how to remove
; it, and the uninstall goes on. Looking for a rule needs no rights.

!macro DenebStopUserEngine
  IfFileExists "$INSTDIR\denebd.exe" 0 deneb_no_engine
    nsExec::Exec '"$INSTDIR\denebd.exe" shutdown'
    Pop $0
  deneb_no_engine:
!macroend

!macro customInit
  Push $0
  !insertmacro DenebStopUserEngine
  Pop $0
!macroend

!macro customUnInstall
  Push $0
  !insertmacro DenebStopUserEngine
  ClearErrors
  ReadRegStr $0 HKLM "SYSTEM\CurrentControlSet\Services\Deneb" "ImagePath"
  StrCmp $0 "" 0 deneb_service_remove
  nsExec::Exec '"$SYSDIR\netsh.exe" advfirewall firewall show rule name=Deneb-Process dir=in'
  Pop $0
  StrCmp $0 "0" deneb_service_remove
  nsExec::Exec '"$SYSDIR\netsh.exe" advfirewall firewall show rule name=Deneb-In dir=in'
  Pop $0
  StrCmp $0 "0" 0 deneb_service_done
  deneb_service_remove:
  ClearErrors
  ExecShellWait "runas" "$INSTDIR\denebd.exe" "service uninstall" SW_HIDE
  IfErrors 0 deneb_service_done
  MessageBox MB_OK|MB_ICONINFORMATION "Deneb 的系统服务没有卸干净：没拿到管理员权限。$\r$\n要卸掉它，以管理员身份运行：$\r$\n$\"$PROGRAMFILES64\Deneb\denebd.exe$\" service uninstall" /SD IDOK
  deneb_service_done:
  Pop $0
!macroend
