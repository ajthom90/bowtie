package api

import (
	"os"
	"strings"
	"testing"
)

// FFmpeg 5.1 (trac #11208) mangles a WebVTT playlist continued with
// append_list: old entries become ".", no discontinuity, and ENDLIST is
// written. The fixtures are a real 5.1.9 two-run session (15 + 15 segments).
func TestRepairCaptionPlaylistFFmpeg51Append(t *testing.T) {
	vtt, err := os.ReadFile("testdata/ffmpeg51-append-v720_vtt.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	video, err := os.ReadFile("testdata/ffmpeg51-append-v720.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	got := string(repairCaptionPlaylist(vtt, video, "v720"))
	if strings.Contains(got, "#EXT-X-ENDLIST") {
		t.Fatalf("ENDLIST must be dropped (captions would stop):\n%s", got)
	}
	var uris []string
	discBefore := map[string]bool{}
	pendingDisc := false
	for _, line := range strings.Split(got, "\n") {
		switch {
		case line == "#EXT-X-DISCONTINUITY":
			pendingDisc = true
		case line != "" && !strings.HasPrefix(line, "#"):
			uris = append(uris, line)
			discBefore[line] = pendingDisc
			pendingDisc = false
		}
	}
	if len(uris) != 30 || uris[0] != "v7200.vtt" || uris[14] != "v72014.vtt" || uris[15] != "v72015.vtt" || uris[29] != "v72029.vtt" {
		t.Fatalf("entries not rebuilt by sequence: %v", uris)
	}
	if !discBefore["v72015.vtt"] {
		t.Fatalf("missing discontinuity at the restart (video has one before segment 15):\n%s", got)
	}
	if discBefore["v72016.vtt"] {
		t.Fatal("spurious discontinuity")
	}
}

func TestRepairCaptionPlaylistLeavesHealthyPlaylistAlone(t *testing.T) {
	vtt := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:7\n#EXTINF:4.0,\nv7207.vtt\n#EXTINF:4.0,\nv7208.vtt\n"
	video := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:7\n#EXTINF:4.0,\nv720_00007.ts\n#EXTINF:4.0,\nv720_00008.ts\n"
	if got := string(repairCaptionPlaylist([]byte(vtt), []byte(video), "v720")); got != vtt {
		t.Fatalf("healthy playlist changed:\n%s", got)
	}
}

