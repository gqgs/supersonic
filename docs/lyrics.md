# Lyrics

The lyrics pane uses regular-weight text, padding and a clearly marked active
line. “Synced lyrics” follows real source timestamps; “Unsynced lyrics” remains
freely scrollable and does not invent timings. Seek backwards or forwards to
reposition the active line; delayed playback updates catch up immediately.

When the existing LRCLIB option is enabled, plain server lyrics can be upgraded
to a timed LRCLIB result. This lookup sends the same title, artist, album and
duration metadata as the existing fallback. Disable LRCLIB in settings to use
only server lyrics. If lookup fails or yields only plain text, the server's text
is retained. Timing availability still depends on the track and source.

Rebuild/reinstall Supersonic and fully quit/reopen it to test these UI changes.
The bridge may need its own update for Apple timestamp parsing; a new client
cannot recover missing source timestamps by itself.
