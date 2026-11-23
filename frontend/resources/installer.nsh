; Keep allowToChangeInstallationDirectory in maker-nsis.ts. This include only
; ties app-data and the worker workspace root to the folder the user picked.
;
; Layout under $INSTDIR:
;   ao-home.json  — marker the packaged app reads before ready
;   data\         — AO_DATA_DIR (SQLite, worktrees, artifacts); hidden
;   electron\     — Chromium userData; hidden
;
; Uninstall preserves those three so worker workspaces survive a reinstall.

!macro customInstall
  CreateDirectory "$INSTDIR\data"
  CreateDirectory "$INSTDIR\data\worktrees"
  CreateDirectory "$INSTDIR\electron"
  FileOpen $0 "$INSTDIR\ao-home.json" w
  FileWrite $0 '{$\r$\n'
  FileWrite $0 '  "version": 1,$\r$\n'
  FileWrite $0 '  "dataDir": "data",$\r$\n'
  FileWrite $0 '  "electronDir": "electron",$\r$\n'
  FileWrite $0 '  "workspaceDir": "data/worktrees"$\r$\n'
  FileWrite $0 '}$\r$\n'
  FileClose $0
  SetFileAttributes "$INSTDIR\data" HIDDEN
  SetFileAttributes "$INSTDIR\electron" HIDDEN
  SetFileAttributes "$INSTDIR\ao-home.json" HIDDEN
!macroend

!macro customRemoveFiles
  ; Stash user state, remove the install tree, then put state back.
  ClearErrors
  Rename "$INSTDIR\data" "$PLUGINSDIR\ao-preserve-data"
  Rename "$INSTDIR\electron" "$PLUGINSDIR\ao-preserve-electron"
  Rename "$INSTDIR\ao-home.json" "$PLUGINSDIR\ao-preserve-home"
  RMDir /r "$INSTDIR"
  CreateDirectory "$INSTDIR"
  Rename "$PLUGINSDIR\ao-preserve-data" "$INSTDIR\data"
  Rename "$PLUGINSDIR\ao-preserve-electron" "$INSTDIR\electron"
  Rename "$PLUGINSDIR\ao-preserve-home" "$INSTDIR\ao-home.json"
  SetFileAttributes "$INSTDIR\data" HIDDEN
  SetFileAttributes "$INSTDIR\electron" HIDDEN
  SetFileAttributes "$INSTDIR\ao-home.json" HIDDEN
  ; Leave $INSTDIR in place when state remains so a later install can reuse it.
  RMDir "$INSTDIR"
!macroend
