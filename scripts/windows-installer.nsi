Unicode True
RequestExecutionLevel admin
SetCompressor /SOLID lzma
!include "LogicLib.nsh"

!ifndef PRODUCT_VERSION
  !error "PRODUCT_VERSION is required"
!endif
!ifndef SOURCE_DIR
  !error "SOURCE_DIR is required"
!endif
!ifndef OUT_FILE
  !error "OUT_FILE is required"
!endif

Name "TunnelBoard ${PRODUCT_VERSION}"
OutFile "${OUT_FILE}"
InstallDir "$PROGRAMFILES64\TunnelBoard"
InstallDirRegKey HKLM "Software\TunnelBoard" "InstallDir"

Section "TunnelBoard" SEC_MAIN
  SetShellVarContext all
  SetRegView 64
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=tunnelboard-helper-migrate.exe "${SOURCE_DIR}\tunnelboard-helper.exe"
  nsExec::ExecToStack '"$PLUGINSDIR\tunnelboard-helper-migrate.exe" --cleanup-legacy-service'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_ICONSTOP "无法安全移除旧版 TunnelBoardHelper 系统服务，安装已中止。错误：$1"
    Abort
  ${EndIf}
  SetOutPath "$INSTDIR"
  File /r "${SOURCE_DIR}\*.*"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKLM "Software\TunnelBoard" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\TunnelBoard" "DisplayName" "TunnelBoard"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\TunnelBoard" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\TunnelBoard" "UninstallString" '"$INSTDIR\Uninstall.exe"'
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  SetRegView 64
  IfFileExists "$INSTDIR\tunnelboard-helper.exe" 0 current_user_ca_cleaned
  nsExec::ExecToStack '"$INSTDIR\tunnelboard-helper.exe" --cleanup-current-user-ca'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_ICONSTOP "无法移除当前用户的 TunnelBoard 本地根 CA 和私钥，卸载已中止。错误：$1"
    Abort
  ${EndIf}
  current_user_ca_cleaned:
  Delete "$INSTDIR\TunnelBoard.exe"
  Delete "$INSTDIR\tunnelboard-helper.exe"
  Delete "$INSTDIR\manifest.json"
  Delete "$INSTDIR\caddy\caddy.exe"
  Delete "$INSTDIR\LICENSES\TunnelBoard.txt"
  Delete "$INSTDIR\LICENSES\Caddy.txt"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR\caddy"
  RMDir "$INSTDIR\LICENSES"
  RMDir "$INSTDIR"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\TunnelBoard"
  DeleteRegKey HKLM "Software\TunnelBoard"
SectionEnd
