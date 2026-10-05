# Bowtie for Xbox

A UWP app for watching live TV from your Bowtie server on an **Xbox** (One,
Series X|S). It also runs on Windows 10/11 PCs, which is the easiest way to
try a build before putting it on a console.

It is a classic UWP XAML app compiled with .NET Native (the toolchain Xbox
officially supports for UWP apps) and shares `Bowtie.Core` (API client, view
models) with the desktop app through Core's .NET Standard 2.0 build.

The app talks only to the Bowtie server you enter, for example
`192.168.1.20:8400` on your home network or `https://tv.example.com`.

## Get a build

Every push that touches the app builds it in GitHub Actions
(`.github/workflows/xbox.yml`). Open the run and download the **bowtie-xbox**
artifact. It contains:

| File | What it is |
| --- | --- |
| `bowtie-xbox-<version>.msixbundle` | The app (x64) |
| `bowtie-xbox-<version>.cer` | The certificate the app is signed with |
| `Dependencies/x64/*.appx` | .NET Native runtime and VC libraries the app needs |

(`bowtie-xbox.msixbundle` and `bowtie-xbox.cer` are the same files without
the version in the name.)

Unless the repository's signing secrets are set, each build is signed with a
new self-signed `CN=Bowtie` certificate, so trust the `.cer` that came with
the bundle you are installing.

## Install on a Windows PC

1. Trust the certificate (once per certificate). In an **administrator**
   PowerShell, in the unzipped artifact folder:

   ```powershell
   Import-Certificate -FilePath .\bowtie-xbox-<version>.cer -CertStoreLocation Cert:\LocalMachine\TrustedPeople
   ```

   (Or double-click the `.cer` → Install Certificate → Local Machine →
   "Place all certificates in the following store" → Trusted People.)

2. Install the app with its dependencies (a normal PowerShell is fine):

   ```powershell
   Add-AppxPackage -Path .\bowtie-xbox-<version>.msixbundle -DependencyPath (Get-ChildItem .\Dependencies\x64\*.appx).FullName
   ```

3. Start **Bowtie** from the Start menu. It opens full screen; Escape goes
   back, and an Xbox controller connected to the PC works as on the console.

It installs next to the desktop app: the package identity is `Bowtie.Xbox`
(the desktop app is `Bowtie.LiveTV`).

## Install on an Xbox (Developer Mode)

Retail Xbox consoles can't sideload apps. Developer Mode can, and you can
switch the console back to retail mode at any time (games and retail apps
only run in retail mode).

1. **Partner Center account.** Sign up for a Microsoft developer account at
   [Partner Center](https://partner.microsoft.com/dashboard) (an individual
   account has a one-time registration fee).
2. **Activate Developer Mode.** On the Xbox, install the **Xbox Dev Mode**
   app from the Store, open it and follow the steps: it shows a code that you
   enter in Partner Center (Xbox devices), then the console restarts into
   Developer Mode with **Dev Home**.
3. **Turn on remote access.** In Dev Home → Remote Access Settings, enable
   the **Xbox Device Portal** and set a username and password. Dev Home
   shows the console's address, like `https://192.168.1.50:11443`.
4. **Deploy.** On a PC on the same network, open that address in a browser
   (accept the console's own certificate warning) and sign in. Under
   **Home → My games & apps → Add**:
   - Package: `bowtie-xbox-<version>.msixbundle`
   - Certificate: `bowtie-xbox-<version>.cer`
   - Dependencies: both `.appx` files in `Dependencies/x64/`
     (tick "I want to specify dependencies" / "optional packages" to pick them)

   Then **Start** / **Next** to install.
5. **Run it.** Bowtie appears in Dev Home's list of apps and games; select
   it and press A. To update, deploy a newer bundle the same way.

The console must be able to reach your Bowtie server (same network, or a
reachable `https://` address).

## Using it

- **Connect:** enter the server address (A on the box opens the on-screen
  keyboard).
- **Sign in:** either scan the QR code (or open the link and type the code)
  on a phone that's already signed in to Bowtie and approve the TV, or enter
  a username and password. The session is remembered in the Windows
  Credential Locker; there's a **Change server** button on this screen.
- **Channels:** favorites first, then everything else, with what's on now and
  next. D-pad to move, **A** to watch, **Y** to add or remove a favorite.
  **Refresh** and **Sign out** are above the list.
- **Watching:** live video full screen. **B** goes back to the channels and
  frees the tuner. If every tuner is busy, the channel has no signal or
  parental controls block it, the player says so and offers **Try again**.
  Suspending the app (Home button, or the console sleeping) also stops the
  stream.

## Build it yourself

On Windows with Visual Studio 2022 and the **Universal Windows Platform
development** workload (plus the Windows 11 SDK 10.0.26100):

```powershell
# Quick compile (no .NET Native):
msbuild windows\Bowtie.Xbox\Bowtie.Xbox.csproj /restore /p:Configuration=Debug /p:Platform=x64
# Signed sideload package, as CI makes it (into windows\out-xbox\dist):
.\windows\scripts\package-xbox.ps1 -Version 0.0.1
```

Or open `windows\Bowtie.Xbox\Bowtie.Xbox.csproj` in Visual Studio and deploy
to the local machine or to a console in Developer Mode (Remote Machine). The
project isn't in `windows\Bowtie.sln`, so `dotnet build` of the solution never
needs the UWP tools.

Release builds use .NET Native and take several minutes. Bowtie.Core's JSON is
source-generated (no reflection), which keeps it working under .NET Native.

## What's in this first slice, and what's next

In this slice: connect, quick sign-in (code + QR) and password sign-in,
favorites-first channel list with now/next, live playback with session
heartbeats and clean teardown, and an installable signed package from CI.

Next:

- A guide grid, and the guide category chips the desktop app has.
- Recordings (DVR) and resume ("Continue watching").
- Player extras: channel up/down zapping, quality picker, audio tracks and
  captions, the sleep timer.
- Channel logos in the list.
- An ARM64 package for ARM PCs, and attaching the
  bundle to GitHub Releases.
- Testing on real Xbox hardware: focus order, safe area on different TVs,
  HEVC/AC-3 detection on the console.
