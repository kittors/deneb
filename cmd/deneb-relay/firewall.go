package main

import (
	"fmt"
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
	// open lets the ports in; undo takes that back.
	open, undo [][]string
	// keep says how to make the rules outlive a reboot, when they do not
	// on their own; "" when they do.
	keep string
}

// nftDeleteMarked is undo's command for nftables rules: delete the rules
// of the chain that carry install's comment (their handles are not known
// before they exist).
const nftDeleteMarked = "nft-delete-marked"

// detectFirewall finds the firewall that keeps the ports out; nil when
// none seems to (or it cannot tell: no root).
func (h *host) detectFirewall(tcp, udp int) *firewall {
	t, u := strconv.Itoa(tcp), strconv.Itoa(udp)
	if h.has("ufw") {
		if out, err := h.run("ufw", "status"); err == nil && strings.Contains(out, "Status: active") {
			return &firewall{kind: "ufw",
				open: [][]string{{"ufw", "allow", t + "/tcp"}, {"ufw", "allow", u + "/udp"}},
				undo: [][]string{{"ufw", "delete", "allow", t + "/tcp"}, {"ufw", "delete", "allow", u + "/udp"}}}
		}
	}
	if h.has("firewall-cmd") {
		if out, err := h.run("firewall-cmd", "--state"); err == nil && strings.TrimSpace(out) == "running" {
			ports := []string{"--add-port=" + t + "/tcp", "--add-port=" + u + "/udp"}
			back := []string{"--remove-port=" + t + "/tcp", "--remove-port=" + u + "/udp"}
			return &firewall{kind: "firewalld",
				open: [][]string{append([]string{"firewall-cmd"}, ports...), append([]string{"firewall-cmd", "--permanent"}, ports...)},
				undo: [][]string{append([]string{"firewall-cmd"}, back...), append([]string{"firewall-cmd", "--permanent"}, back...)}}
		}
	}
	if h.has("nft") {
		if out, err := h.run("nft", "list", "ruleset"); err == nil {
			if family, table, chain, ok := nftDropChain(out); ok {
				rule := func(proto, port string) []string {
					return []string{"nft", "insert", "rule", family, table, chain, proto, "dport", port, "accept", "comment", `"deneb-relay"`}
				}
				return &firewall{kind: "nftables",
					open: [][]string{rule("tcp", t), rule("udp", u)},
					undo: [][]string{{nftDeleteMarked, family, table, chain}},
					keep: "这两条规则重启后会丢：要长期保留，请把它们加进 /etc/nftables.conf"}
			}
		}
	}
	if h.has("iptables") {
		var fw *firewall
		for _, cmd := range []string{"iptables", "ip6tables"} {
			if !h.has(cmd) {
				continue
			}
			if out, err := h.run(cmd, "-S", "INPUT"); err == nil && iptablesDrops(out) {
				if fw == nil {
					fw = &firewall{kind: "iptables"}
				}
				for _, p := range [][2]string{{"tcp", t}, {"udp", u}} {
					spec := []string{"INPUT", "-p", p[0], "--dport", p[1], "-j", "ACCEPT"}
					fw.open = append(fw.open, append([]string{cmd, "-I"}, spec...))
					fw.undo = append(fw.undo, append([]string{cmd, "-D"}, spec...))
				}
			}
		}
		if fw != nil {
			switch {
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
