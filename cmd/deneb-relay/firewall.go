package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The system's firewall (design/plan/m7/relay-install.md §5.2): install
// finds which one guards the machine, asks, and lets the relay's two
// ports in; without a yes it prints the commands instead. It remembers
// how to take them back, for uninstall. A cloud's security group is
// outside the machine: the probe from outside finds that one.

// firewall is the system's firewall, as install found it.
type firewall struct {
	kind string // "ufw", "firewalld", "nftables", "iptables"
	// open lets in the ports it keeps out; undo takes that back. opening
	// names those ports ("UDP 47478"), had the ones it let in already: a
	// rule the person made is not install's to open or take back.
	open, undo   [][]string
	opening, had []string
	// keep says how to make the rules outlive a reboot, when they do not
	// on their own; "" when they do.
	keep string
}

// fwPort is one of the relay's ports, as the firewalls say it.
type fwPort struct{ proto, port, words string }

// nftDeleteMarked is undo's command for nftables rules: delete the rules
// of the chain that carry install's comment (their handles are not known
// before they exist).
const nftDeleteMarked = "nft-delete-marked"

// detectFirewall finds the firewall that keeps the ports out; nil when
// none seems to (or it cannot tell: no root).
func (h *host) detectFirewall(tcp, udp int) *firewall {
	t, u := strconv.Itoa(tcp), strconv.Itoa(udp)
	ports := []fwPort{{"tcp", t, "TCP " + t}, {"udp", u, "UDP " + u}}
	if h.has("ufw") {
		if out, err := h.run("ufw", "status"); err == nil && strings.Contains(out, "Status: active") {
			added, _ := h.run("ufw", "show", "added")
			fw := &firewall{kind: "ufw"}
			for _, p := range ports {
				if ufwAllows(added, p) {
					fw.had = append(fw.had, p.words)
					continue
				}
				fw.open = append(fw.open, []string{"ufw", "allow", p.port + "/" + p.proto})
				fw.undo = append(fw.undo, []string{"ufw", "delete", "allow", p.port + "/" + p.proto})
				fw.opening = append(fw.opening, p.words)
			}
			return fw
		}
	}
	if h.has("firewall-cmd") {
		if out, err := h.run("firewall-cmd", "--state"); err == nil && strings.TrimSpace(out) == "running" {
			fw := &firewall{kind: "firewalld"}
			var add, back []string
			for _, p := range ports {
				if out, err := h.run("firewall-cmd", "--query-port="+p.port+"/"+p.proto); err == nil && strings.TrimSpace(out) == "yes" {
					fw.had = append(fw.had, p.words)
					continue
				}
				add = append(add, "--add-port="+p.port+"/"+p.proto)
				back = append(back, "--remove-port="+p.port+"/"+p.proto)
				fw.opening = append(fw.opening, p.words)
			}
			if len(add) > 0 {
				fw.open = [][]string{append([]string{"firewall-cmd"}, add...), append([]string{"firewall-cmd", "--permanent"}, add...)}
				fw.undo = [][]string{append([]string{"firewall-cmd"}, back...), append([]string{"firewall-cmd", "--permanent"}, back...)}
			}
			return fw
		}
	}
	if h.has("nft") {
		if out, err := h.run("nft", "list", "ruleset"); err == nil {
			if family, table, chain, ok := nftDropChain(out); ok {
				fw := &firewall{kind: "nftables"}
				for _, p := range ports {
					if nftAllows(out, family, table, chain, p) {
						fw.had = append(fw.had, p.words)
						continue
					}
					fw.open = append(fw.open, []string{"nft", "insert", "rule", family, table, chain, p.proto, "dport", p.port, "accept", "comment", `"deneb-relay"`})
					fw.opening = append(fw.opening, p.words)
				}
				if len(fw.open) > 0 {
					fw.undo = [][]string{{nftDeleteMarked, family, table, chain}}
					fw.keep = "这些规则重启后会丢：要长期保留，请把它们加进 /etc/nftables.conf"
				}
				return fw
			}
		}
	}
	if h.has("iptables") {
		var fw *firewall
		missing := map[string]bool{} // by words: kept out by one of the two
		for _, cmd := range []string{"iptables", "ip6tables"} {
			if !h.has(cmd) {
				continue
			}
			if out, err := h.run(cmd, "-S", "INPUT"); err == nil && iptablesDrops(out) {
				if fw == nil {
					fw = &firewall{kind: "iptables"}
				}
				for _, p := range ports {
					if iptablesAllows(out, p) {
						continue
					}
					spec := []string{"INPUT", "-p", p.proto, "--dport", p.port, "-j", "ACCEPT"}
					fw.open = append(fw.open, append([]string{cmd, "-I"}, spec...))
					fw.undo = append(fw.undo, append([]string{cmd, "-D"}, spec...))
					missing[p.words] = true
				}
			}
		}
		if fw != nil {
			for _, p := range ports {
				if missing[p.words] {
					fw.opening = append(fw.opening, p.words)
				} else {
					fw.had = append(fw.had, p.words)
				}
			}
			switch {
			case len(fw.open) == 0:
			case h.has("netfilter-persistent"):
				fw.open = append(fw.open, []string{"netfilter-persistent", "save"})
				fw.undo = append(fw.undo, []string{"netfilter-persistent", "save"})
			case h.exists("/etc/sysconfig/iptables") && h.has("service"):
				fw.open = append(fw.open, []string{"service", "iptables", "save"})
				fw.undo = append(fw.undo, []string{"service", "iptables", "save"})
			default:
				fw.keep = "这些规则重启后会丢：要长期保留，请用系统保存 iptables 规则的办法（比如装 iptables-persistent）"
			}
			return fw
		}
	}
	return nil
}

// ufwAllows: `ufw show added` has a rule letting the port in from anywhere,
// for its protocol or for both.
func ufwAllows(added string, p fwPort) bool {
	for _, l := range strings.Split(added, "\n") {
		switch strings.TrimSpace(l) {
		case "ufw allow " + p.port + "/" + p.proto, "ufw allow " + p.port,
			"ufw allow in " + p.port + "/" + p.proto, "ufw allow in " + p.port:
			return true
		}
	}
	return false
}

// nftAllows: the chain has a rule, not install's own, accepting the port:
// "tcp dport 47445 accept", or "tcp dport { 22, 47445 } accept".
func nftAllows(ruleset, family, table, chain string, p fwPort) bool {
	var curFamily, curTable, curChain string
	for _, line := range strings.Split(ruleset, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) >= 3 && f[0] == "table":
			curFamily, curTable, curChain = f[1], f[2], ""
		case len(f) >= 2 && f[0] == "chain":
			curChain = f[1]
		case curFamily == family && curTable == table && curChain == chain && !strings.Contains(line, `comment "deneb-relay"`):
			_, rest, ok := strings.Cut(line, p.proto+" dport ")
			if !ok || !strings.Contains(rest, "accept") {
				continue
			}
			for _, w := range strings.FieldsFunc(rest, func(r rune) bool { return r == ' ' || r == ',' || r == '{' || r == '}' }) {
				if w == p.port {
					return true
				}
				if w == "accept" {
					break
				}
			}
		}
	}
	return false
}

// iptablesAllows: `iptables -S INPUT` has a rule accepting the port from
// anywhere: "-A INPUT -p tcp -m tcp --dport 47445 -j ACCEPT", or a
// multiport one with it. One for a single source, or for loopback, is not.
func iptablesAllows(out string, p fwPort) bool {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		arg := func(name string) string {
			for i := 0; i+1 < len(f); i++ {
				if f[i] == name {
					return f[i+1]
				}
			}
			return ""
		}
		if len(f) < 2 || f[0] != "-A" || f[1] != "INPUT" || arg("-j") != "ACCEPT" || arg("-p") != p.proto || arg("-s") != "" || arg("-i") == "lo" {
			continue
		}
		if arg("--dport") == p.port || slices.Contains(strings.Split(arg("--dports"), ","), p.port) {
			return true
		}
	}
	return false
}

// nftDropChain finds, in `nft list ruleset`, the input chain that drops
// what no rule lets in: its family, table and name.
func nftDropChain(ruleset string) (family, table, chain string, ok bool) {
	var curFamily, curTable, curChain string
	for _, line := range strings.Split(ruleset, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) >= 3 && f[0] == "table":
			curFamily, curTable, curChain = f[1], f[2], ""
		case len(f) >= 2 && f[0] == "chain":
			curChain = f[1]
		case strings.Contains(line, "hook input") && (strings.Contains(line, "policy drop") || strings.Contains(line, "policy reject")):
			if curFamily == "inet" || curFamily == "ip" || curFamily == "ip6" {
				return curFamily, curTable, curChain, curChain != ""
			}
		}
	}
	return "", "", "", false
}

// iptablesDrops: `iptables -S INPUT` drops what no rule lets in, by its
// policy or by a last rule that drops everything.
func iptablesDrops(out string) bool {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		return false
	}
	if strings.HasPrefix(lines[0], "-P INPUT DROP") || strings.HasPrefix(lines[0], "-P INPUT REJECT") {
		return true
	}
	last := strings.Fields(lines[len(lines)-1])
	// "-A INPUT -j REJECT --reject-with icmp-host-prohibited" (RHEL's).
	return len(last) >= 4 && last[0] == "-A" && last[2] == "-j" && (last[3] == "REJECT" || last[3] == "DROP")
}

// apply runs a firewall's commands, the undo marker included.
func (h *host) apply(cmds [][]string) error {
	for _, c := range cmds {
		if c[0] == nftDeleteMarked {
			if err := h.nftDeleteMarked(c[1], c[2], c[3]); err != nil {
				return err
			}
			continue
		}
		if out, err := h.run(c[0], c[1:]...); err != nil {
			return fmt.Errorf("%s：%v%s", strings.Join(c, " "), err, outTail(out))
		}
	}
	return nil
}

// nftDeleteMarked deletes the chain's rules that carry install's comment.
func (h *host) nftDeleteMarked(family, table, chain string) error {
	out, err := h.run("nft", "-a", "list", "chain", family, table, chain)
	if err != nil {
		return fmt.Errorf("nft list chain：%v%s", err, outTail(out))
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, `comment "deneb-relay"`) {
			continue
		}
		_, handle, ok := strings.Cut(line, "# handle ")
		if !ok {
			continue
		}
		if out, err := h.run("nft", "delete", "rule", family, table, chain, "handle", strings.TrimSpace(handle)); err != nil {
			return fmt.Errorf("nft delete rule：%v%s", err, outTail(out))
		}
	}
	return nil
}

// commandLines are commands for a person to type, one per line, with
// sudo in front when they are not root.
func commandLines(cmds [][]string, root bool) []string {
	var out []string
	for _, c := range cmds {
		if c[0] == nftDeleteMarked {
			continue
		}
		line := shellJoin(c)
		if !root {
			line = "sudo " + line
		}
		out = append(out, line)
	}
	return out
}
