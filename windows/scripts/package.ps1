<#
.SYNOPSIS
  Builds Bowtie for Windows: a signed MSIX bundle (x64 + ARM64) and
  unpackaged x64 / ARM64 zips, into windows/out/dist.

.DESCRIPTION
  Run from a "Developer PowerShell for VS 2022" (or anywhere msbuild.exe is
  on PATH) with the .NET 8 SDK and the Windows 10/11 SDK installed. CI runs
  this same script (.github/workflows/windows.yml).

  Signing: with -PfxPath/-PfxPassword (or the WINDOWS_PFX_B64 and
  WINDOWS_PFX_PASSWORD environment variables) the packages are signed with
  that certificate; otherwise a fresh self-signed "CN=Bowtie" certificate is
  made for this build. Either way the public certificate is written next to
  the bundle as a .cer so people can trust it before installing (see
  windows/README.md). The manifest's Publisher is set to the certificate's
  subject, which MSIX requires.

.PARAMETER Version
  Three-part version (0.11.0). The package version becomes 0.11.0.0.

.PARAMETER Label
  Text used in file names (defaults to the version).
#>
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$Label = $Version,
    [string]$PfxPath = "",
    [string]$PfxPassword = "",
    [string[]]$Platforms = @("x64", "ARM64")
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$windows = Split-Path -Parent $PSScriptRoot
$project = Join-Path $windows "Bowtie.App\Bowtie.App.csproj"
$manifest = Join-Path $windows "Bowtie.App\Package.appxmanifest"
$out = Join-Path $windows "out"
$dist = Join-Path $out "dist"
Remove-Item $out -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $dist | Out-Null

if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "Version must look like 1.2.3, got '$Version'" }
$packageVersion = "$Version.0"

function Invoke-Checked([string]$exe, [string[]]$arguments) {
    Write-Host ">> $exe $($arguments -join ' ')"
    & $exe @arguments
    if ($LASTEXITCODE -ne 0) { throw "$exe failed with exit code $LASTEXITCODE" }
}

function Reset-AppBuild {
    # Packaged and unpackaged builds generate different sources into obj/; start clean.
    foreach ($dir in @("bin", "obj")) {
        Remove-Item (Join-Path $windows "Bowtie.App\$dir") -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# ── Windows SDK tools ───────────────────────────────────────────────────────
$kits = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
$sdkBin = Get-ChildItem $kits -Directory -Filter "10.*" |
    Where-Object { Test-Path (Join-Path $_.FullName "x64\makeappx.exe") } |
    Sort-Object { [version]$_.Name } -Descending | Select-Object -First 1
if (-not $sdkBin) { throw "Windows SDK (makeappx.exe) not found under $kits" }
$makeappx = Join-Path $sdkBin.FullName "x64\makeappx.exe"
$signtool = Join-Path $sdkBin.FullName "x64\signtool.exe"
Write-Host "Windows SDK tools: $($sdkBin.FullName)"

# ── Signing certificate ─────────────────────────────────────────────────────
$certDir = Join-Path $out "cert"
New-Item -ItemType Directory -Force $certDir | Out-Null
if (-not $PfxPath -and $env:WINDOWS_PFX_B64) {
    $PfxPath = Join-Path $certDir "signing.pfx"
    [IO.File]::WriteAllBytes($PfxPath, [Convert]::FromBase64String($env:WINDOWS_PFX_B64))
    $PfxPassword = $env:WINDOWS_PFX_PASSWORD
}
if ($PfxPath) {
    $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($PfxPath, $PfxPassword)
    Write-Host "Signing with the provided certificate: $($cert.Subject)"
} else {
    $PfxPassword = [Guid]::NewGuid().ToString("N")
    $cert = New-SelfSignedCertificate -Type Custom -Subject "CN=Bowtie" `
        -KeyUsage DigitalSignature -FriendlyName "Bowtie sideload signing" `
        -CertStoreLocation "Cert:\CurrentUser\My" -NotAfter (Get-Date).AddYears(3) `
        -TextExtension @("2.5.29.37={text}1.3.6.1.5.5.7.3.3", "2.5.29.19={text}")
    $PfxPath = Join-Path $certDir "signing.pfx"
    Export-PfxCertificate -Cert $cert -FilePath $PfxPath `
        -Password (ConvertTo-SecureString $PfxPassword -AsPlainText -Force) | Out-Null
    Write-Host "Signing with a new self-signed certificate: $($cert.Subject) ($($cert.Thumbprint))"
}
$publisher = $cert.Subject
$cerBytes = $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Cert)
[IO.File]::WriteAllBytes((Join-Path $dist "bowtie-windows-$Label.cer"), $cerBytes)
[IO.File]::WriteAllBytes((Join-Path $dist "bowtie-windows.cer"), $cerBytes)

function Sign([string]$file) {
    # Not via Invoke-Checked: that echoes arguments, and this one carries the password.
    Write-Host ">> signtool sign $file"
    & $signtool sign /fd SHA256 /f $PfxPath /p $PfxPassword $file
    if ($LASTEXITCODE -ne 0) { throw "signtool failed for $file with exit code $LASTEXITCODE" }
}

# ── Stamp the manifest (restored afterwards) ────────────────────────────────
$originalManifest = Get-Content $manifest -Raw
try {
    [xml]$xml = $originalManifest
    $xml.Package.Identity.Version = $packageVersion
    $xml.Package.Identity.Publisher = $publisher
    $xml.Save($manifest)

    # ── MSIX per architecture, then one bundle ──────────────────────────────
    $bundleInput = Join-Path $out "bundle-input"
    New-Item -ItemType Directory -Force $bundleInput | Out-Null
    foreach ($platform in $Platforms) {
        Reset-AppBuild
        $packageDir = Join-Path $out "msix\$platform\"
        Invoke-Checked "msbuild" @(
            $project, "/restore", "/m", "/v:minimal", "/nologo",
            "/p:Configuration=Release", "/p:Platform=$platform",
            "/p:Version=$Version",
            "/p:GenerateAppxPackageOnBuild=true",
            "/p:AppxPackageSigningEnabled=false",
            "/p:AppxBundle=Never",
            "/p:UapAppxPackageBuildMode=SideloadOnly",
            "/p:AppxPackageDir=$packageDir")
        $msix = Get-ChildItem $packageDir -Recurse -Filter "*.msix" | Select-Object -First 1
        if (-not $msix) { throw "No .msix produced for $platform under $packageDir" }
        $target = Join-Path $bundleInput "Bowtie_$($Version)_$platform.msix"
        Copy-Item $msix.FullName $target
        Sign $target
    }

    $bundle = Join-Path $dist "bowtie-windows-$Label.msixbundle"
    Invoke-Checked $makeappx @("bundle", "/d", $bundleInput, "/p", $bundle, "/bv", $packageVersion, "/o")
    Sign $bundle
    Copy-Item $bundle (Join-Path $dist "bowtie-windows.msixbundle")
} finally {
    Set-Content -Path $manifest -Value $originalManifest -NoNewline
}

# ── Unpackaged, self-contained zips ─────────────────────────────────────────
foreach ($platform in $Platforms) {
    Reset-AppBuild
    $arch = $platform.ToLowerInvariant()
    $publishRoot = Join-Path $out "zip\$arch\Bowtie"
    $publishDir = "$publishRoot\"
    Invoke-Checked "msbuild" @(
        $project, "/restore", "/t:Publish", "/m", "/v:minimal", "/nologo",
        "/p:Configuration=Release", "/p:Platform=$platform",
        "/p:Version=$Version",
        "/p:WindowsPackageType=None",
        "/p:PublishDir=$publishDir")
    if (-not (Test-Path (Join-Path $publishDir "Bowtie.exe"))) { throw "Bowtie.exe missing from $publishDir" }
    $zip = Join-Path $dist "bowtie-windows-$Label-$arch.zip"
    # The zip holds one "Bowtie" folder.
    Compress-Archive -Path $publishRoot -DestinationPath $zip -Force
    Copy-Item $zip (Join-Path $dist "bowtie-windows-$arch.zip")
}

Write-Host "Packages:"
Get-ChildItem $dist | ForEach-Object { Write-Host ("  {0} ({1:N1} MB)" -f $_.Name, ($_.Length / 1MB)) }
