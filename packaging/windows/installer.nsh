; Included in the Windows installer by electron-builder
; (desktop/electron-builder.yml, nsis.include).
;
; Kumo installs for the current user only: no admin rights, and no
; "Anyone who uses this computer / Only for me" page.
!macro customInstallMode
  StrCpy $isForceCurrentInstall "1"
!macroend
