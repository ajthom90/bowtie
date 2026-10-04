# Bowtie for Windows

A native Windows app for watching your Bowtie server: live TV with
favorites, Recent, guide filters, pause/rewind, Go Live and a sleep timer,
and DVR recordings with resume and Skip ad.
Windows 10 version 1809 or later and Windows 11, on **x64** (Intel/AMD) and
**ARM64** (Snapdragon, Surface Pro X, Copilot+ PCs).

| Folder | What it is |
|--------|------------|
| `Bowtie.Core/` | Plain .NET 8 class library: the API client (`docs/api/openapi.yaml`), models, guide/recording/live-edge logic and the view models. No Windows APIs, so it builds and tests on macOS and Linux too. |
| `Bowtie.Core.Tests/` | xUnit tests for Core (fake HTTP server; no real Bowtie needed). |
| `Bowtie.App/` | The WinUI 3 app (Windows App SDK 1.6, single-project MSIX). Pages, playback, the Credential Locker token store. |
| `scripts/package.ps1` | Builds the signed `.msixbundle` and the unpackaged zips (CI runs it too). |
| `scripts/make-icons.sh` | Regenerates `Bowtie.App/Assets/*` from the shared 1024 px app icon. |

## Install

Each [GitHub Release](https://github.com/ajthom90/bowtie/releases) has:

- `bowtie-windows-<version>.msixbundle` — the installer, one file for x64 and ARM64
  (Windows picks the right one). Also as `bowtie-windows.msixbundle`.
- `bowtie-windows-<version>.cer` — the certificate the bundle is signed with.
- `bowtie-windows-<version>-x64.zip` / `-arm64.zip` — the app without installing.

### Install the app (MSIX)

The bundle is signed with a self-signed certificate, not one from a
certificate authority, so Windows has to be told to trust it once. **Each
release is signed with a new certificate**, so repeat step 1 when you update
(unless the maintainer has set up a permanent signing certificate; see
[Signing](#signing)).

1. Trust the certificate. Download `bowtie-windows-<version>.cer`, double-click
   it, choose **Install Certificate…** → **Local Machine** → **Place all
   certificates in the following store** → **Browse…** → **Trusted People** →
   **OK** → **Next** → **Finish**. (Needs an administrator.) Or, in an
   administrator PowerShell:

   ```powershell
   Import-Certificate -FilePath .\bowtie-windows-0.11.0.cer -CertStoreLocation Cert:\LocalMachine\TrustedPeople
   ```

2. Double-click `bowtie-windows-<version>.msixbundle` and choose **Install**
   (or `Add-AppxPackage .\bowtie-windows-0.11.0.msixbundle`).

3. Open **Bowtie** from Start, enter your server's address (the one you use
   in a browser, like `192.168.1.20:8400`) and sign in.

If Windows says sideloading is turned off (older Windows 10 builds), turn on
**Settings → Privacy & security → For developers → Developer Mode** (Windows 11)
or **Settings → Update & Security → For developers → Sideload apps / Developer
mode** (Windows 10). Windows 10 2004 and later, and Windows 11, allow
sideloading signed apps by default.

The package is self-contained (.NET and the Windows App SDK runtime are
inside), so there's nothing else to install. If Windows reports a missing
`Microsoft.VCLibs.140.00.UWPDesktop` framework, install it from
<https://aka.ms/Microsoft.VCLibs.x64.14.00.Desktop.appx> (or the `arm64`
link on that page) and try again.

### Run without installing (zip)

Unzip `bowtie-windows-<version>-x64.zip` (or `-arm64.zip`) anywhere and run
`Bowtie\Bowtie.exe`. The server and sign-in are saved in the Windows
Credential Locker for your Windows account. If it doesn't
start on a fresh PC, install the Microsoft Visual C++ Redistributable
(<https://aka.ms/vs/17/release/vc_redist.x64.exe>, or `vc_redist.arm64.exe`).

### Removing it

Uninstall **Bowtie** from Start or Settings → Apps. To forget the saved
server and sign-in, open **Credential Manager → Windows Credentials** and
remove the `Bowtie` entries (or use **Sign out** / **Use a different server**
in the app first).

## Using it

- **Live TV**: favorites first, then every channel; **Recent** shows what you
  watched lately. Click ☆ to star a channel. F5 refreshes. **All · Sports ·
  Movies · News · Kids · New** filter by what's on in the next four hours:
  channels with nothing matching are hidden, lines outside the filter dim,
  and a later match shows as "Later: …". The choice is remembered on this PC.
- **Player**: the system controls play/pause, show the seek bar and volume.
  - ← / → skip 30 seconds; the pause/rewind buffer is set by your server
    admin (15 minutes by default). **Go Live** (or End) jumps back to live.
  - Page Up / Page Down change channel (in the list's order).
  - **Quality** caps the stream (Auto, or Original/High/Medium/Low as your
    account allows); audio and captions menus appear when the broadcast has
    choices.
  - F11, double-click or the full-screen button toggle full screen; Esc leaves it.
  - **Sleep** stops playback after 15 minutes to 2 hours, or at the end of the
    live program. A minute before, "Still watching?" offers **Keep watching**;
    otherwise the player closes and the tuner is freed.
  - In recordings, **Skip ad** (or S) appears during a detected commercial
    break. Turn on **Skip ads automatically** in the account menu to skip each
    break once.
- **Recordings**: Upcoming / Recorded / Missed. Play asks whether to resume
  where you stopped; your position is saved every 15 seconds. Keep protects a
  recording from automatic cleanup; Delete removes it for everyone. A
  recording blocked by parental controls shows 🔒 and its rating and can't
  be played.

## Build

You need Windows 10/11 with **Visual Studio 2022** (17.10 or later) and the
**WinUI application development** workload (it brings the .NET 8 SDK, the
Windows SDK and the MSIX tooling).

- **Run from Visual Studio**: open `windows/Bowtie.sln`, pick `x64` (or
  `ARM64` on an ARM PC), choose the **Bowtie (Package)** launch profile and
  press F5. **Bowtie (Unpackaged)** runs it as a plain exe.
- **Command line** (Developer PowerShell for VS 2022):

  ```powershell
  cd windows
  dotnet test Bowtie.Core.Tests                                    # Core tests
  msbuild Bowtie.App\Bowtie.App.csproj /restore /p:Configuration=Release /p:Platform=x64
  msbuild Bowtie.App\Bowtie.App.csproj /restore /p:Configuration=Release /p:Platform=ARM64
  ```

  Use Visual Studio's `msbuild`, not `dotnet build`, for the app: the .NET
  CLI doesn't include the MSIX/PRI build tasks a WinUI app needs.

- **Packages** (what CI publishes): `.\scripts\package.ps1 -Version 0.11.0`
  writes the signed bundle, its `.cer` and both zips to `windows\out\dist`.
  Without a certificate it creates a self-signed `CN=Bowtie` one in your
  `CurrentUser\My` store for that run.

`Bowtie.Core` and its tests build anywhere the .NET 8 SDK runs:

```bash
cd windows && dotnet test Bowtie.Core.Tests
```

### ARM64 notes

- x64 runners build the ARM64 package by cross-compiling; nothing ARM-specific
  is needed to build. The bundle contains both architectures and Windows
  installs the native one, so ARM PCs don't run the x64 build under emulation.
- Test on real ARM64 hardware (or an ARM64 VM) before relying on it: video
  decoding goes through the PC's hardware decoders, which differ between
  Snapdragon and Intel/AMD.
- HEVC: the app only asks the server for HEVC when Windows has an HEVC
  decoder (the *HEVC Video Extensions* from the Microsoft Store). Without it
  the server sends H.264, which every PC plays.

## Signing

`scripts/package.ps1` signs every `.msix` and the bundle with one
certificate, and sets the manifest's `Publisher` to that certificate's
subject (MSIX requires them to match).

- **Default (no secrets)**: a new self-signed `CN=Bowtie` certificate per
  build. Fine for sideloading, but people re-trust the new `.cer` on each
  update.
- **A permanent certificate**: add repository secrets `WINDOWS_PFX_B64`
  (`[Convert]::ToBase64String([IO.File]::ReadAllBytes("bowtie.pfx"))`) and
  `WINDOWS_PFX_PASSWORD`. CI then signs every release with it, so users trust
  it once. A self-signed one is enough for that:

  ```powershell
  $cert = New-SelfSignedCertificate -Type Custom -Subject "CN=Bowtie" -KeyUsage DigitalSignature `
    -FriendlyName "Bowtie signing" -CertStoreLocation Cert:\CurrentUser\My -NotAfter (Get-Date).AddYears(5) `
    -TextExtension @("2.5.29.37={text}1.3.6.1.5.5.7.3.3", "2.5.29.19={text}")
  Export-PfxCertificate -Cert $cert -FilePath bowtie.pfx -Password (Read-Host -AsSecureString "PFX password")
  ```

  A certificate from a CA trusted by Windows (or Microsoft Store
  distribution) would remove the trust step entirely.

## CI

`.github/workflows/windows.yml` runs on changes under `windows/`: it builds
and tests Core, then runs `scripts/package.ps1` for x64 and ARM64 and uploads
the bundle, `.cer` and zips as the `bowtie-windows` artifact. On a `v*` tag,
`release.yml` calls the same workflow and attaches those files to the GitHub
Release after GoReleaser has created it.

## Icons

`Bowtie.App/Assets` is generated from the shared app icon
(`ios/App/iOS/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png`). After a
logo change, run `windows/scripts/make-icons.sh [new-icon-1024.png]`
(needs ImageMagick 7) and commit the results.
