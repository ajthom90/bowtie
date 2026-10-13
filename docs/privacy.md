# Bowtie privacy policy

_Last updated: October 13, 2026_

Bowtie is open-source software you run yourself. The Bowtie apps for
Windows, Xbox, iPhone, iPad, Apple TV, Mac, Android, Android TV, Fire TV and
Roku, and the web app, connect only to the Bowtie server you choose. The
Bowtie project has no servers of its own, and nothing you do in the apps is
sent to the project or its developers.

## What the apps collect

Nothing is collected by the Bowtie project. The apps contain no analytics,
advertising, tracking or crash-reporting services.

## What the apps send, and where

The apps talk to one place: the Bowtie server whose address you enter
(usually a computer in your home). They send it your username and password
when you sign in, and then what you ask for: the channel list, the TV guide,
live TV and recordings, favorites, where you stopped watching, and
recordings you schedule. That server is run by you or by whoever invited
you to use it, and that person can see this information.

Channel logos and artwork load from the addresses your server provides,
which may be on another website (for example, a guide data service your
server uses).

When you set up a TV app from your phone (the QR code on the TV's Connect
screen), your phone sends the server address to the TV over your local
Wi-Fi network, and you approve the sign-in on your server's own page.
Nothing goes through the Bowtie project for that step.

## What the apps store on your device

- The address of your Bowtie server.
- Sign-in tokens (not your password), kept in the device's secure storage:
  Windows Credential Locker, Apple Keychain, Android encrypted storage, or
  the Roku device registry. The web app keeps them in your browser's local
  storage.
- Your app settings, such as video quality and whether to skip ads
  automatically.

Signing out removes the sign-in tokens. Uninstalling an app removes
everything it stored.

## Your Bowtie server

The Bowtie server runs on hardware you control. It stores user accounts,
favorites, watch progress and recordings on that machine. To fetch TV guide
data and send notifications it may contact services you configure, such as
SiliconDust (the HDHomeRun maker), Schedules Direct, an XMLTV source, or an
ntfy notification server. Those services have their own privacy policies.
The Bowtie project receives none of this data.

## Children

The apps are not directed at children and collect no data about anyone.
Parental controls on your server can limit which channels a user can watch.

## Changes

Changes to this policy are published in this file, with the date above
updated. The full history is in the project's Git repository.

## Contact

Questions: open an issue at <https://github.com/ajthom90/bowtie/issues>.
