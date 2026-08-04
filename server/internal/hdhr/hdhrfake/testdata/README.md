# MPEG-TS test fixture

`fixture.ts` is a ~2s 480i MPEG-2 / AC-3 transport stream used by the fake HDHomeRun
to emulate live channel streaming in tests.

Generated once with:

```bash
ffmpeg -f lavfi -i "testsrc2=duration=2:size=720x480:rate=29.97" \
  -f lavfi -i "sine=frequency=440:duration=2" \
  -c:v mpeg2video -b:v 2M -flags +ilme+ildct \
  -c:a ac3 -b:a 192k \
  -f mpegts fixture.ts
```

Do not regenerate in CI; the binary is committed so tests run without FFmpeg.
