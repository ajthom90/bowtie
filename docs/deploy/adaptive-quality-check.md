# Checking adaptive quality on TrueNAS (Intel QSV)

Adaptive quality makes one transcode per channel produce several qualities
(1080/720/480/360, never above the broadcast) that every viewer shares. It
uses roughly 1.7× the GPU of one stream, and the Intel path can only be
checked on the TrueNAS box. It ships **off**. Five minutes:

1. **Admin → Settings → Streaming**: tick **Adaptive quality** and Save.
2. On a phone, play **9.1**. It should start within a few seconds.
3. In a TrueNAS shell, watch the GPU for 30 seconds:
   ```sh
   sudo intel_gpu_top -l
   ```
   *Render/3D* and *Video* should stay under ~80 %.
4. Start two more channels (another phone, the web app). Playback on all
   three should stay smooth; re-check `intel_gpu_top`.
5. Look for trouble in the container log:
   ```sh
   docker logs bowtie 2>&1 | grep -E 'full layout failed|ffmpeg exited' | tail
   ```
   Expect nothing. `full layout failed` means a channel fell back to one
   quality (still plays).
6. Force a restart and confirm playback recovers within ~10 s:
   ```sh
   docker exec bowtie pkill -f ffmpeg
   ```

If any step fails, untick **Adaptive quality** (new sessions go back to one
stream per quality) and send me the log lines from step 5.

Captions, Spanish audio and the 5.1 option work with the switch on or off.
To turn those off entirely, set `BOWTIE_MULTITRACK=off` and restart.
