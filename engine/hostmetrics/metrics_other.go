//go:build !darwin && !linux && !windows

package hostmetrics

import "github.com/kittors/deneb/proto"

func readHost(m *proto.DeviceMetrics) (counters, bool) {
	m.Errors = []string{"此系统暂不支持主机统计"}
	return counters{}, false
}
