# package-msi.ps1 builds the Windows installer for one architecture from
# GoReleaser's zip archive in Dist, writing canopy_<version>_windows_<arch>.msi
# beside it. It needs the WiX toolset (`dotnet tool install --global wix`).
#
# Usage: scripts/package-msi.ps1 -Version 1.2.3 -Arch amd64 [-Dist dist]
param(
    [Parameter(Mandatory)][string]$Version,
    [Parameter(Mandatory)][ValidateSet('amd64', 'arm64')][string]$Arch,
    [string]$Dist = 'dist'
)
$ErrorActionPreference = 'Stop'

$archive = Join-Path $Dist "canopy_${Version}_windows_${Arch}.zip"
$work = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
try {
    Expand-Archive -Path $archive -DestinationPath $work

    # An MSI version is numeric major.minor.patch, so a pre-release suffix
    # (1.2.3-rc.1) is dropped; the file name keeps the full version.
    $msiVersion = ($Version -split '-', 2)[0]
    $wixArch = @{ amd64 = 'x64'; arm64 = 'arm64' }[$Arch]
    $out = Join-Path $Dist "canopy_${Version}_windows_${Arch}.msi"

    # WiX 7 refuses to run until its Open Source Maintenance Fee EULA is
    # accepted: https://docs.firegiant.com/wix/osmf/
    wix build -acceptEula wix7 -arch $wixArch -d "Version=$msiVersion" -d "BinDir=$work" `
        -o $out (Join-Path $PSScriptRoot 'canopy.wxs')
    if ($LASTEXITCODE -ne 0) { throw "wix build failed with exit code $LASTEXITCODE" }
}
finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $work
}
