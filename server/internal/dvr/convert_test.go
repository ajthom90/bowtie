package dvr

import (
	"strings"
	"testing"
	"time"
)

func TestPlaylistDuration(t *testing.T) {
	pl := "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.006000,\nv720_00000.ts\n#EXTINF:5.5,\nv720_00001.ts\n#EXT-X-ENDLIST\n"
	if d := playlistDuration([]byte(pl)); d != 11506*time.Millisecond {
		t.Fatalf("duration %v", d)
	}
}

func TestVODMasterIsRelative(t *testing.T) {
	m := vodMaster()
	if !strings.Contains(m, "\nv720.m3u8\n") || !strings.Contains(m, `URI="aac0.m3u8"`) {
		t.Fatalf("master:\n%s", m)
	}
}
