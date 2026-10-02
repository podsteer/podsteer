; NSIS installer for PodSteer. Built by CI (makensis) from build/bin/podsteer.exe
; AFTER that executable has been signed, and the installer is signed in turn.
;
; Per-user install by default (no UAC prompt, no admin rights), because
; PodSteer needs nothing outside the user's profile.
;
; Usage: makensis /DVERSION=1.2.3 /DARCH=amd64 /DOUTFILE=build\dist\x.exe build\windows\installer.nsi

Unicode true
!include "MUI2.nsh"

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef OUTFILE
  !define OUTFILE "..\dist\podsteer-windows-amd64-setup.exe"
!endif

Name "PodSteer"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\PodSteer"
InstallDirRegKey HKCU "Software\PodSteer" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "PodSteer"
VIAddVersionKey "CompanyName" "PodSteer"
VIAddVersionKey "FileDescription" "PodSteer installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "Copyright (c) 2026 PodSteer. Apache-2.0 licensed."

!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_LICENSE "..\..\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\podsteer.exe"
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "PodSteer"
  SetOutPath "$INSTDIR"
  File "..\bin\podsteer.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "Software\PodSteer" "InstallDir" "$INSTDIR"
  CreateShortcut "$SMPROGRAMS\PodSteer.lnk" "$INSTDIR\podsteer.exe"

  ; Add/Remove Programs entry, per user.
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer" "DisplayName" "PodSteer"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer" "Publisher" "PodSteer"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer" "DisplayIcon" "$INSTDIR\podsteer.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer" "NoRepair" 1
SectionEnd

; User data (history, settings) lives in the user's profile and is left alone.
Section "Uninstall"
  Delete "$INSTDIR\podsteer.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\PodSteer.lnk"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PodSteer"
  DeleteRegKey HKCU "Software\PodSteer"
SectionEnd
