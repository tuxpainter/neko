package legacy

import "testing"

func TestParseMediaMode(t *testing.T) {
	tests := []struct {
		value string
		want  mediaMode
	}{
		{value: "webcodecs-ws", want: mediaModeWebCodecsWS},
		{value: "webcodecs-wt", want: mediaModeWebCodecsWT},
		{value: "webcodecs", want: mediaModeWebRTC},
		{value: "webrtc", want: mediaModeWebRTC},
		{value: "", want: mediaModeWebRTC},
		{value: "unknown", want: mediaModeWebRTC},
	}

	for _, test := range tests {
		if got := parseMediaMode(test.value); got != test.want {
			t.Errorf("parseMediaMode(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}
