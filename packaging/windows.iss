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
Source: "{#Payload}\*"; DestDir: "{app}"; Flags: ignoreversion
[Icons]
Name: "{group}\Fased App"; Filename: "{app}\fased-desktop.exe"
Name: "{group}\Uninstall Fased App"; Filename: "{uninstallexe}"
[Run]
Filename: "{app}\fased-desktop.exe"; Description: "Open Fased App"; Flags: postinstall nowait skipifsilent
[Code]
function InitializeUninstall(): Boolean;
var Code: Integer;
begin
  Result := Exec(ExpandConstant('{app}\fased-app.exe'), 'uninstall', '', SW_HIDE, ewWaitUntilTerminated, Code);
  if Result then Result := Code = 0;
  if (not Result) and (not UninstallSilent) then
    MsgBox('Fased core removal was refused. Application and instance data are preserved. Inspect the CLI uninstall error before retrying.', mbError, MB_OK);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Code: Integer;
begin
  if CurStep = ssPostInstall then
    if (not Exec(ExpandConstant('{app}\fased-app.exe'), 'install --bundle "' + ExpandConstant('{app}') + '"', '', SW_HIDE, ewWaitUntilTerminated, Code)) or (Code <> 0) then
      RaiseException('Fased core installation was not confirmed. Existing workspace data is preserved; inspect the CLI installation error before retrying.');
end;
