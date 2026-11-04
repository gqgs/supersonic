# Display-matched artwork

Supersonic requests artwork sized for the display instead of using the same
300-pixel thumbnail everywhere. Track and queue rows use small images; grid and
grouped-release cards, album and artist headers, and Now Playing request enough
pixels for their displayed size, including HiDPI/Retina backing resolution.

Requests round up to stable buckets: 128, 256, 300, 400, 512, 600, 1024, 1200,
and 1400 pixels. Display-aware requests are bounded at 1400 pixels. Now Playing
only upgrades the current cover while resizing, so shrinking the window or
moving within a bucket does not repeatedly download artwork. Source quality and
the server's resizing support still determine the available detail.

Disk variants above 300 pixels use JPEG quality 95 to preserve more detail
after restarting the app. Small and legacy 300-pixel thumbnails keep their
existing JPEG quality. The shared 50 MiB default disk-cache budget is unchanged.

Memory cache entries, disk files, and in-flight downloads distinguish request
sizes, so a cached row thumbnail cannot replace a larger cover. The decoded
thumbnail cache also has a conservative 64 MiB pixel-memory budget, in addition
to its existing entry limit and disk-cache policy. This budget uses an
eight-bytes-per-pixel estimate; it is not a limit on total application memory,
visible images, or downloads in progress.

Full-image popups retain the existing original-size API, allowing the server
to choose its original/default response. Other legacy thumbnail callers
continue using 300 pixels.
