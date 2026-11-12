package ui

import (
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestPacketGapDraftAppliesOnlyValidValues(t *testing.T) {
	app := &internal.App{Settings: config.Settings{
		Encoding:           "UTF-8",
		Timestamp:          true,
		PacketGap:          "20",
		DisplayBufferLimit: config.DisplayBufferPresets()[0].Bytes,
	}}
	s := newController(app)
	s.configureLog()

	s.packetGapDraft = "0"
	s.packetGapChanged()
	if s.settings.PacketGap != "20" || s.log.options.gap != 20*time.Millisecond || s.packetGapError.en == "" || app.SettingsDirty {
		t.Fatalf("invalid packet-gap edit changed applied settings: value=%q gap=%s error=%+v dirty=%v", s.settings.PacketGap, s.log.options.gap, s.packetGapError, app.SettingsDirty)
	}

	s.packetGapDraft = "45"
	s.packetGapChanged()
	if s.settings.PacketGap != "45" || s.log.options.gap != 45*time.Millisecond || s.packetGapError != (localizedText{}) || !app.SettingsDirty {
		t.Fatalf("valid packet-gap edit was not applied: value=%q gap=%s error=%+v dirty=%v", s.settings.PacketGap, s.log.options.gap, s.packetGapError, app.SettingsDirty)
	}
}
