# Throwaway probe for the aigolog design (section 7.1, the Windows Event Log reader).
#
# It runs on a GitHub-hosted Windows runner, which is a disposable machine. It creates two local
# test accounts, three test services and a few test events, then reads them back the way aigolog
# will: through the Windows Event Log API (EvtQuery, EvtNext, EvtRender; here by way of .NET),
# and, for comparison, through wevtutil.
#
# Everything it prints is public. It never prints the test account's password. It prints and
# exports only events written after it started, plus counts and names of event kinds.
#
# Written for Windows PowerShell 5.1 (the "powershell" shell of the runner).

$ErrorActionPreference = 'Continue'

$script:StartUtc = [DateTime]::UtcNow.AddSeconds(-5)
$script:Since = $script:StartUtc.ToString("yyyy-MM-dd'T'HH:mm:ss.000'Z'")
$script:SinceClause = "TimeCreated[@SystemTime >= '$($script:Since)']"
$script:TempRoot = $env:RUNNER_TEMP
if (-not $script:TempRoot) { $script:TempRoot = $env:TEMP }
$script:Cap = Join-Path $script:TempRoot 'capture'
New-Item -ItemType Directory -Force -Path $script:Cap | Out-Null
$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:Report = New-Object System.Text.StringBuilder
$script:FailedSections = New-Object System.Collections.ArrayList
$script:FailedProbes = New-Object System.Collections.ArrayList
$script:ProbeUser = 'aigoprobe'
$script:ProbePw = $null
$script:ProbeUserReady = $false
$script:NewestSystemRecord = $null
$script:ProbeNames = @('probe-ghost-a', 'probe-ghost-b', 'aigoprobe')
$script:FailedLogonsMade = 0
$script:AdminUser = 'aigoadmin'
$script:AdminPw = $null
$script:AdminReady = $false
$script:AdminLocal = $null
$script:LogonAuditGuid = '{0CCE9215-69AE-11D9-BED3-505054503030}'

Add-Type -AssemblyName System.Core

$script:NativeSource = @'
using System;
using System.Runtime.InteropServices;

public static class ProbeNative
{
    [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool LogonUserW(string user, string domain, string password, int logonType, int logonProvider, out IntPtr token);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool CloseHandle(IntPtr handle);

    [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr RegisterEventSourceW(string server, string source);

    [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool ReportEventW(IntPtr log, ushort type, ushort category, uint eventId, IntPtr sid, ushort count, uint dataSize, [MarshalAs(UnmanagedType.LPArray, ArraySubType = UnmanagedType.LPWStr)] string[] strings, IntPtr data);

    [DllImport("advapi32.dll", SetLastError = true)]
    static extern bool DeregisterEventSource(IntPtr log);

    [DllImport("advapi32.dll", SetLastError = true)]
    static extern bool GetTokenInformation(IntPtr token, int infoClass, out int info, int infoLength, out int returnLength);

    // 1 = the token is not split, 2 = full (elevated), 3 = limited (filtered). Negative = Windows error.
    public static int ElevationType(IntPtr token)
    {
        int value;
        int length;
        if (GetTokenInformation(token, 18, out value, 4, out length)) { return value; }
        return -Marshal.GetLastWin32Error();
    }

    // Returns 0 when the logon worked, otherwise the Windows error number.
    public static int Logon(string user, string domain, string password, int logonType, out IntPtr token)
    {
        if (LogonUserW(user, domain, password, logonType, 0, out token)) { return 0; }
        return Marshal.GetLastWin32Error();
    }

    // Writes one event from a source name. Returns 0 when written, otherwise the Windows error number.
    public static int Report(string source, ushort type, uint eventId, string[] strings)
    {
        IntPtr h = RegisterEventSourceW(null, source);
        if (h == IntPtr.Zero) { return Marshal.GetLastWin32Error(); }
        bool ok = ReportEventW(h, type, 0, eventId, IntPtr.Zero, (ushort)strings.Length, 0, strings, IntPtr.Zero);
        int err = ok ? 0 : Marshal.GetLastWin32Error();
        DeregisterEventSource(h);
        return err;
    }
}
'@

$script:ServiceSource = @'
using System;
using System.ServiceProcess;
using System.Threading;

// A service that reports "running" and then dies 1.5 seconds later without telling Windows.
public class AigoProbeService : ServiceBase
{
    protected override void OnStart(string[] args)
    {
        Thread t = new Thread(delegate() { Thread.Sleep(1500); Environment.Exit(3); });
        t.IsBackground = true;
        t.Start();
    }

    public static void Main()
    {
        ServiceBase.Run(new AigoProbeService());
    }
}
'@

# ---------------------------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------------------------

function Say([string]$Text) {
    if ($null -eq $Text) { $Text = '' }
    Write-Host $Text
    [void]$script:Report.AppendLine($Text)
}

function Save-Text([string]$Name, [string]$Text) {
    [System.IO.File]::WriteAllText((Join-Path $script:Cap $Name), $Text, $script:Utf8NoBom)
}

function Save-Report {
    [System.IO.File]::WriteAllText((Join-Path $script:Cap 'report.txt'), $script:Report.ToString(), $script:Utf8NoBom)
}

function Format-Failure($ErrorRecord) {
    $lines = New-Object System.Collections.ArrayList
    $ex = $ErrorRecord.Exception
    $depth = 0
    while ($null -ne $ex -and $depth -lt 6) {
        [void]$lines.Add(('  [{0}] {1}: {2} (HResult 0x{3:X8})' -f $depth, $ex.GetType().FullName, $ex.Message, $ex.HResult))
        $ex = $ex.InnerException
        $depth++
    }
    return ($lines -join "`n")
}

function Section([string]$Name, [scriptblock]$Body) {
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    Say ''
    Say "==================== SECTION: $Name ===================="
    try { & $Body }
    catch {
        Say "SECTION FAILED: $Name"
        Say (Format-Failure $_)
        Say $_.ScriptStackTrace
        [void]$script:FailedSections.Add($Name)
    }
    Say ("==================== END: $Name ({0:N1} s) ====================" -f $sw.Elapsed.TotalSeconds)
    Save-Report
}

function Probe([string]$Label, [scriptblock]$Body) {
    Say ''
    Say "--- $Label ---"
    try { & $Body }
    catch {
        Say "PROBE FAILED: $Label"
        Say (Format-Failure $_)
        Say $_.ScriptStackTrace
        [void]$script:FailedProbes.Add($Label)
    }
}

function Decode-Bytes([byte[]]$Bytes) {
    if ($null -eq $Bytes -or $Bytes.Length -eq 0) { return @{ Text = ''; Encoding = 'empty' } }
    if ($Bytes.Length -ge 2 -and $Bytes[0] -eq 0xFF -and $Bytes[1] -eq 0xFE) {
        return @{ Text = [System.Text.Encoding]::Unicode.GetString($Bytes, 2, $Bytes.Length - 2); Encoding = 'UTF-16LE with a byte-order mark' }
    }
    if ($Bytes.Length -ge 3 -and $Bytes[0] -eq 0xEF -and $Bytes[1] -eq 0xBB -and $Bytes[2] -eq 0xBF) {
        return @{ Text = $script:Utf8NoBom.GetString($Bytes, 3, $Bytes.Length - 3); Encoding = 'UTF-8 with a byte-order mark' }
    }
    if ($Bytes.Length -ge 4 -and $Bytes[1] -eq 0 -and $Bytes[3] -eq 0) {
        return @{ Text = [System.Text.Encoding]::Unicode.GetString($Bytes); Encoding = 'UTF-16LE without a byte-order mark' }
    }
    $high = $false
    foreach ($b in $Bytes) { if ($b -ge 0x80) { $high = $true; break } }
    if (-not $high) { return @{ Text = [System.Text.Encoding]::ASCII.GetString($Bytes); Encoding = 'plain ASCII (no byte above 0x7F)' } }
    try {
        $strict = New-Object System.Text.UTF8Encoding($false, $true)
        return @{ Text = $strict.GetString($Bytes); Encoding = 'valid UTF-8 with non-ASCII bytes' }
    }
    catch {
        $cp = [System.Globalization.CultureInfo]::CurrentCulture.TextInfo.OEMCodePage
        return @{ Text = [System.Text.Encoding]::GetEncoding($cp).GetString($Bytes); Encoding = "not valid UTF-8; shown with the OEM code page $cp" }
    }
}

# Runs a program with no shell in between, so the command line is exactly $Arguments and the
# output bytes are exactly what the program wrote.
function Invoke-Tool {
    param(
        [string]$File,
        [string]$Arguments = '',
        [string]$Display = '',
        [string]$SaveAs = '',
        [string]$AsUser = '',
        [string]$Password = '',
        [bool]$ShowOutput = $true,
        [bool]$Quiet = $false,
        [bool]$LoadProfile = $false,
        [int]$MaxShow = 8000
    )
    $shown = $Display
    if (-not $shown) { $shown = "$File $Arguments" }
    if ($AsUser) { $shown = "$shown   [run as the account $AsUser]" }
    $result = New-Object psobject -Property @{ Started = $false; ExitCode = $null; Text = ''; StdErr = ''; Encoding = ''; Length = 0 }

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $File
    $psi.Arguments = $Arguments
    $psi.UseShellExecute = $false
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $psi.WorkingDirectory = (Join-Path $env:SystemRoot 'System32')
    if ($AsUser) {
        $psi.UserName = $AsUser
        $psi.Domain = $env:COMPUTERNAME
        $psi.Password = (ConvertTo-SecureString $Password -AsPlainText -Force)
        $psi.LoadUserProfile = $LoadProfile
    }
    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    try { [void]$p.Start() }
    catch {
        if (-not $Quiet) {
            Say "COMMAND: $shown"
            Say 'COULD NOT START:'
            Say (Format-Failure $_)
        }
        return $result
    }
    $result.Started = $true
    try { $p.StandardInput.Close() } catch { }
    $errTask = $p.StandardError.ReadToEndAsync()
    $ms = New-Object System.IO.MemoryStream
    $p.StandardOutput.BaseStream.CopyTo($ms)
    $p.WaitForExit()
    $bytes = $ms.ToArray()
    $dec = Decode-Bytes $bytes
    $result.ExitCode = $p.ExitCode
    $result.Text = $dec.Text
    $result.Encoding = $dec.Encoding
    $result.Length = $bytes.Length
    $result.StdErr = $errTask.Result
    $p.Dispose()
    if ($SaveAs) { [System.IO.File]::WriteAllBytes((Join-Path $script:Cap $SaveAs), $bytes) }
    if (-not $Quiet) {
        Say "COMMAND: $shown"
        Say ("EXIT CODE: {0} (0x{0:X8})" -f $result.ExitCode)
        Say ("STANDARD OUTPUT: {0} bytes, {1}" -f $result.Length, $result.Encoding)
        if ($ShowOutput) {
            $t = $result.Text.TrimEnd()
            if ($t.Length -gt $MaxShow) {
                $t = $t.Substring(0, $MaxShow) + "`n(cut here; $($result.Text.Length) characters in all)"
            }
            if ($t) { Say $t }
        }
        else {
            $head = $result.Text
            if ($head.Length -gt 80) { $head = $head.Substring(0, 80) }
            Say "(content not shown; its first 80 characters: $head)"
        }
        if ($result.StdErr -and $result.StdErr.Trim()) { Say ("STANDARD ERROR: " + $result.StdErr.Trim()) }
    }
    return $result
}

# Reads events through the Event Log API. $Path is a channel name, or a file when $IsFile is set.
function Read-Log {
    param(
        [string]$Path,
        [string]$XPath = '*',
        [int]$Max = 10,
        [bool]$NewestFirst = $true,
        [bool]$IsFile = $false,
        [bool]$WithMessage = $false
    )
    $ptype = [System.Diagnostics.Eventing.Reader.PathType]::LogName
    if ($IsFile) { $ptype = [System.Diagnostics.Eventing.Reader.PathType]::FilePath }
    $q = New-Object System.Diagnostics.Eventing.Reader.EventLogQuery($Path, $ptype, $XPath)
    $q.ReverseDirection = $NewestFirst
    $reader = New-Object System.Diagnostics.Eventing.Reader.EventLogReader($q)
    $list = New-Object System.Collections.ArrayList
    try {
        while ($list.Count -lt $Max) {
            $ev = $reader.ReadEvent()
            if ($null -eq $ev) { break }
            $item = New-Object psobject -Property @{
                Xml = $ev.ToXml(); Provider = $ev.ProviderName; Id = $ev.Id; RecordId = $ev.RecordId
                Level = $ev.Level; Time = $ev.TimeCreated; MessageAsked = $WithMessage; Message = $null; MessageError = $null
            }
            if ($WithMessage) {
                try { $item.Message = $ev.FormatDescription() }
                catch { $item.MessageError = $_.Exception.GetType().Name + ': ' + $_.Exception.Message }
            }
            [void]$list.Add($item)
            $ev.Dispose()
        }
    }
    finally { $reader.Dispose() }
    return , $list
}

# Counts events without keeping them. With $Render it also asks for each event's XML.
function Read-Count {
    param([string]$Path, [string]$XPath = '*', [int]$Max = 1000, [bool]$Render = $false, [bool]$Message = $false)
    $q = New-Object System.Diagnostics.Eventing.Reader.EventLogQuery($Path, [System.Diagnostics.Eventing.Reader.PathType]::LogName, $XPath)
    $q.ReverseDirection = $true
    $reader = New-Object System.Diagnostics.Eventing.Reader.EventLogReader($q)
    $n = 0
    $chars = 0
    try {
        while ($n -lt $Max) {
            $ev = $reader.ReadEvent()
            if ($null -eq $ev) { break }
            $n++
            if ($Render) { $chars += $ev.ToXml().Length }
            if ($Message) {
                try {
                    $text = $ev.FormatDescription()
                    if ($text) { $chars += $text.Length }
                }
                catch { }
            }
            $ev.Dispose()
        }
    }
    finally { $reader.Dispose() }
    return (New-Object psobject -Property @{ Count = $n; Characters = $chars })
}

function Get-Shape([string]$Xml) {
    $doc = New-Object System.Xml.XmlDocument
    try { $doc.LoadXml($Xml) }
    catch { return (New-Object psobject -Property @{ Shape = 'XML-a-strict-parser-refuses'; HasRecordId = ($Xml -match '<EventRecordID>') }) }
    $root = $doc.DocumentElement
    $parts = New-Object System.Collections.ArrayList
    $ed = $root.SelectSingleNode("*[local-name()='EventData']")
    if ($ed) {
        $data = $ed.SelectNodes("*[local-name()='Data']")
        $named = 0
        foreach ($d in $data) { if ($d.HasAttribute('Name')) { $named++ } }
        $unnamed = $data.Count - $named
        $bin = $ed.SelectNodes("*[local-name()='Binary']").Count
        if ($data.Count -eq 0) { [void]$parts.Add('EventData:no-Data') }
        elseif ($unnamed -eq 0) { [void]$parts.Add("EventData:$named-named") }
        elseif ($named -eq 0) { [void]$parts.Add("EventData:$unnamed-unnamed") }
        else { [void]$parts.Add("EventData:$named-named+$unnamed-unnamed") }
        if ($bin -gt 0) { [void]$parts.Add('Binary') }
    }
    $ud = $root.SelectSingleNode("*[local-name()='UserData']")
    if ($ud) { [void]$parts.Add('UserData') }
    if ($root.SelectSingleNode("*[local-name()='ProcessingErrorData']")) { [void]$parts.Add('ProcessingErrorData') }
    if ($root.SelectSingleNode("*[local-name()='DebugData']")) { [void]$parts.Add('DebugData') }
    if ($parts.Count -eq 0) { [void]$parts.Add('no-payload') }
    $rid = $root.SelectSingleNode("*[local-name()='System']/*[local-name()='EventRecordID']")
    return (New-Object psobject -Property @{ Shape = ($parts -join '+'); HasRecordId = ($null -ne $rid) })
}

# The children of EventData, in order, as text lines.
function Get-DataLines([string]$Xml) {
    $doc = New-Object System.Xml.XmlDocument
    $doc.LoadXml($Xml)
    $nodes = $doc.DocumentElement.SelectNodes("*[local-name()='EventData']/*")
    $lines = New-Object System.Collections.ArrayList
    $i = 0
    foreach ($n in $nodes) {
        $i++
        $name = '(no name)'
        if ($n.HasAttribute('Name')) { $name = $n.GetAttribute('Name') }
        [void]$lines.Add(('  {0,2}. <{1}> {2} = {3}' -f $i, $n.LocalName, $name, $n.InnerText))
    }
    if ($lines.Count -eq 0) { return '  (EventData has no children, or there is no EventData)' }
    return ($lines -join "`n")
}

# The named Data values of an event, as a table from name to value.
function Get-DataMap([string]$Xml) {
    $doc = New-Object System.Xml.XmlDocument
    $doc.LoadXml($Xml)
    $map = @{}
    foreach ($n in $doc.DocumentElement.SelectNodes("*[local-name()='EventData']/*[local-name()='Data']")) {
        if ($n.HasAttribute('Name')) { $map[$n.GetAttribute('Name')] = $n.InnerText }
    }
    return $map
}

function Show-Event($Item, [string]$SaveAs) {
    $shape = Get-Shape $Item.Xml
    Say ("EVENT provider='{0}' number={1} record={2} level={3} shape={4} record-number-in-xml={5}" -f $Item.Provider, $Item.Id, $Item.RecordId, $Item.Level, $shape.Shape, $shape.HasRecordId)
    Say $Item.Xml
    if ($Item.MessageAsked) {
        if ($Item.MessageError) { Say "MESSAGE: (asking for it failed) $($Item.MessageError)" }
        elseif ([string]::IsNullOrEmpty($Item.Message)) { Say 'MESSAGE: (none: the API returned nothing)' }
        else { Say "MESSAGE: $($Item.Message)" }
    }
    if ($SaveAs) { Save-Text $SaveAs $Item.Xml }
}

function New-ProbeUser {
    # 14 characters: longer ones make "net user" stop and ask a question.
    $script:ProbePw = 'Pb1!' + [guid]::NewGuid().ToString('N').Substring(0, 10)
    $created = $false
    try {
        $secure = ConvertTo-SecureString $script:ProbePw -AsPlainText -Force
        New-LocalUser -Name $script:ProbeUser -Password $secure -AccountNeverExpires -PasswordNeverExpires -Description 'aigolog probe, standard user' -ErrorAction Stop | Out-Null
        $created = $true
        Say "Created the local account $($script:ProbeUser) with New-LocalUser (password not shown)."
    }
    catch {
        Say 'New-LocalUser did not work; trying net user instead.'
        Say (Format-Failure $_)
        $r = Invoke-Tool -File 'net.exe' -Arguments ('user {0} {1} /add' -f $script:ProbeUser, $script:ProbePw) -Display "net.exe user $($script:ProbeUser) <password not shown> /add"
        if ($r.ExitCode -eq 0) { $created = $true }
    }
    if (-not $created) { throw 'could not create the test account' }
    $script:ProbeUserReady = $true

    # Group membership is its own step: a failure here must not undo the account.
    try {
        Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $script:ProbeUser -ErrorAction Stop
        Say 'Added it to the Users group (so it was not a member straight after creation).'
    }
    catch {
        Say 'Adding it to the Users group raised this (a "member exists" error means it already was one):'
        Say (Format-Failure $_)
    }
    Say 'The account as Windows now describes it (look at "Local Group Memberships"):'
    [void](Invoke-Tool -File 'net.exe' -Arguments "user $($script:ProbeUser)")
}

# The probe's own failed logons since it started. Other failed logons on the machine are counted
# but never shown or saved.
function Get-ProbeFailedLogons([int]$Max) {
    $q = "*[System[(EventID=4625) and $($script:SinceClause)]]"
    $all = Read-Log -Path 'Security' -XPath $q -Max $Max -WithMessage $true
    $own = New-Object System.Collections.ArrayList
    foreach ($e in $all) {
        $who = (Get-DataMap $e.Xml)['TargetUserName']
        if ($script:ProbeNames -contains $who) { [void]$own.Add($e) }
    }
    return (New-Object psobject -Property @{ Query = $q; AllCount = $all.Count; Own = $own })
}

# One event, as the API gives its message and as wevtutil shows it in three formats.
function Show-MessageForms([string]$Log, $Item, [string]$Name) {
    Say ("Event {0}, record {1}, through the API:" -f $Item.Id, $Item.RecordId)
    if ($Item.MessageError) { Say "  asking for the message failed: $($Item.MessageError)" }
    elseif ([string]::IsNullOrEmpty($Item.Message)) { Say '  the API returned no message' }
    else {
        $first = ($Item.Message -split "`r?`n") | Where-Object { $_.Trim() } | Select-Object -First 1
        if ($null -eq $first) { Say '  the message holds only blank space' }
        else {
            $first = $first.Trim()
            Say "  first line of the message: $first"
            Say ("  that line appears in the plain event XML: {0}" -f $Item.Xml.Contains($first))
            Say ("  the message is {0} characters; the plain XML is {1} characters" -f $Item.Message.Length, $Item.Xml.Length)
        }
    }
    $one = "*[System[(EventRecordID=$($Item.RecordId))]]"
    foreach ($f in 'text', 'xml', 'RenderedXml') {
        [void](Invoke-Tool -File 'wevtutil.exe' -Arguments ('qe {0} "/q:{1}" /c:1 /f:{2}' -f $Log, $one, $f) -SaveAs "wevtutil-$Name-$f.txt")
    }
}

# Logs $User on with one kind of logon, describes the token Windows hands back, then takes on
# that identity for a moment and tries to read one event from each of three logs through the API.
# Returns $true when the logon itself worked.
function Test-TokenRead([string]$Label, [string]$User, [string]$Pw, [int]$LogonType) {
    $tok = [IntPtr]::Zero
    $rc = [ProbeNative]::Logon($User, '.', $Pw, $LogonType, [ref]$tok)
    Say ("LogonUser as {0}, logon type {1} (2 interactive, 4 batch, 3 network) -> Windows error {2}" -f $User, $LogonType, $rc)
    if ($rc -ne 0) { return $false }
    $id = New-Object System.Security.Principal.WindowsIdentity($tok)
    try {
        $sids = @($id.Groups | ForEach-Object { $_.Value })
        Say ("Token of {0}: elevation type {1} (1 not split, 2 full, 3 limited)" -f $id.Name, [ProbeNative]::ElevationType($tok))
        Say ("  enabled group SIDs: " + ($sids -join ' '))
        Say ("  Administrators (S-1-5-32-544) enabled: {0}; Users (S-1-5-32-545): {1}; Event Log Readers (S-1-5-32-573): {2}; INTERACTIVE (S-1-5-4): {3}" -f ($sids -contains 'S-1-5-32-544'), ($sids -contains 'S-1-5-32-545'), ($sids -contains 'S-1-5-32-573'), ($sids -contains 'S-1-5-4'))
        $results = New-Object System.Collections.ArrayList
        $ctx = $id.Impersonate()
        try {
            [void]$results.Add('identity of the thread while reading: ' + [System.Security.Principal.WindowsIdentity]::GetCurrent().Name)
            foreach ($log in 'Application', 'System', 'Security') {
                try {
                    $r = Read-Log -Path $log -XPath '*' -Max 1
                    if ($r.Count -gt 0) { [void]$results.Add("$log : ALLOWED (read record $($r[0].RecordId))") }
                    else { [void]$results.Add("$log : ALLOWED, but the log gave no event") }
                }
                catch {
                    [void]$results.Add("$log : REFUSED`n" + (Format-Failure $_))
                }
            }
        }
        finally { $ctx.Undo() }
        Say "RESULT ($Label; logon type $LogonType), read through the API:"
        foreach ($line in $results) { Say "  $line" }
    }
    finally {
        $id.Dispose()
        [void][ProbeNative]::CloseHandle($tok)
    }
    return $true
}

# Tries the logon types in order. With $All it tests every one that works; without, the first.
function Test-ImpersonatedRead([string]$Label, [string]$User, [string]$Pw, [int[]]$Types, [bool]$All) {
    $worked = 0
    foreach ($type in $Types) {
        if (Test-TokenRead $Label $User $Pw $type) {
            $worked++
            if (-not $All) { break }
        }
    }
    if ($worked -eq 0) { throw "no logon type worked for $User" }
}

function Test-ChildRead([string]$Label, [string]$User, [string]$Pw) {
    Say "As a separate process started with the name and password of $User ($Label):"
    [void](Invoke-Tool -File 'whoami.exe' -Arguments '/groups' -AsUser $User -Password $Pw)
    foreach ($log in 'Application', 'System', 'Security') {
        [void](Invoke-Tool -File 'wevtutil.exe' -Arguments "qe $log /c:1 /f:xml" -AsUser $User -Password $Pw -ShowOutput $false)
    }
}

function New-ProbeAdmin {
    $script:AdminPw = 'Pa1!' + [guid]::NewGuid().ToString('N').Substring(0, 10)
    $secure = ConvertTo-SecureString $script:AdminPw -AsPlainText -Force
    New-LocalUser -Name $script:AdminUser -Password $secure -AccountNeverExpires -PasswordNeverExpires -Description 'aigolog probe, administrator' -ErrorAction Stop | Out-Null
    $script:AdminReady = $true
    Say "Created the local account $($script:AdminUser) (password not shown)."
    foreach ($sid in 'S-1-5-32-545', 'S-1-5-32-544') {
        try {
            Add-LocalGroupMember -SID $sid -Member $script:AdminUser -ErrorAction Stop
            Say "Added it to the group $sid."
        }
        catch {
            Say "Adding it to the group $sid raised:"
            Say (Format-Failure $_)
        }
    }
    [void](Invoke-Tool -File 'net.exe' -Arguments "user $($script:AdminUser)")
}

# Text with every character outside printable ASCII written as {U+XXXX}.
function Show-Codes([string]$Text) {
    $sb = New-Object System.Text.StringBuilder
    foreach ($ch in $Text.ToCharArray()) {
        $n = [int]$ch
        if ($n -ge 32 -and $n -le 126) { [void]$sb.Append($ch) }
        else { [void]$sb.Append(('{{U+{0:X4}}}' -f $n)) }
    }
    return $sb.ToString()
}

function Survey-Log([string]$Log, [int]$Max) {
    $q = New-Object System.Diagnostics.Eventing.Reader.EventLogQuery($Log, [System.Diagnostics.Eventing.Reader.PathType]::LogName, '*')
    $q.ReverseDirection = $true
    $reader = New-Object System.Diagnostics.Eventing.Reader.EventLogReader($q)
    $tally = @{}
    $shapes = @{}
    $messages = @{}
    $total = 0
    $noRecordId = 0
    try {
        while ($total -lt $Max) {
            $ev = $reader.ReadEvent()
            if ($null -eq $ev) { break }
            $total++
            $shape = Get-Shape ($ev.ToXml())
            if (-not $shape.HasRecordId) { $noRecordId++ }
            $msg = 'message'
            try {
                $d = $ev.FormatDescription()
                if ([string]::IsNullOrEmpty($d)) { $msg = 'no-message' }
            }
            catch { $msg = 'message-error' }
            $key = "{0}`t{1}`t{2}`t{3}" -f $ev.ProviderName, $ev.Id, $shape.Shape, $msg
            if ($tally.ContainsKey($key)) { $tally[$key]++ } else { $tally[$key] = 1 }
            $family = ($shape.Shape -replace '\d+-', '')
            if ($shapes.ContainsKey($family)) { $shapes[$family]++ } else { $shapes[$family] = 1 }
            if ($messages.ContainsKey($msg)) { $messages[$msg]++ } else { $messages[$msg] = 1 }
            $ev.Dispose()
        }
    }
    finally { $reader.Dispose() }

    Say ''
    Say "SURVEY of the newest $total events of ${Log}:"
    Say "  events with no record number in their XML: $noRecordId"
    Say '  by shape of the payload:'
    foreach ($k in ($shapes.Keys | Sort-Object)) { Say ('    {0,6}  {1}' -f $shapes[$k], $k) }
    Say '  by whether Windows could produce a readable message:'
    foreach ($k in ($messages.Keys | Sort-Object)) { Say ('    {0,6}  {1}' -f $messages[$k], $k) }
    $rows = New-Object System.Collections.ArrayList
    foreach ($k in $tally.Keys) { [void]$rows.Add(('{0}' -f $tally[$k]).PadLeft(6) + "`t" + $k) }
    $sorted = @($rows | Sort-Object -Descending)
    Save-Text "survey-$Log.tsv" ("count`tprovider`tnumber`tshape`tmessage`n" + ($sorted -join "`n") + "`n")
    Say '  the 25 commonest kinds (count, provider, number, shape, message):'
    $shown = 0
    foreach ($row in $sorted) {
        if ($shown -ge 25) { break }
        Say "    $row"
        $shown++
    }
    Say '  kinds that have unnamed values, or no readable message (up to 25):'
    $shown = 0
    foreach ($row in $sorted) {
        if ($shown -ge 25) { break }
        if ($row -match 'unnamed' -or $row -match 'no-message' -or $row -match 'message-error') {
            Say "    $row"
            $shown++
        }
    }
    if ($shown -eq 0) { Say '    (none in this sample)' }
}

# ---------------------------------------------------------------------------------------------
# The probe
# ---------------------------------------------------------------------------------------------

Say "aigolog Windows Event Log probe. Started (UTC, less 5 s): $($script:Since)"
Say "Capture folder: $($script:Cap)"

Section 'prepare' {
    Add-Type -TypeDefinition $script:NativeSource -ErrorAction Stop
    Say 'Compiled the small native-call helper.'
}

Section '1 the machine' {
    Probe 'operating system' {
        $os = Get-CimInstance Win32_OperatingSystem
        Say ("OS: {0} | version {1} | build {2} | {3}" -f $os.Caption, $os.Version, $os.BuildNumber, $os.OSArchitecture)
        Say ("Processor architecture: {0}; 64-bit process: {1}" -f $env:PROCESSOR_ARCHITECTURE, [Environment]::Is64BitProcess)
        Say ("PowerShell: {0} (CLR {1})" -f $PSVersionTable.PSVersion, $PSVersionTable.CLRVersion)
        $ti = [System.Globalization.CultureInfo]::CurrentCulture.TextInfo
        Say ("Culture: {0}; OEM code page {1}; ANSI code page {2}" -f [System.Globalization.CultureInfo]::CurrentCulture.Name, $ti.OEMCodePage, $ti.ANSICodePage)
    }
    Probe 'who is running this' {
        $id = [System.Security.Principal.WindowsIdentity]::GetCurrent()
        $pr = New-Object System.Security.Principal.WindowsPrincipal($id)
        $isAdmin = $pr.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
        Say ("Running as: {0}; holds the Administrators role: {1}; session {2}" -f $id.Name, $isAdmin, [System.Diagnostics.Process]::GetCurrentProcess().SessionId)
        $lua = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System' -ErrorAction SilentlyContinue).EnableLUA
        Say "UAC registry setting EnableLUA: $lua (0 means UAC is off)"
        [void](Invoke-Tool -File 'whoami.exe' -Arguments '/groups' -SaveAs 'machine-whoami-groups.txt')
    }
    Probe 'build tools on the path' {
        foreach ($t in 'go', 'git', 'gcc', 'clang', 'cl', 'zig') {
            $c = Get-Command $t -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($c) { Say "TOOL $t -> $($c.Path)" } else { Say "TOOL $t -> not on the path" }
        }
        $go = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($go) {
            [void](Invoke-Tool -File $go.Path -Arguments 'version')
            [void](Invoke-Tool -File $go.Path -Arguments 'env GOOS GOARCH CGO_ENABLED CC')
        }
        $gcc = Get-Command gcc -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($gcc) { [void](Invoke-Tool -File $gcc.Path -Arguments '--version' -MaxShow 300) }
        $git = Get-Command git -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($git) { [void](Invoke-Tool -File $git.Path -Arguments '--version') }
    }
    Probe 'which accounts may log on, and how (user rights of this machine)' {
        $cfg = Join-Path $script:TempRoot 'user-rights.inf'
        [void](Invoke-Tool -File 'secedit.exe' -Arguments ('/export /cfg "{0}" /areas USER_RIGHTS' -f $cfg) -ShowOutput $false)
        if (Test-Path $cfg) {
            $rights = @(Get-Content -Path $cfg | Where-Object { $_ -match '^Se(Deny)?(Interactive|Batch|Network|RemoteInteractive|Service)LogonRight' })
            Say 'Logon rights (S-1-5-32-544 Administrators, S-1-5-32-545 Users, S-1-5-32-551 Backup Operators, S-1-1-0 Everyone):'
            foreach ($line in $rights) { Say "  $line" }
            if ($rights.Count -eq 0) { Say '  (no logon-right line found in what secedit wrote)' }
        }
        else { Say 'secedit wrote no file.' }
    }
    Probe 'the event log tool and the three logs' {
        $r = Invoke-Tool -File 'wevtutil.exe' -Arguments 'el' -SaveAs 'machine-channels.txt' -ShowOutput $false
        $n = @(($r.Text -split "`r?`n") | Where-Object { $_.Trim() }).Count
        Say "Channels listed by 'wevtutil el': $n"
        foreach ($log in 'Application', 'System', 'Security') {
            [void](Invoke-Tool -File 'wevtutil.exe' -Arguments "gl $log" -SaveAs "machine-gl-$log.txt")
            [void](Invoke-Tool -File 'wevtutil.exe' -Arguments "gli $log" -SaveAs "machine-gli-$log.txt")
        }
    }
    Probe 'what this machine audits before the probe changes anything' {
        [void](Invoke-Tool -File 'auditpol.exe' -Arguments '/get /category:*' -SaveAs 'machine-auditpol-before.txt')
    }
}

Section '2 failed logons' {
    Probe 'turn on auditing of logons (success and failure)' {
        [void](Invoke-Tool -File 'auditpol.exe' -Arguments "/set /subcategory:$($script:LogonAuditGuid) /success:enable /failure:enable")
        [void](Invoke-Tool -File 'auditpol.exe' -Arguments "/get /subcategory:$($script:LogonAuditGuid)")
    }
    Probe 'create the standard test account' { New-ProbeUser }
    Probe 'make failed logons, then one good one' {
        $attempts = New-Object System.Collections.ArrayList
        foreach ($i in 1..6) { [void]$attempts.Add(@('probe-ghost-a', 3)) }
        foreach ($i in 1..3) { [void]$attempts.Add(@('probe-ghost-b', 3)) }
        [void]$attempts.Add(@('probe-ghost-b', 2))
        if ($script:ProbeUserReady) {
            foreach ($i in 1..2) { [void]$attempts.Add(@($script:ProbeUser, 3)) }
            foreach ($i in 1..2) { [void]$attempts.Add(@($script:ProbeUser, 2)) }
        }
        foreach ($a in $attempts) {
            $tok = [IntPtr]::Zero
            $wrong = 'Wrong-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
            $rc = [ProbeNative]::Logon($a[0], '.', $wrong, $a[1], [ref]$tok)
            if ($tok -ne [IntPtr]::Zero) { [void][ProbeNative]::CloseHandle($tok) }
            if ($rc -ne 0) { $script:FailedLogonsMade++ }
            Say ("LogonUser name={0} logon type={1} wrong password -> Windows error {2}" -f $a[0], $a[1], $rc)
        }
        Say "Failed logons the probe made: $($script:FailedLogonsMade)"
        if ($script:ProbeUserReady) {
            $tok = [IntPtr]::Zero
            $rc = [ProbeNative]::Logon($script:ProbeUser, '.', $script:ProbePw, 3, [ref]$tok)
            if ($tok -ne [IntPtr]::Zero) { [void][ProbeNative]::CloseHandle($tok) }
            Say ("LogonUser name={0} logon type=3 right password -> Windows error {1}" -f $script:ProbeUser, $rc)
        }
        Start-Sleep -Seconds 4
    }
    Probe 'read the failed logons back (event 4625)' {
        $got = Get-ProbeFailedLogons 300
        $ev = $got.Own
        Say "QUERY on Security: $($got.Query)"
        Say "Events 4625 since the probe started: $($got.AllCount). Of those, for the probe's own test names: $($ev.Count). The probe made $($script:FailedLogonsMade) failed logons."
        Say 'Only the probe''s own are shown and saved.'
        $seen = @{}
        $i = 0
        foreach ($e in $ev) {
            $i++
            Save-Text ('security-4625-{0:D2}.xml' -f $i) $e.Xml
            $m = Get-DataMap $e.Xml
            $key = '{0} | logon type {1} | status {2} | sub-status {3}' -f $m['TargetUserName'], $m['LogonType'], $m['Status'], $m['SubStatus']
            if (-not $seen.ContainsKey($key)) {
                $seen[$key] = 0
                Say ''
                Show-Event $e ''
            }
            $seen[$key]++
        }
        Say ''
        Say 'Distinct (name | logon type | status | sub-status), and how many of each:'
        foreach ($k in ($seen.Keys | Sort-Object)) { Say ('  {0,3}  {1}' -f $seen[$k], $k) }
        if ($ev.Count -gt 0) {
            Say 'The values of the newest one, in order:'
            Say (Get-DataLines $ev[0].Xml)
        }
    }
    Probe 'read the good logon back (event 4624)' {
        $q = "*[System[(EventID=4624) and $($script:SinceClause)] and EventData[Data[@Name='TargetUserName']='$($script:ProbeUser)']]"
        Say "QUERY on Security: $q"
        $ev = Read-Log -Path 'Security' -XPath $q -Max 5 -WithMessage $false
        Say "Events 4624 for the test account since the probe started: $($ev.Count)"
        if ($ev.Count -gt 0) {
            Show-Event $ev[0] 'security-4624-01.xml'
            Say (Get-DataLines $ev[0].Xml)
        }
    }
    Probe 'how many 4625 the whole Security log holds' {
        $c = Read-Count -Path 'Security' -XPath '*[System[(EventID=4625)]]' -Max 100000
        Say "Events 4625 in the whole Security log (count only): $($c.Count)"
    }
}

Section '3 services that fail' {
    Probe 'method A: a test service that dies by itself' {
        $exe = Join-Path $script:TempRoot 'aigoprobesvc.exe'
        Add-Type -TypeDefinition $script:ServiceSource -ReferencedAssemblies 'System.ServiceProcess' -OutputAssembly $exe -OutputType ConsoleApplication -ErrorAction Stop
        Say "Compiled the test service: $exe ($((Get-Item $exe).Length) bytes)"
        [void](Invoke-Tool -File 'sc.exe' -Arguments ('create aigoprobesvc binPath= "{0}" start= demand' -f $exe))
        Say 'First start, with no recovery action set:'
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'start aigoprobesvc')
        Start-Sleep -Seconds 6
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'query aigoprobesvc')
        Say 'Now with the recovery action "restart after 2 seconds", left to loop for 16 seconds:'
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'failure aigoprobesvc reset= 3600 actions= restart/2000/restart/2000/restart/2000')
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'qfailure aigoprobesvc')
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'start aigoprobesvc')
        Start-Sleep -Seconds 16
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'failure aigoprobesvc reset= 0 actions= ""')
        Start-Sleep -Seconds 6
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'stop aigoprobesvc')
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'delete aigoprobesvc')
    }
    Probe 'method A2: services that cannot start' {
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'create aigoprobemissing binPath= "C:\aigolog-probe\no-such-file.exe" start= demand')
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'start aigoprobemissing')
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'delete aigoprobemissing')
        Say 'A program that is not a service (Windows waits about 30 seconds for it):'
        [void](Invoke-Tool -File 'sc.exe' -Arguments ('create aigoprobetimeout binPath= "{0} /c exit 1" start= demand' -f (Join-Path $env:SystemRoot 'System32\cmd.exe')))
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'start aigoprobetimeout')
        [void](Invoke-Tool -File 'sc.exe' -Arguments 'delete aigoprobetimeout')
    }
    Probe 'method B: kill a Windows service that has its own recovery actions' {
        $rows = New-Object System.Collections.ArrayList
        $cands = @(Get-CimInstance Win32_Service -Filter "State='Running'" | Where-Object { $_.ServiceType -eq 'Own Process' })
        foreach ($s in $cands) {
            $r = Invoke-Tool -File 'sc.exe' -Arguments ('qfailure "{0}"' -f $s.Name) -Quiet $true
            [void]$rows.Add(('  {0} (process {1}); a restart action is configured: {2}' -f $s.Name, $s.ProcessId, ($r.Text -match 'RESTART')))
        }
        Say "Running services that have a process of their own: $($cands.Count)"
        if ($rows.Count -gt 0) { Say ($rows -join "`n") }

        $target = 'Spooler'
        $svc = Get-CimInstance Win32_Service -Filter "Name='$target'"
        if (-not $svc) {
            Say "The service $target does not exist on this image, so method B was not run."
            return
        }
        Say "$target as found: state $($svc.State), start mode $($svc.StartMode), process $($svc.ProcessId)"
        if ($svc.StartMode -eq 'Disabled') {
            Say "$target is disabled on this image; setting it to manual start so the test can use it."
            Set-Service -Name $target -StartupType Manual -ErrorAction Stop
        }
        if ($svc.State -ne 'Running') {
            Say "Starting $target."
            Start-Service -Name $target -ErrorAction Stop
            Start-Sleep -Seconds 3
        }
        [void](Invoke-Tool -File 'sc.exe' -Arguments "qfailure $target")
        foreach ($round in 1, 2) {
            $svc = Get-CimInstance Win32_Service -Filter "Name='$target'"
            if ($svc.State -ne 'Running' -or -not $svc.ProcessId) {
                Say "Round ${round}: $target is not running (state $($svc.State)); stopping here."
                break
            }
            $oldProcess = $svc.ProcessId
            Say "Round ${round}: killing process $oldProcess of $target"
            Stop-Process -Id $oldProcess -Force -ErrorAction Stop
            $deadline = (Get-Date).AddSeconds(100)
            $back = $false
            while ((Get-Date) -lt $deadline) {
                Start-Sleep -Seconds 5
                $now = Get-CimInstance Win32_Service -Filter "Name='$target'"
                if ($now.State -eq 'Running' -and $now.ProcessId -and $now.ProcessId -ne $oldProcess) { $back = $true; break }
            }
            Say "Round ${round}: Windows started $target again with a new process within 100 s: $back"
            if (-not $back) { break }
        }
    }
    Probe 'read the Service Control Manager events back' {
        $q = "*[System[Provider[@Name='Service Control Manager'] and $($script:SinceClause)]]"
        Say "QUERY on System: $q"
        $ev = Read-Log -Path 'System' -XPath $q -Max 400 -WithMessage $true
        Say "Service Control Manager events since the probe started: $($ev.Count)"
        $byId = @{}
        foreach ($e in $ev) {
            $k = [int]$e.Id
            if (-not $byId.ContainsKey($k)) { $byId[$k] = New-Object System.Collections.ArrayList }
            [void]$byId[$k].Add($e)
        }
        foreach ($k in ($byId.Keys | Sort-Object)) {
            $list = $byId[$k]
            Say ''
            Say ("##### event number {0}: {1} of them, level {2} #####" -f $k, $list.Count, $list[0].Level)
            $n = 0
            foreach ($e in $list) {
                $n++
                if ($n -le 3) { Save-Text ('system-scm-{0}-{1:D2}.xml' -f $k, $n) $e.Xml }
            }
            Show-Event $list[0] ''
            Say 'Its values, in order:'
            Say (Get-DataLines $list[0].Xml)
        }
        foreach ($k in 7031, 7034) {
            if ($byId.ContainsKey($k)) {
                Say ''
                Say "Every event $k since the probe started, oldest last, with its values (is the restart count among them?):"
                foreach ($e in $byId[$k]) {
                    Say ("  record {0} at {1:o}" -f $e.RecordId, $e.Time)
                    Say (Get-DataLines $e.Xml)
                }
            }
            else { Say "No event $k since the probe started." }
        }
    }
    Probe 'which Service Control Manager events the whole System log holds' {
        $all = Read-Log -Path 'System' -XPath "*[System[Provider[@Name='Service Control Manager']]]" -Max 5000
        $count = @{}
        foreach ($e in $all) {
            $k = [int]$e.Id
            if ($count.ContainsKey($k)) { $count[$k]++ } else { $count[$k] = 1 }
        }
        Say "Service Control Manager events in the System log (up to 5000 newest): $($all.Count). Event number and how many:"
        foreach ($k in ($count.Keys | Sort-Object)) { Say ('  {0,6}  x {1}' -f $k, $count[$k]) }
    }
}

Section '4 events with unnamed values, and events with no readable message' {
    Probe 'write test events from a source that nobody registered' {
        $rc1 = [ProbeNative]::Report('aigolog-probe-unregistered', 2, 4242, [string[]]@('first value', 'second value'))
        $rc2 = [ProbeNative]::Report('aigolog-probe-unregistered', 1, 4243, [string[]]@('only value'))
        $rc3 = [ProbeNative]::Report('aigolog-probe-unregistered', 4, 4244, [string[]]@())
        Say "ReportEvent results for events 4242, 4243, 4244 (0 means written): $rc1, $rc2, $rc3"
        [void](Invoke-Tool -File 'eventcreate.exe' -Arguments '/T WARNING /ID 999 /L APPLICATION /SO aigolog-probe-eventcreate /D "probe text with the number 42"')
        Start-Sleep -Seconds 3
    }
    Probe 'read them back through the API' {
        $q = "*[System[Provider[@Name='aigolog-probe-unregistered' or @Name='aigolog-probe-eventcreate']]]"
        Say "QUERY on Application: $q"
        $ev = Read-Log -Path 'Application' -XPath $q -Max 10 -WithMessage $true
        Say "Found: $($ev.Count) (the probe wrote 4)"
        $i = 0
        foreach ($e in $ev) {
            $i++
            Say ''
            Show-Event $e ('application-probe-{0}-{1:D2}.xml' -f $e.Id, $i)
            Say (Get-DataLines $e.Xml)
        }
    }
    Probe 'the unregistered event as wevtutil shows it, three ways' {
        $q = "*[System[Provider[@Name='aigolog-probe-unregistered'] and (EventID=4242)]]"
        foreach ($f in 'text', 'xml', 'RenderedXml') {
            [void](Invoke-Tool -File 'wevtutil.exe' -Arguments ('qe Application "/q:{0}" /c:1 /rd:true /f:{1}' -f $q, $f) -SaveAs "wevtutil-application-4242-$f.txt")
        }
    }
    Probe 'an event whose values hold awkward characters' {
        $esc = [string][char]27
        $values = [string[]]@(
            ('tab' + [char]9 + 'inside'),
            ('line one' + [char]10 + 'line two'),
            'ampersand & less-than < greater-than > quote " apostrophe ''',
            ($esc + '[31mred' + $esc + '[0m and a bell ' + [char]7),
            ('greek ' + [char]0x03B5 + [char]0x03BB + [char]0x03BB + ' and a face ' + [char]0xD83D + [char]0xDE00),
            ''
        )
        $rc = [ProbeNative]::Report('aigolog-probe-unregistered', 4, 4245, $values)
        Say "ReportEvent result for event 4245, six values (0 means written): $rc"
        Say 'The six values written: a tab inside; a line feed inside; & < > " and an apostrophe; an escape'
        Say 'character (U+001B) twice and a bell (U+0007); three Greek letters and one character outside'
        Say 'the basic plane (U+D83D U+DE00); an empty value.'
        Start-Sleep -Seconds 2
        $ev = Read-Log -Path 'Application' -XPath "*[System[Provider[@Name='aigolog-probe-unregistered'] and (EventID=4245)]]" -Max 1
        if ($ev.Count -eq 0) {
            Say 'The event was not found.'
            return
        }
        $xml = $ev[0].Xml
        Save-Text 'application-probe-4245-awkward.xml' $xml
        Say 'Its XML as the API returned it; every character outside printable ASCII is written {U+XXXX}:'
        Say (Show-Codes $xml)
        try {
            $settings = New-Object System.Xml.XmlReaderSettings
            $settings.CheckCharacters = $true
            $xr = [System.Xml.XmlReader]::Create((New-Object System.IO.StringReader($xml)), $settings)
            try { while ($xr.Read()) { } } finally { $xr.Dispose() }
            Say 'A reader that checks every character against the XML rules (.NET XmlReader, CheckCharacters on) ACCEPTS this XML.'
        }
        catch {
            Say 'A reader that checks every character against the XML rules (.NET XmlReader, CheckCharacters on) REFUSES this XML:'
            Say (Format-Failure $_)
        }
        try {
            $doc = New-Object System.Xml.XmlDocument
            $doc.LoadXml($xml)
            Say 'A lenient reader (.NET XmlDocument, which does not check characters) reads these values back:'
            $i = 0
            foreach ($n in $doc.DocumentElement.SelectNodes("*[local-name()='EventData']/*")) {
                $i++
                Say ('  {0}. {1}' -f $i, (Show-Codes $n.InnerText))
            }
        }
        catch {
            Say 'A lenient reader (.NET XmlDocument) REFUSES this XML too:'
            Say (Format-Failure $_)
        }
    }
    Probe 'survey of what the three logs already hold' {
        Survey-Log 'Application' 500
        Survey-Log 'System' 500
        Survey-Log 'Security' 500
    }
}

Section '5 what a standard user may read' {
    Say 'The account running the probe is elevated (section 1). What follows is a STANDARD USER: a member'
    Say 'of Users only. An administrator who is not elevated is section 5b.'
    if (-not $script:ProbeUserReady) {
        Probe 'create the standard test account' { New-ProbeUser }
    }
    if (-not $script:ProbeUserReady) { throw 'there is no test account, so this section cannot run' }
    Probe 'control: the account running the probe reads one event from each log' {
        foreach ($log in 'Application', 'System', 'Security') {
            $r = Read-Log -Path $log -XPath '*' -Max 1
            Say "$log : read $($r.Count) event(s) as the account running the probe"
        }
    }
    Probe 'standard user, as a separate process' { Test-ChildRead 'member of Users only' $script:ProbeUser $script:ProbePw }
    Probe 'standard user, through the API' { Test-ImpersonatedRead 'member of Users only' $script:ProbeUser $script:ProbePw @(2, 4, 3) $false }
    Probe 'put the standard user in the group Event Log Readers' {
        Add-LocalGroupMember -SID 'S-1-5-32-573' -Member $script:ProbeUser -ErrorAction Stop
        Say 'Added to Event Log Readers (S-1-5-32-573).'
    }
    Probe 'standard user in Event Log Readers, as a separate process' { Test-ChildRead 'member of Users and Event Log Readers' $script:ProbeUser $script:ProbePw }
    Probe 'standard user in Event Log Readers, through the API' { Test-ImpersonatedRead 'member of Users and Event Log Readers' $script:ProbeUser $script:ProbePw @(2, 4, 3) $false }
}

Section '5b an administrator who is not elevated' {
    Say 'UAC is on here (EnableLUA is 1, section 1). The account running the probe is the built-in'
    Say 'Administrator, which Windows does not filter. A second account in Administrators shows what'
    Say 'an ordinary administrator gets from an ordinary prompt, and with each other kind of logon.'
    Probe 'create a test account that is a member of Administrators' { New-ProbeAdmin }
    if (-not $script:AdminReady) { throw 'there is no administrator test account, so this section cannot run' }
    Probe 'administrator account, as a separate process (the nearest thing to an ordinary prompt)' {
        Test-ChildRead 'member of Administrators, started with name and password, nothing elevated' $script:AdminUser $script:AdminPw
    }
    Probe 'administrator account, through the API, with each kind of logon' {
        Test-ImpersonatedRead 'member of Administrators' $script:AdminUser $script:AdminPw @(2, 4, 3) $true
    }
}

Section '5c a file written with full rights, opened with filtered rights' {
    if (-not $script:AdminReady) { throw 'there is no administrator test account, so this section cannot run' }
    Probe 'give the administrator test account a profile and find its local application data folder' {
        $r = Invoke-Tool -File 'cmd.exe' -Arguments '/c echo %LOCALAPPDATA%' -AsUser $script:AdminUser -Password $script:AdminPw -LoadProfile $true
        $path = $r.Text.Trim()
        if (-not $path -or $path -like '*%*' -or -not (Test-Path -LiteralPath $path)) {
            $guess = Join-Path $env:SystemDrive ("Users\{0}\AppData\Local" -f $script:AdminUser)
            Say "The child did not give a usable folder ('$path'); trying $guess"
            $path = $guess
        }
        if (Test-Path -LiteralPath $path) {
            $script:AdminLocal = $path
            Say "Local application data folder of the test account: $path"
            [void](Invoke-Tool -File 'icacls.exe' -Arguments ('"{0}"' -f $path))
        }
        else { Say "That folder does not exist: $path" }
    }
    Probe 'write a file there with the full token, then open it with the filtered token' {
        if (-not $script:AdminLocal) { throw 'no local application data folder for the test account' }
        $dir = Join-Path $script:AdminLocal 'aigolog-probe'
        $file = Join-Path $dir 'written-with-full-rights.db'

        $tokFull = [IntPtr]::Zero
        $idFull = $null
        $useFull = $false
        $rc = [ProbeNative]::Logon($script:AdminUser, '.', $script:AdminPw, 4, [ref]$tokFull)
        if ($rc -eq 0) {
            $idFull = New-Object System.Security.Principal.WindowsIdentity($tokFull)
            $useFull = (@($idFull.Groups | ForEach-Object { $_.Value }) -contains 'S-1-5-32-544')
            Say ("Batch-logon token of the test account: elevation type {0}; Administrators enabled: {1}" -f [ProbeNative]::ElevationType($tokFull), $useFull)
        }
        else { Say "Batch logon of the test account failed with Windows error $rc." }

        if ($useFull) {
            $ctx = $idFull.Impersonate()
            try {
                [void][System.IO.Directory]::CreateDirectory($dir)
                [System.IO.File]::WriteAllText($file, 'written with full rights')
            }
            finally { $ctx.Undo() }
            Say 'The file was written by: the test account itself, with its full (unfiltered) token.'
        }
        else {
            [void][System.IO.Directory]::CreateDirectory($dir)
            [System.IO.File]::WriteAllText($file, 'written by the probe account')
            Say 'The file was written by: the account running the probe (a DIFFERENT administrator, elevated), because the test account gave no full token.'
        }
        if ($null -ne $idFull) { $idFull.Dispose() }
        if ($tokFull -ne [IntPtr]::Zero) { [void][ProbeNative]::CloseHandle($tokFull) }

        Say ("Owner of the file: {0}" -f (Get-Acl -LiteralPath $file).Owner)
        [void](Invoke-Tool -File 'icacls.exe' -Arguments ('"{0}"' -f $file))

        $tokLim = [IntPtr]::Zero
        $rc = [ProbeNative]::Logon($script:AdminUser, '.', $script:AdminPw, 2, [ref]$tokLim)
        if ($rc -ne 0) { throw "interactive logon of the test account failed with Windows error $rc" }
        $idLim = New-Object System.Security.Principal.WindowsIdentity($tokLim)
        try {
            $adminOn = (@($idLim.Groups | ForEach-Object { $_.Value }) -contains 'S-1-5-32-544')
            Say ("Interactive-logon token of the test account: elevation type {0}; Administrators enabled: {1}" -f [ProbeNative]::ElevationType($tokLim), $adminOn)
            $results = New-Object System.Collections.ArrayList
            $ctx = $idLim.Impersonate()
            try {
                foreach ($mode in 'Read', 'ReadWrite') {
                    try {
                        $fs = [System.IO.File]::Open($file, [System.IO.FileMode]::Open, [System.IO.FileAccess]$mode)
                        $fs.Dispose()
                        [void]$results.Add("open the file for ${mode}: ALLOWED")
                    }
                    catch { [void]$results.Add("open the file for ${mode}: REFUSED`n" + (Format-Failure $_)) }
                }
                try {
                    [System.IO.File]::WriteAllText((Join-Path $dir 'written-with-filtered-rights.db-journal'), 'x')
                    [void]$results.Add('create a second file beside it: ALLOWED')
                }
                catch { [void]$results.Add("create a second file beside it: REFUSED`n" + (Format-Failure $_)) }
                try {
                    [System.IO.File]::Delete($file)
                    [void]$results.Add('delete the file: ALLOWED')
                }
                catch { [void]$results.Add("delete the file: REFUSED`n" + (Format-Failure $_)) }
            }
            finally { $ctx.Undo() }
            Say 'RESULT, with the filtered token of the same account:'
            foreach ($line in $results) { Say "  $line" }
        }
        finally {
            $idLim.Dispose()
            [void][ProbeNative]::CloseHandle($tokLim)
        }
    }
}

Section '6 does the readable message come with the event' {
    Probe 'message of one failed logon (the probe''s own)' {
        $got = Get-ProbeFailedLogons 300
        if ($got.Own.Count -eq 0) {
            Say 'There is no failed logon of the probe''s own to look at.'
            return
        }
        Show-MessageForms 'Security' $got.Own[0] 'security-4625'
    }
    Probe 'message of one service crash' {
        $q = "*[System[Provider[@Name='Service Control Manager'] and (EventID=7031 or EventID=7034) and $($script:SinceClause)]]"
        $ev = Read-Log -Path 'System' -XPath $q -Max 1 -WithMessage $true
        if ($ev.Count -eq 0) {
            Say "No event matched on System: $q"
            return
        }
        Show-MessageForms 'System' $ev[0] 'system-scm-crash'
    }
}

Section '7 bounding a read, and how the API refuses' {
    Probe 'newest and oldest of System' {
        $newest = Read-Log -Path 'System' -XPath '*' -Max 3 -NewestFirst $true
        $oldest = Read-Log -Path 'System' -XPath '*' -Max 3 -NewestFirst $false
        Say ('Newest three, as returned newest first (record number @ time): ' + (($newest | ForEach-Object { '{0} @ {1:o}' -f $_.RecordId, $_.Time }) -join '; '))
        Say ('Oldest three, as returned oldest first (record number @ time): ' + (($oldest | ForEach-Object { '{0} @ {1:o}' -f $_.RecordId, $_.Time }) -join '; '))
        if ($newest.Count -gt 0) { $script:NewestSystemRecord = $newest[0].RecordId }
    }
    Probe 'bound by record number' {
        if ($null -eq $script:NewestSystemRecord) { throw 'no newest record number from the probe before' }
        $n = $script:NewestSystemRecord - 3
        $q = "*[System[EventRecordID > $n]]"
        $r = Read-Log -Path 'System' -XPath $q -Max 100 -NewestFirst $false
        Say ("QUERY on System: {0} -> {1} events (3 or a few more expected); record numbers: {2}" -f $q, $r.Count, (($r | ForEach-Object { $_.RecordId }) -join ', '))
    }
    Probe 'bound by age (the last hour)' {
        $q = '*[System[TimeCreated[timediff(@SystemTime) <= 3600000]]]'
        $c = Read-Count -Path 'System' -XPath $q -Max 50000
        Say "QUERY on System: $q -> $($c.Count) events"
    }
    Probe 'bound by a moment in time (since the probe started)' {
        $q = "*[System[$($script:SinceClause)]]"
        $c = Read-Count -Path 'System' -XPath $q -Max 50000
        Say "QUERY on System: $q -> $($c.Count) events"
    }
    Probe 'the last seven days of each log' {
        $q = '*[System[TimeCreated[timediff(@SystemTime) <= 604800000]]]'
        foreach ($log in 'Application', 'System', 'Security') {
            $c = Read-Count -Path $log -XPath $q -Max 200000
            Say "${log}: $($c.Count) events in the last 7 days (count stops at 200000)"
        }
    }
    Probe 'a query that is not valid' {
        try {
            [void](Read-Log -Path 'System' -XPath '*[System[(EventID=' -Max 1)
            Say 'UNEXPECTED: the malformed query was accepted.'
        }
        catch {
            Say 'A malformed query is refused like this:'
            Say (Format-Failure $_)
        }
    }
    Probe 'a log that does not exist' {
        try {
            [void](Read-Log -Path 'aigolog-no-such-log' -XPath '*' -Max 1)
            Say 'UNEXPECTED: reading a log that does not exist worked.'
        }
        catch {
            Say 'A log that does not exist is refused like this:'
            Say (Format-Failure $_)
        }
    }
    Probe 'a file that is not an event log' {
        $bogus = Join-Path $script:TempRoot 'not-an-event-log.evtx'
        [System.IO.File]::WriteAllText($bogus, 'this is not an event log file')
        try {
            [void](Read-Log -Path $bogus -IsFile $true -Max 1)
            Say 'UNEXPECTED: reading a file that is not an event log worked.'
        }
        catch {
            Say 'A file that is not an event log is refused like this:'
            Say (Format-Failure $_)
        }
    }
    Probe 'save the probe''s own events as event log files, and read those files back' {
        $ownNames = ($script:ProbeNames | ForEach-Object { "Data[@Name='TargetUserName']='$_'" }) -join ' or '
        $exports = @(
            @('Security', 'security-4625-since-start.evtx', "*[System[(EventID=4625) and $($script:SinceClause)] and EventData[$ownNames]]"),
            @('System', 'system-scm-since-start.evtx', "*[System[Provider[@Name='Service Control Manager'] and $($script:SinceClause)]]"),
            @('Application', 'application-probe-sources.evtx', "*[System[Provider[@Name='aigolog-probe-unregistered' or @Name='aigolog-probe-eventcreate']]]")
        )
        foreach ($x in $exports) {
            $out = Join-Path $script:Cap $x[1]
            [void](Invoke-Tool -File 'wevtutil.exe' -Arguments ('epl {0} "{1}" "/q:{2}"' -f $x[0], $out, $x[2]))
            if (Test-Path $out) {
                $back = Read-Log -Path $out -IsFile $true -Max 1000 -NewestFirst $false -WithMessage $true
                $withMessage = @($back | Where-Object { -not [string]::IsNullOrEmpty($_.Message) }).Count
                Say ("FILE {0}: {1} bytes; read back {2} events through the API; {3} of them came with a readable message; record numbers {4} to {5}" -f $x[1], (Get-Item $out).Length, $back.Count, $withMessage, $(if ($back.Count -gt 0) { $back[0].RecordId } else { '-' }), $(if ($back.Count -gt 0) { $back[$back.Count - 1].RecordId } else { '-' }))
            }
            else { Say "FILE $($x[1]): was not written" }
        }
    }
    Probe 'rough speed of reading (a lead, not a measurement)' {
        foreach ($run in 1, 2) {
            $sw = [System.Diagnostics.Stopwatch]::StartNew()
            $c = Read-Count -Path 'Security' -XPath '*' -Max 3000 -Render $true
            $sw.Stop()
            Say ("Run {0}: {1} Security events read and rendered to XML ({2} characters) in {3} ms" -f $run, $c.Count, $c.Characters, $sw.ElapsedMilliseconds)
        }
        Say 'ROUGH ONLY: a shared runner, through .NET rather than Go, background load not controlled.'
    }
    Probe 'does a read that is already open see events written afterwards' {
        $xp = "*[System[Provider[@Name='aigolog-probe-unregistered']]]"
        $n0 = (Read-Count -Path 'Application' -XPath $xp -Max 10000).Count
        $q = New-Object System.Diagnostics.Eventing.Reader.EventLogQuery('Application', [System.Diagnostics.Eventing.Reader.PathType]::LogName, $xp)
        $q.ReverseDirection = $false
        $reader = New-Object System.Diagnostics.Eventing.Reader.EventLogReader($q)
        try {
            $got = 0
            while ($got -lt $n0) {
                $ev = $reader.ReadEvent()
                if ($null -eq $ev) { break }
                $got++
                $ev.Dispose()
            }
            Say "The log held $n0 events of the probe's source. Read $got of them from one open query, oldest first, and did not ask for more."
            $rc = [ProbeNative]::Report('aigolog-probe-unregistered', 4, 4246, [string[]]@('written after the read caught up'))
            Start-Sleep -Seconds 2
            $extra = 0
            while ($extra -lt 5) {
                $ev = $reader.ReadEvent()
                if ($null -eq $ev) { break }
                $extra++
                $ev.Dispose()
            }
            Say "Then one more event was written (result $rc, 0 means written). Asking the SAME open query for more returned $extra event(s)."
            if ($extra -gt 0) { Say 'So an open query DOES hand over events written after it was opened: a read needs its own stopping rule.' }
            else { Say 'So an open query does NOT hand over events written after it was opened.' }
        }
        finally { $reader.Dispose() }
    }
    Probe 'rough cost of asking for the readable message as well (a lead, not a measurement)' {
        foreach ($run in 1, 2) {
            $sw = [System.Diagnostics.Stopwatch]::StartNew()
            $c = Read-Count -Path 'Security' -XPath '*' -Max 3000 -Render $true -Message $true
            $sw.Stop()
            Say ("Run {0}: {1} Security events read, rendered to XML and formatted to a message ({2} characters) in {3} ms" -f $run, $c.Count, $c.Characters, $sw.ElapsedMilliseconds)
        }
        Say 'ROUGH ONLY, as above. Compare with the XML-only figure in the probe before this one.'
    }
}

Section '8 tidy up and list what was captured' {
    Probe 'remove the test account' {
        if ($script:ProbeUserReady) {
            Remove-LocalUser -Name $script:ProbeUser -ErrorAction Stop
            Say "Removed the local account $($script:ProbeUser)."
        }
    }
    Probe 'remove the administrator test account' {
        if ($script:AdminReady) {
            Remove-LocalUser -Name $script:AdminUser -ErrorAction Stop
            Say "Removed the local account $($script:AdminUser)."
        }
    }
    Probe 'captured files' {
        foreach ($f in (Get-ChildItem -Path $script:Cap | Sort-Object Name)) { Say ('  {0,9}  {1}' -f $f.Length, $f.Name) }
    }
}

Say ''
Say 'NOT MEASURED BY THIS PROBE: reading speed in Go; a real desktop session with a consent prompt.'
Say ("Sections that failed as a whole: {0}" -f $(if ($script:FailedSections.Count) { $script:FailedSections -join '; ' } else { 'none' }))
Say ("Probes that failed: {0}" -f $(if ($script:FailedProbes.Count) { $script:FailedProbes -join '; ' } else { 'none' }))
Say 'End of the aigolog Windows Event Log probe.'
Save-Report
exit 0
