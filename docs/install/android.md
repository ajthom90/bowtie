# Installing the Android and Fire TV apps

Bowtie's Android apps are installed directly ("sideloaded") from GitHub
Releases, not from Google Play. Your Bowtie server hands out the right file:

| Device | Link to open |
|--------|--------------|
| Android phone or tablet | `https://<your-bowtie-server>/android` |
| Fire TV, Android TV, Google TV | `https://<your-bowtie-server>/tv` |

Each link downloads the app built for the version your server runs, so the
app and server always match. (A development build of the server links to the
latest release instead.) The files are also on the
[Releases page](https://github.com/ajthom90/bowtie/releases/latest):
`bowtie-android.apk` and `bowtie-tv.apk`.

## Android phone or tablet

1. Open `https://<your-bowtie-server>/android` in Chrome on the phone.
2. Open the downloaded `bowtie-android.apk` (or `bowtie-<version>.apk`).
3. If Android says your browser isn't allowed to install apps, tap
   **Settings**, turn on **Allow from this source**, and go back.
4. Tap **Install**, then open Bowtie and sign in to your server.

## Fire TV

Fire TV has no web browser that installs apps, so use the free **Downloader**
app (by AFTVnews).

1. Search for **Downloader** on the Fire TV home screen and install it.
2. Allow it to install apps: **Settings → My Fire TV → Developer options →
   Install unknown apps → Downloader → On**.
   If **Developer options** is missing, open **Settings → My Fire TV → About**,
   select your Fire TV's name seven times, then go back.
3. Open Downloader, select the address box, and type
   `https://<your-bowtie-server>/tv`, then **Go**.
4. When the download finishes, choose **Install**, then **Open**. Downloader
   offers to delete the file afterwards; it's safe to say yes.

Android TV and Google TV work the same way: install Downloader from Google
Play, then allow it under **Settings → Apps → Security & restrictions →
Unknown sources**.

## Updating

After your server is updated, open the same link again and install over the
existing app; your sign-in is kept. Each release has a higher version number,
so Android accepts it as an update.

## If the install fails

- **"App not installed" / "conflicts with an existing package":** the app on
  the device was signed differently (a build from Android Studio, or a copy
  from an app store). Uninstall Bowtie from the device and install again.
- **Fire TV Appstore copy:** Amazon signs Appstore apps with its own key, so
  the Appstore copy and this download can't update each other. Keep one or the
  other; switching means uninstalling first.
