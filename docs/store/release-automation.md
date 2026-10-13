# Store release automation (plan)

Goal: pushing a `vX.Y.Z` tag ships every app to its store with as little
manual work as possible. None of this is built yet. Each section lists what
must exist first (secrets, one-time setup) and what the workflow would do.

Rules for all of it:

- Store steps run only on `v*` tags, never on pushes to `main`.
- Store jobs run in a GitHub environment named `store`. Add a required
  reviewer to it if you want one click to approve each store push; leave it
  off for fully automatic releases.
- Release notes ("What's New") come from the tag's section of
  `CHANGELOG.md`, so a release needs nothing beyond the PR that adds the
  version heading.
- Listing text and screenshots stay in `docs/store/` as the source of truth.
- Credentials live only in GitHub secrets (or the store consoles), never in
  the repo. App Review's sign-in details stay in App Store Connect.

Suggested order: Microsoft Store first (smallest), then Apple, then Roku.

## Apple: App Store (iOS, iPadOS, tvOS, macOS)

Today: Xcode Cloud archives all three platforms on a tag and uploads them
to App Store Connect. `ios/ci_scripts/ci_post_clone.sh` stamps the version
from the tag. Submitting for review is still manual.

One-time setup:

1. Ship the first version by hand. That covers the app record (bundle ID
   `app.bowtie`, universal purchase), App Privacy answers, age rating,
   category, and the App Review notes and review account
   (`docs/store/apple-listing.md`).
2. Create an App Store Connect API key with the App Manager role (Users and
   Access → Integrations → App Store Connect API).
3. Add the secrets `ASC_KEY_ID`, `ASC_ISSUER_ID` and `ASC_KEY_P8` (the .p8
   contents).
4. Add `ITSAppUsesNonExemptEncryption` = `NO` to the iOS, tvOS and macOS
   Info.plists. Bowtie uses only HTTPS, which is exempt, and without the key
   every build waits on the export compliance question.

Workflow job (`release.yml`, on the tag):

1. For each platform (`IOS`, `TV_OS`, `MAC_OS`), poll the App Store Connect
   API until the build with this version has finished processing.
2. Create the App Store version (or reuse an editable one) and set
   `whatsNew` from the changelog.
3. Attach the build. Set the release type to "after approval", with an
   optional phased release on iOS.
4. Submit for review (`reviewSubmissions`).
5. Optionally, upload changed screenshots and listing text from
   `docs/store/`.

Tooling: fastlane `deliver` (Ruby), or a small script that calls the API
with a JWT signed by the .p8 key.

Still manual: answering App Review if they reply.

Later (parked): notarize the Mac app with a Developer ID and attach it to
the GitHub release for direct download. That needs a Developer ID
Application certificate (.p12 and password), the same API key, and a
Developer ID provisioning profile with Group Activities.

## Microsoft Store: Windows and Xbox

Both packages ship in the same Store product (`AndrewJ.Thom.Bowtie`).
`windows.yml` builds `bowtie-windows-store` (`.msixupload`, Desktop) and
`xbox.yml` builds `bowtie-xbox-store` (Xbox). The Xbox package's version
has 1000 added to its third part, so two packages never share a name and
version.

One-time setup:

1. The first submission must pass certification. It was submitted by hand
   on 2026-10-13 (0.25.1).
2. Create a Microsoft Entra app and link it in Partner Center (Account
   settings → User management → Microsoft Entra applications) with the
   Manager role.
3. Add the secrets `MSSTORE_TENANT_ID`, `MSSTORE_CLIENT_ID`,
   `MSSTORE_CLIENT_SECRET` and `MSSTORE_SELLER_ID`. Add the product's
   Store ID as the variable `MSSTORE_PRODUCT_ID`.
4. Before the Xbox package can go in, enable the Xbox device family for the
   product in Partner Center and complete its Xbox requirements.

Workflow job (`windows-latest`, on the tag, after both builds):

```
- uses: microsoft/setup-msstore-cli@v1
- run: msstore reconfigure --tenantId $env:MSSTORE_TENANT_ID --sellerId $env:MSSTORE_SELLER_ID --clientId $env:MSSTORE_CLIENT_ID --clientSecret $env:MSSTORE_CLIENT_SECRET
- run: msstore publish <the .msixupload from the bowtie-windows-store artifact> -id $env:MSSTORE_PRODUCT_ID
```

`msstore publish` makes a new submission that keeps the current listing,
replaces the package and submits it for certification. To check first:
whether one submission can carry both the Windows and Xbox packages through
`msstore`. If it can't, use the Microsoft Store Submission API directly
(or the StoreBroker PowerShell module) to add both packages to one
submission.

Still manual: nothing, unless certification fails.

## Roku Channel Store

Roku has no publishing API. A signed package can only be made on a Roku in
developer mode.

One-time setup:

1. A Roku developer account.
2. Run `genkey` once on a developer-mode Roku. Keep the key package and its
   password as secrets (`ROKU_SIGNING_PKG`, `ROKU_SIGNING_PASSWORD`).
3. A self-hosted GitHub runner on the home network that can reach that
   Roku, plus its developer password as `ROKU_DEV_PASSWORD`.

Workflow job (self-hosted, on the tag):

1. Build and stamp the channel zip (`roku/scripts/stamp-version.sh`, as
   now).
2. Over HTTP with digest auth to the Roku: rekey with the signing package,
   sideload the zip, and package it (`/plugin_inspect`, `/plugin_install`,
   `/plugin_package`).
3. Download the signed `.pkg` and attach it to the GitHub release.

Still manual: upload the `.pkg` in the Roku developer dashboard and press
Publish (about two minutes). Scripting the dashboard with a browser would
break easily and may go against Roku's terms, so it's not planned.

## Android: Google Play (undecided)

If Google Play is chosen: the Google Play Developer API through fastlane
`supply` or `r0adkll/upload-google-play`, with a service account JSON key
as a secret. Upload the signed AAB to a track, with "What's New" from the
changelog.
