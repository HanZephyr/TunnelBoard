Unicode True
RequestExecutionLevel admin
SetCompressor /SOLID lzma

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
