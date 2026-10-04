/** Setup steps for an app that reads M3U + XMLTV links. */
export type FeedApp = {
  name: string
  /** One line under the name (platforms, caveats). */
  note: string
  steps: string[]
}

/** The two links are called "M3U link" and "guide link" in the steps. */
export const FEED_APPS: FeedApp[] = [
  {
    name: 'Kodi',
    note: 'Windows, Mac, Linux, Android, Fire TV and Xbox (install Kodi from the Microsoft Store).',
    steps: [
      'Settings → Add-ons → Install from repository → PVR clients → IPTV Simple Client → Install.',
      'Open the add-on’s Configure. Under General, set Location to “Remote path (Internet address)” and paste the M3U link as the M3U playlist URL.',
      'Under EPG, set Location to “Remote path (Internet address)” and paste the guide link as the XMLTV URL.',
      'Restart Kodi. Channels and the guide appear under TV.',
    ],
  },
  {
    name: 'VLC',
    note: 'Quick watching on a computer; VLC doesn’t show the guide.',
    steps: [
      'Media → Open Network Stream (on a Mac: File → Open Network).',
      'Paste the M3U link and press Play. Pick channels from the playlist.',
    ],
  },
  {
    name: 'TiviMate',
    note: 'Android TV, Fire TV and Google TV.',
    steps: [
      'Add playlist → Enter URL, paste the M3U link, then Next.',
      'When it asks for a TV guide, paste the guide link (or later: Settings → EPG → EPG sources → Add EPG source).',
    ],
  },
  {
    name: 'Plex',
    note: 'Plex can also use your HDHomeRun directly, without Bowtie.',
    steps: [
      'Plex can’t open an M3U link by itself. Add both links to a bridge such as Threadfin (or xTeVe).',
      'In Plex: Settings → Live TV & DVR → Set up Plex DVR, choose the Threadfin tuner, and use the guide link as the XMLTV guide.',
    ],
  },
  {
    name: 'Jellyfin',
    note: 'Also works for Emby.',
    steps: [
      'Dashboard → Live TV → Tuner Devices → +. Tuner type: M3U Tuner; File or URL: the M3U link.',
      'TV Guide Data Providers → + → XMLTV; File or URL: the guide link.',
      'Run “Refresh Guide” under Scheduled Tasks if the guide is empty.',
    ],
  },
]

/** The server can't say whether a link exists, so links show only after one is made here. */
export function feedButtons(linkShown: boolean): { create: string | null; rotate: boolean } {
  return linkShown ? { create: null, rotate: true } : { create: 'Create link', rotate: false }
}

export const FEED_COPY = {
  lead: 'Watch your channels in apps that can’t sign in to Bowtie: Kodi (including on Xbox), VLC, TiviMate, Plex and Jellyfin. They get a playlist link and a guide link.',
  warning:
    'Anyone with these links can watch as you (your limits and parental controls still apply). Keep them private.',
  existing:
    'Made a link before? It keeps working until you make a new one or turn it off. Bowtie can’t show it again, so make a new one if you need the links.',
  rotateConfirm: 'Make a new link? Apps using the old links will stop working until you give them the new ones.',
  offConfirm: 'Turn the links off? Apps using them will stop working.',
} as const
