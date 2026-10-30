; Built only on the native Windows runner. Signing is a separate owner gate.
#define Payload GetEnv("FASED_PACKAGE_PAYLOAD")
#define Output GetEnv("FASED_PACKAGE_OUTPUT")
[Setup]
AppId=org.fased.app
AppName=Fased App Engineering
AppVersion=0.0.0
DefaultDirName={localappdata}\Programs\Fased App
DefaultGroupName=Fased App
PrivilegesRequired=lowest
OutputDir={#Output}
OutputBaseFilename=fased-app-windows-setup-unsigned
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayName=Fased App Engineering (keeps workspace data)
[Files]
Source: "{#Payload}\*"; DestDir: "{app}\bundle"; Flags: ignoreversion
[Icons]
Name: "{group}\Fased App"; Filename: "{app}\bundle\fased-desktop.exe"
Name: "{group}\Uninstall Fased App"; Filename: "{uninstallexe}"
[Run]
Filename: "{app}\bundle\fased-desktop.exe"; Description: "Open Fased App"; Flags: postinstall nowait skipifsilent
[Code]
var
  CoreInstallAttempted: Boolean;
  CoreInstallConfirmed: Boolean;

function GetCustomSetupExitCode(): Integer;
begin
  Result := 0;
  if CoreInstallAttempted and (not CoreInstallConfirmed) then Result := 1;
end;

function InitializeUninstall(): Boolean;
var Code: Integer;
begin
  Result := ExecAndLogOutput(ExpandConstant('{app}\bundle\fased-app.exe'), 'uninstall', '', SW_HIDE, ewWaitUntilTerminated, Code, nil);
  if Result then Result := Code = 0;
  if (not Result) and (not UninstallSilent) then
    MsgBox('Fased core removal was refused. Application and instance data are preserved. Inspect the CLI uninstall error before retrying.', mbError, MB_OK);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Code: Integer;
begin
  if CurStep = ssPostInstall then
  begin
    CoreInstallAttempted := True;
    Code := 0;
    CoreInstallConfirmed := ExecAndLogOutput(ExpandConstant('{app}\bundle\fased-app.exe'), 'install --bundle "' + ExpandConstant('{app}\bundle') + '"', '', SW_HIDE, ewWaitUntilTerminated, Code, nil);
    if CoreInstallConfirmed then CoreInstallConfirmed := Code = 0;
    if not CoreInstallConfirmed then
    begin
      Log('Fased core installation was not confirmed (exit code ' + IntToStr(Code) + ').');
      RaiseException('Fased core installation was not confirmed. Existing workspace data is preserved; inspect the CLI installation error before retrying.');
    end;
    Log('Fased core installation confirmed.');
  end;
end;
