package speech

import (
	"bytes"
	"testing"

	"github.com/israel-duff/taskiem/engine/voice"
)

func TestOpusChannelsAndInline(t *testing.T) {
	mono := voice.TestOgg(2, 8)
	if n := opusChannels(mono); n != 1 {
		t.Fatalf("mono read as %d channels", n)
	}
	stereo := bytes.Clone(mono)
	i := bytes.Index(stereo, []byte("OpusHead"))
	stereo[i+9] = 2
	if n := opusChannels(stereo); n != 2 {
		t.Fatalf("stereo read as %d channels", n)
	}
	if opusChannels([]byte("ID3 no ogg")) != 0 {
		t.Fatal("no header")
	}
	if !(Spoken{Data: mono, Type: "audio/ogg"}).PlaysInline() || (Spoken{Data: make([]byte, InlineBytes+1), Type: "audio/ogg"}).PlaysInline() || (Spoken{Data: mono, Type: "audio/mpeg"}).PlaysInline() {
		t.Fatal("inline")
	}
}
