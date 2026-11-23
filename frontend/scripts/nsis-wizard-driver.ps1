# Drives the NSIS installer wizard that the app's non-silent quitAndInstall(false, true) shows, for a
# TEST-SCOPED installer only. Called by update-nsis-harness.mjs.
#
#   powershell -File nsis-wizard-driver.ps1 -InstallerPid <pid> -WorkDir <temp work dir> -Identity <id> -OutFile <transcript.json>
#
# Safety rules (the script exits without clicking anything when one fails):
#   * the installer process' executable must live under -WorkDir (the isolated updater cache);
#   * on the "who should this be installed for" page "Only for me" must already be selected and
#     "all users" must not be (all users needs elevation and writes machine-wide);
#   * a directory page is only accepted when its path is under -WorkDir;
#   * only Next / Install / Finish are ever clicked, never anything else.
param(
	[Parameter(Mandatory = $true)][int]$InstallerPid,
	[Parameter(Mandatory = $true)][string]$WorkDir,
	[Parameter(Mandatory = $true)][string]$Identity,
	[Parameter(Mandatory = $true)][string]$OutFile
)
Add-Type -AssemblyName UIAutomationClient
Add-Type @"
using System; using System.Runtime.InteropServices; using System.Text;
public static class NsisW {
  [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h, int m, IntPtr w, IntPtr l);
  [DllImport("user32.dll", CharSet = CharSet.Auto)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
}
"@
$transcript = New-Object System.Collections.ArrayList
function Note($msg, $data) { [void]$transcript.Add([pscustomobject]@{ t = (Get-Date).ToString('o'); note = $msg; data = $data }) }
function Save($status) { [pscustomobject]@{ status = $status; transcript = $transcript } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $OutFile -Encoding utf8 }

$proc = Get-CimInstance Win32_Process -Filter "ProcessId=$InstallerPid"
if (-not $proc -or -not $proc.ExecutablePath -or -not $proc.ExecutablePath.ToLower().StartsWith($WorkDir.ToLower())) {
	Note 'refused: installer is not under the isolated work dir' $proc.ExecutablePath; Save 'refused'; exit 3
}
if ($proc.CommandLine -notmatch '--updated') { Note 'refused: not an --updated installer run' $proc.CommandLine; Save 'refused'; exit 3 }
Note 'installer verified' ([pscustomobject]@{ path = $proc.ExecutablePath; cmd = $proc.CommandLine })

$root = [System.Windows.Automation.AutomationElement]::RootElement
function Get-Win {
	$c = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty, $InstallerPid)
	return $root.FindFirst([System.Windows.Automation.TreeScope]::Children, $c)
}
function Get-Texts($win) {
	$out = @{}
	foreach ($e in $win.FindAll([System.Windows.Automation.TreeScope]::Descendants, [System.Windows.Automation.Condition]::TrueCondition)) {
		$n = $e.Current.Name
		if ($n -ne '') { $out[[string]$e.Current.AutomationId + '|' + $e.Current.ClassName] = $n }
	}
	return $out
}
function Find-ById($win, $id) {
	return $win.FindFirst([System.Windows.Automation.TreeScope]::Descendants, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty, [string]$id)))
}
function Find-ByName($win, $name) {
	return $win.FindFirst([System.Windows.Automation.TreeScope]::Descendants, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, $name)))
}
function Checked($el) { return [NsisW]::SendMessage([IntPtr]$el.Current.NativeWindowHandle, 0x00F0, [IntPtr]::Zero, [IntPtr]::Zero).ToInt32() }
function Click($el) { [void][NsisW]::SendMessage([IntPtr]$el.Current.NativeWindowHandle, 0x00F5, [IntPtr]::Zero, [IntPtr]::Zero) }

$deadline = (Get-Date).AddMinutes(8)
$seen = @{}
while ((Get-Date) -lt $deadline) {
	if (-not (Get-Process -Id $InstallerPid -ErrorAction SilentlyContinue)) { Note 'installer process exited'; Save 'exited'; exit 0 }
	$win = Get-Win
	if (-not $win) { Start-Sleep -Milliseconds 500; continue }
	$texts = Get-Texts $win
	$joined = ($texts.Values -join ' || ')
	$page = if ($joined -match 'Who should this application be installed for') { 'install-for' }
		elseif ($joined -match 'Choose Install Location|Installation Folder|Choose the folder') { 'directory' }
		elseif ($joined -match 'Completing|has been installed on your computer') { 'finish' }
		elseif ($joined -match 'Installing|Please wait while') { 'installing' }
		else { 'unknown' }
	if (-not $seen.ContainsKey($page)) { $seen[$page] = 0 }
	if ($seen[$page] -eq 0) { Note "page: $page" $texts }
	$seen[$page]++
	switch ($page) {
		'install-for' {
			if ($seen[$page] -gt 1) { Start-Sleep -Milliseconds 700; break }
			$me = Find-ById $win 1202; $all = Find-ById $win 1201
			$meState = Checked $me; $allState = Checked $all
			Note 'radio state' ([pscustomobject]@{ onlyForMe = $meState; allUsers = $allState })
			if ($meState -ne 1 -or $allState -ne 0) { Note 'refused: all-users would be installed'; Save 'refused'; exit 4 }
			if ($joined -notmatch [regex]::Escape($WorkDir)) { Note 'refused: existing install shown is not under the work dir'; Save 'refused'; exit 4 }
			Click (Find-ById $win 1); Note 'clicked Next (only for me)'
		}
		'directory' {
			if ($seen[$page] -gt 1) { Start-Sleep -Milliseconds 700; break }
			if ($joined -notmatch [regex]::Escape($WorkDir)) { Note 'refused: directory page path is not under the work dir'; Save 'refused'; exit 4 }
			Click (Find-ById $win 1); Note 'clicked Next/Install on directory page'
		}
		'finish' {
			$run = Find-ByName $win ("Run AO Update Test $Identity")
			$fin = Find-ByName $win 'Finish'
			Note 'finish page' ([pscustomobject]@{ runChecked = $(if ($run) { Checked $run } else { $null }) })
			Click $fin; Note 'clicked Finish'
			Start-Sleep -Seconds 2
			Save 'finished'; exit 0
		}
		default { Start-Sleep -Milliseconds 700 }
	}
	Start-Sleep -Milliseconds 400
}
Note 'timeout'; Save 'timeout'; exit 5
