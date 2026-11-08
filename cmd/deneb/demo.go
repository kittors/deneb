package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

// demoPlaces are where the demo's devices rest: the prototype's places, so
// that the demo is the design's sample as it was drawn. They are kept in
// memory only: the demo starts as drawn every time.
func demoPlaces() *places {
	return &places{at: map[model.LANID]map[string]ui.Point{
		"weekend": {
			"mac": {X: 300, Y: 278}, "mia": {X: 136, Y: 128}, "jdesk": {X: 468, Y: 128},
			"game": {X: 136, Y: 430}, "jnas": {X: 520, Y: 462}, relayKey: {X: 412, Y: 374},
		},
		"work": {
			"mac": {X: 300, Y: 278}, "studio": {X: 136, Y: 128}, "lindb": {X: 468, Y: 128},
			"linci": {X: 136, Y: 430}, relayKey: {X: 412, Y: 374},
		},
	}}
}

// newDemoApp is the view of the demo's world, as the design's sample shows
// it: what deneb --demo, the view's tests and the review frames start from.
func newDemoApp() (*app, *client.Demo) {
	d := client.NewDemo()
	return newApp(d, demoPlaces()), d
}
