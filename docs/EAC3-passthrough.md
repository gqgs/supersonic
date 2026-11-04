# E-AC-3 passthrough

PCM playback remains the default. To send compressed E-AC-3 to a compatible HDMI receiver or soundbar:

1. In Settings → Playback, select the HDMI audio device using the existing Audio device control.
2. Enable **E-AC-3 passthrough (restart required)**, then quit and restart Supersonic.
3. Play an original E-AC-3 stream. Server transcoding to another codec cannot preserve E-AC-3 passthrough.
4. Check Now Playing: **Source** identifies the track codec, while **Output** reports the format libmpv writes to the audio API. EAC3 passthrough confirms that API format, not receiver decoding or Atmos metadata. Verify Atmos on the receiver itself. The transport can report 192 kHz for a 48 kHz source.

The setting applies `audio-spdif=eac3` directly to libmpv. It does not load mpv.conf or enable other compressed codecs. The saved TOML setting is `LocalPlayback.EAC3Passthrough`.

Start with the receiver at a low volume and use its volume control for passthrough; the software slider does not attenuate a compressed bitstream. Supersonic's saved software volume remains available for PCM. While this option is enabled, EQ, visualization filters, ReplayGain and pause fading are bypassed for the session, including PCM tracks or PCM fallback. Their saved settings are retained and resume when passthrough is disabled and Supersonic restarts.

Other codecs still decode to PCM. Device selection and the existing unavailable-device fallback are unchanged. Passthrough success depends on the device and libmpv; automatic PCM fallback is not guaranteed. If playback fails, disable the option and restart. Supersonic does not silently enable extra codecs, choose another device, or claim Atmos based on an E-AC-3 source label.

Automated tests use a generated E-AC-3 fixture and libmpv's null output. They verify PCM defaults, E-AC-3 transport, other-codec PCM, option persistence, bypassed filters and manual recovery after an incompatible output. These tests cannot validate physical HDMI output or a soundbar.

Reference: [mpv audio options and output properties](https://mpv.io/manual/master/).
