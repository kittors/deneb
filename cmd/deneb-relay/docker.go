package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Deneb's own relays ran in Docker (deploy/relay/deploy.sh), and so may
// someone's who followed the old command. install finds such a container,
// asks, and takes it over: its data directory (the identity, certificate
// and keys, so devices and the control server see the same relay) and
// its settings; the container is stopped and kept, to go back to
// (design/plan/m7/relay-install.md §5.1).

// dockerRelay is a relay container, as docker inspect describes it.
type dockerRelay struct {
	id, name, image string
	running         bool
	restart         string // its restart policy ("unless-stopped"), put back if install fails
	data            string // the host directory mounted at its data directory
	settings        settings
}

// giveBack undoes takeOverDocker, for an install that failed after it:
// the container runs again as it did.
func (h *host) giveBack(d *dockerRelay) {
	if d.restart != "" {
		h.run("docker", "update", "--restart="+d.restart, d.id)
	}
	if d.running {
		h.run("docker", "start", d.id)
	}
}

// findDockerRelay finds a relay container; nil when there is none (or no
// Docker).
func (h *host) findDockerRelay() (*dockerRelay, error) {
	if !h.has("docker") {
		return nil, nil
	}
	out, err := h.run("docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}")
	if err != nil {
		return nil, nil // no daemon, or not ours to ask
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[1] != "deneb-relay" && !strings.HasPrefix(f[2], "deneb-relay") {
			continue
		}
		js, err := h.run("docker", "inspect", f[0])
		if err != nil {
			return nil, fmt.Errorf("docker inspect %s：%v%s", f[1], err, outTail(js))
		}
		d, err := parseDockerInspect(js)
		if err != nil {
			return nil, err
		}
		d.id, d.name, d.image = f[0], f[1], f[2]
		return d, nil
	}
	return nil, nil
}

// parseDockerInspect reads a relay container's settings from docker
// inspect: the flags it runs with, and where its data directory is.
func parseDockerInspect(js string) (*dockerRelay, error) {
	var in []struct {
		Args   []string
		State  struct{ Running bool }
		Mounts []struct {
			Source, Destination string
		}
		Config     struct{ Env []string }
		HostConfig struct {
			RestartPolicy struct{ Name string }
		}
	}
	if err := json.Unmarshal([]byte(js), &in); err != nil || len(in) == 0 {
		return nil, fmt.Errorf("docker inspect 的结果看不懂：%v", err)
	}
	c := in[0]
	fs := flag.NewFlagSet("docker", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var s settings
	var derp, stun uint
	data := fs.String("data", "/data", "")
	fs.StringVar(&s.Control, "control", "", "")
	fs.StringVar(&s.ControlPin, "control-pin", "", "")
	fs.StringVar(&s.Listen, "listen", ":47445", "")
	fs.StringVar(&s.STUN, "stun", ":47478", "")
	fs.StringVar(&s.HostName, "hostname", "", "")
	fs.StringVar(&s.IPv4, "ipv4", "", "")
	fs.StringVar(&s.IPv6, "ipv6", "", "")
	fs.StringVar(&s.Region, "region", "", "")
	fs.UintVar(&derp, "derp-port", 0, "")
	fs.UintVar(&stun, "stun-port", 0, "")
	fs.String("token", "", "")
	fs.String("join", "", "")
	fs.Duration("sync", 0, "")
	if err := fs.Parse(c.Args); err != nil {
		return nil, fmt.Errorf("容器的参数看不懂：%v", err)
	}
	s.DERPPort, s.STUNPort = uint16(derp), uint16(stun)
	d := &dockerRelay{running: c.State.Running, restart: c.HostConfig.RestartPolicy.Name, settings: s}
	for _, m := range c.Mounts {
		if m.Destination == *data {
			d.data = m.Source
		}
	}
	if d.data == "" {
		return nil, errors.New("容器的数据目录不在宿主机上（没有挂载 " + *data + "）：迁不过来")
	}
	return d, nil
}

// takeOverDocker copies the container's data into data, stops the
// container and keeps it from starting again; it returns the container's
// settings.
func (h *host) takeOverDocker(d *dockerRelay, data string) (*settings, error) {
	src := h.path(d.data)
	ents, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(h.path(data), 0o700); err != nil {
		return nil, err
	}
	// The container first, so that the copy is what it last wrote.
	if out, err := h.run("docker", "update", "--restart=no", d.id); err != nil {
		return nil, fmt.Errorf("docker update：%v%s", err, outTail(out))
	}
	if d.running {
		if out, err := h.run("docker", "stop", "-t", "10", d.id); err != nil {
			return nil, fmt.Errorf("docker stop：%v%s", err, outTail(out))
		}
	}
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		switch e.Name() {
		case lockFile, pidFile, listenFile:
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return nil, err
		}
		if err := writeFile(h.path(data), e.Name(), b); err != nil {
			return nil, err
		}
	}
	s := d.settings
	return &s, nil
}
