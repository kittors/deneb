package main

import (
	"embed"
	"fmt"
	"image/color"
)

//go:generate go run ../../deploy/gen-avatar-presets.go -out avatar-presets
//go:embed avatar-presets/*.png
var avatarPresetFiles embed.FS
var avatarPatterns = []string{"星芒", "环轨", "山峦", "海浪", "新月", "日光", "叶芽", "云朵", "方块", "花瓣", "小猫", "机器人"}
var avatarPalettes = []struct {
	name        string
	top, bottom color.NRGBA
}{
	{"海蓝", color.NRGBA{188, 229, 255, 255}, color.NRGBA{37, 84, 163, 255}},
	{"青碧", color.NRGBA{188, 243, 233, 255}, color.NRGBA{25, 119, 121, 255}},
	{"森林", color.NRGBA{216, 237, 183, 255}, color.NRGBA{63, 118, 89, 255}},
	{"落日", color.NRGBA{255, 224, 185, 255}, color.NRGBA{184, 92, 78, 255}},
	{"玫瑰", color.NRGBA{255, 216, 227, 255}, color.NRGBA{160, 82, 120, 255}},
	{"鸢尾", color.NRGBA{225, 220, 255, 255}, color.NRGBA{106, 98, 163, 255}},
	{"石墨", color.NRGBA{217, 229, 240, 255}, color.NRGBA{70, 92, 119, 255}},
	{"暖沙", color.NRGBA{244, 231, 200, 255}, color.NRGBA{141, 117, 90, 255}},
}

func presetAvatar(pattern, palette int) []byte {
	b, err := avatarPresetFiles.ReadFile(fmt.Sprintf("avatar-presets/%d-%d.png", palette, pattern))
	if err != nil {
		panic(err)
	}
	return b
}
