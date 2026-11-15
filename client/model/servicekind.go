package model

import (
	"strconv"
	"strings"
)

// ServiceKindFor is the kind a service set up by hand is taken to be, by
// its port: what people usually run there, "other" when nothing usual is
// (DESIGN §6.15, design/plan/m7/mydevices.md §6.1). Only a name and a port
// are asked for; the kind gives the icon and the name left empty.
func ServiceKindFor(port uint16) string {
	switch port {
	case 3389, 5900:
		return "screen"
	case 445, 548, 139:
		return "files"
	case 22:
		return "shell"
	case 80, 443, 3000, 5000, 8000, 8080, 8443, 8888:
		return "web"
	case 25565, 27015, 7777, 19132:
		return "game"
	}
	return "other"
}

// KindName is what people call a service of kind when it has no name of
// its own.
func KindName(kind string) string {
	switch kind {
	case "screen":
		return "远程桌面"
	case "files":
		return "共享文件夹"
	case "web":
		return "网站"
	case "shell":
		return "命令行"
	case "game":
		return "游戏"
	}
	return "服务"
}

// HandService is a service set up by hand: name (its kind's when empty) on
// port, TCP and UDP both, as the user decided (a game needs UDP, and a
// port that only half opens fails without a word).
func HandService(name string, port uint16) Service {
	kind := ServiceKindFor(port)
	if name = strings.TrimSpace(name); name == "" {
		name = KindName(kind)
	}
	p := strconv.Itoa(int(port))
	return Service{Key: "tcp/" + p + ",udp/" + p, Kind: kind, Name: name, Proto: "tcp", Port: port, Ports: p}
}

// PortTaken is the service of d's catalog that already uses port, if any:
// two services on a device never share a port.
func PortTaken(d *Device, port uint16) *Service {
	p := strconv.Itoa(int(port))
	for i, s := range d.Catalog {
		for _, part := range strings.Split(s.Key, ",") {
			_, num, ok := strings.Cut(part, "/")
			if !ok {
				continue
			}
			first, last, isRange := strings.Cut(num, "-")
			if !isRange {
				last = first
			}
			lo, err1 := strconv.Atoi(first)
			hi, err2 := strconv.Atoi(last)
			if err1 == nil && err2 == nil && int(port) >= lo && int(port) <= hi {
				return &d.Catalog[i]
			}
			if num == p {
				return &d.Catalog[i]
			}
		}
	}
	return nil
}
