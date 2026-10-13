<#
.SYNOPSIS
  Builds Bowtie for Xbox (the UWP app in windows/Bowtie.Xbox): a signed,
  sideloadable .msixbundle plus everything needed to install it, into
  windows/out-xbox/dist.

.DESCRIPTION
  Run from a "Developer PowerShell for VS 2022" (msbuild.exe on PATH) with
  the "Universal Windows Platform development" workload and the Windows 11
  SDK (10.0.26100.0). CI runs this same script (.github/workflows/xbox.yml).

  Release builds compile with .NET Native, which is what runs on Xbox.

  Signing: with -PfxPath/-PfxPassword (or the WINDOWS_PFX_B64 and
  WINDOWS_PFX_PASSWORD environment variables) the package is signed with
  that certificate; otherwise a fresh self-signed "CN=Bowtie" certificate is
  made for this build. The manifest's Publisher is set to the certificate's
  subject, which packaging requires.

  dist/ gets:
    bowtie-xbox-<label>.msixbundle   the app (x64, plus ARM64 when asked)
    bowtie-xbox-<label>.cer          the signing certificate, to trust first
    Dependencies/<arch>/*.appx       .NET Native runtime + VCLibs (Xbox Device
                                     Portal asks for these alongside the bundle)

  Microsoft Store: when the Store identity is known (parameters, or the
  WINDOWS_STORE_* environment variables shared with the desktop app), a
  second, unsigned pass builds an x64 .appxupload for Partner Center into
  windows/out-xbox/store. That pass targets Windows.Xbox only, so PCs keep
  getting the desktop app from the same Store product.

  Store versions: the desktop app and this one share a Store identity, and
  two packages may not share name, version and architecture (both are
  x64). So the Xbox upload adds StorePatchOffset (1000) to the third part:
  0.26.0 ships as 0.26.1000.0. XBOX_STORE_IDENTITY_NAME names a separate
  Store product instead; then there's no clash and no offset.

.PARAMETER Version
  Three-part version (0.11.0). The package version becomes 0.11.0.0.

.PARAMETER Label
  Text used in file names (defaults to the version).

.PARAMETER Platforms
  x64 (Xbox and most PCs) by default; add ARM64 for ARM PCs.
#>
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$Label = $Version,
    [string]$PfxPath = "",
    [string]$PfxPassword = "",
    [string[]]$Platforms = @("x64"),
    [string]$StoreIdentityName = $(if ($env:XBOX_STORE_IDENTITY_NAME) { $env:XBOX_STORE_IDENTITY_NAME } else { $env:WINDOWS_STORE_IDENTITY_NAME }),
    [string]$StorePublisher = $env:WINDOWS_STORE_PUBLISHER,
    [string]$StorePublisherDisplayName = $env:WINDOWS_STORE_PUBLISHER_DISPLAY_NAME,
    [int]$StorePatchOffset = $(if ($env:XBOX_STORE_IDENTITY_NAME) { 0 } else { 1000 })
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$windows = Split-Path -Parent $PSScriptRoot
$project = Join-Path $windows "Bowtie.Xbox\Bowtie.Xbox.csproj"
$manifest = Join-Path $windows "Bowtie.Xbox\Package.appxmanifest"
$out = Join-Path $windows "out-xbox"
$dist = Join-Path $out "dist"
Remove-Item $out -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $dist | Out-Null

if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "Version must look like 1.2.3, got '$Version'" }
$packageVersion = "$Version.0"

$buildStore = -not ([string]::IsNullOrWhiteSpace($StoreIdentityName) -or
    [string]::IsNullOrWhiteSpace($StorePublisher) -or
    [string]::IsNullOrWhiteSpace($StorePublisherDisplayName))
if ($buildStore) {
    if (-not $StorePublisher.StartsWith('CN=', [StringComparison]::Ordinal)) {
        throw "StorePublisher must start with 'CN='; copy Package/Identity/Publisher from Partner Center."
    }
    $parts = $Version.Split('.')
    $storePatch = [int]$parts[2] + $StorePatchOffset
    if ($storePatch -gt 65535) { throw "Store version part $storePatch is over 65535" }
    $storeVersion = "$($parts[0]).$($parts[1]).$storePatch.0"
} else {
    Write-Host "Skipping Store upload: set the three WINDOWS_STORE_* identity values (or Store parameters) to enable it."
}

function Invoke-Checked([string]$exe, [string[]]$arguments) {
    Write-Host ">> $exe $($arguments -join ' ')"
    & $exe @arguments
    if ($LASTEXITCODE -ne 0) { throw "$exe failed with exit code $LASTEXITCODE" }
}

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
[IO.File]::WriteAllBytes((Join-Path $dist "bowtie-xbox-$Label.cer"), $cerBytes)
[IO.File]::WriteAllBytes((Join-Path $dist "bowtie-xbox.cer"), $cerBytes)

# ── Stamp the manifest (restored afterwards) ────────────────────────────────
$originalManifest = Get-Content $manifest -Raw
try {
    [xml]$xml = $originalManifest
    $xml.Package.Identity.Version = $packageVersion
    $xml.Package.Identity.Publisher = $publisher
    $xml.Save($manifest)

    # ── Build, package and sign (msbuild signs bundle and packages) ─────────
    $packageDir = Join-Path $out "pkg\"
    $platformList = $Platforms -join "|"
    # Not via Invoke-Checked: these arguments carry the certificate password.
    $arguments = @(
        $project, "/restore", "/m", "/v:minimal", "/nologo",
        "/p:Configuration=Release", "/p:Platform=$($Platforms[0])",
        "/p:AppxBundle=Always", "/p:AppxBundlePlatforms=$platformList",
        "/p:UapAppxPackageBuildMode=SideloadOnly",
        "/p:AppxPackageDir=$packageDir",
        "/p:AppxPackageSigningEnabled=true",
        "/p:PackageCertificateKeyFile=$PfxPath",
        "/p:PackageCertificatePassword=$PfxPassword",
        "/p:PackageCertificateThumbprint=",
        "/p:GenerateTestArtifacts=true")
    Write-Host ">> msbuild $project (Release, $platformList, bundle, signed)"
    & msbuild @arguments
    if ($LASTEXITCODE -ne 0) { throw "msbuild failed with exit code $LASTEXITCODE" }

    if ($buildStore) {
        # Unsigned Store upload (bundle + symbols) for Xbox only. Start clean so
        # nothing from the sideload identity is reused.
        foreach ($dir in @("bin", "obj")) {
            Remove-Item (Join-Path $windows "Bowtie.Xbox\$dir") -Recurse -Force -ErrorAction SilentlyContinue
        }
        [xml]$xml = $originalManifest
        $xml.Package.Identity.Name = $StoreIdentityName
        $xml.Package.Identity.Publisher = $StorePublisher
        $xml.Package.Identity.Version = $storeVersion
        $xml.Package.Properties.PublisherDisplayName = $StorePublisherDisplayName
        $family = $xml.Package.Dependencies.TargetDeviceFamily
        if (@($family).Count -ne 1) { throw "Expected one TargetDeviceFamily in $manifest" }
        $family.Name = "Windows.Xbox"
        $xml.Save($manifest)

        $storeDir = Join-Path $out "store-pkg\"
        Invoke-Checked "msbuild" @(
            $project, "/restore", "/m", "/v:minimal", "/nologo",
            "/p:Configuration=Release", "/p:Platform=x64",
            "/p:AppxBundle=Always", "/p:AppxBundlePlatforms=x64",
            "/p:UapAppxPackageBuildMode=StoreUpload",
            "/p:AppxSymbolPackageEnabled=true",
            "/p:AppxPackageDir=$storeDir",
            "/p:AppxPackageSigningEnabled=false")
        $upload = Get-ChildItem $storeDir -Recurse -Include "*.appxupload", "*.msixupload" | Select-Object -First 1
        if (-not $upload) { throw "No Store upload produced under $storeDir" }
        $storeOut = Join-Path $out "store"
        New-Item -ItemType Directory -Force $storeOut | Out-Null
        Copy-Item $upload.FullName (Join-Path $storeOut "bowtie-xbox-$Label$($upload.Extension)")
        Write-Host "Store upload: bowtie-xbox-$Label$($upload.Extension) ($StoreIdentityName $storeVersion, Windows.Xbox)"
    }
} finally {
    Set-Content -Path $manifest -Value $originalManifest -NoNewline
}

# ── Collect ─────────────────────────────────────────────────────────────────
$bundle = Get-ChildItem $packageDir -Recurse -Include "*.msixbundle", "*.appxbundle" | Select-Object -First 1
if (-not $bundle) { throw "No bundle produced under $packageDir" }
$testDir = $bundle.Directory
Write-Host "Sideload folder: $($testDir.FullName)"
Get-ChildItem $testDir -Recurse | ForEach-Object { Write-Host "  $($_.FullName.Substring($testDir.FullName.Length))" }

$ext = $bundle.Extension
Copy-Item $bundle.FullName (Join-Path $dist "bowtie-xbox-$Label$ext")
Copy-Item $bundle.FullName (Join-Path $dist "bowtie-xbox$ext")
# Only the dependency packages for the architectures in the bundle.
foreach ($platform in $Platforms) {
    $arch = $platform.ToLowerInvariant()
    $deps = Join-Path $testDir.FullName "Dependencies\$arch"
    if (-not (Test-Path $deps)) { throw "No dependency packages for $arch under $($testDir.FullName)" }
    $target = Join-Path $dist "Dependencies\$arch"
    New-Item -ItemType Directory -Force $target | Out-Null
    Copy-Item (Join-Path $deps "*.appx") $target
}

Write-Host "Packages:"
Get-ChildItem $dist -Recurse -File | ForEach-Object {
    Write-Host ("  {0} ({1:N1} MB)" -f $_.FullName.Substring($dist.Length + 1), ($_.Length / 1MB))
}
