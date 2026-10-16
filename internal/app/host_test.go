package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Core looks after a linked machine through the agent link: its status, a
// command, and a refusal when the machine's owner turned control off.
func TestCoreLooksAfterALinkedMachine(t *testing.T) {
	core := NewApp()
	addr := startCore(t, core)

	agent := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "box", Version: "test"})
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the agent to connect", func() bool { return core.agents().get("box") != nil })

	h, err := core.MachineStatus("box")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if h.CPUs == 0 || h.OS == "" || !h.Control {
		t.Fatalf("status: %+v", h)
	}

	r, err := core.MachineExec("box", "echo hello; exit 3", 10)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if r.Exit != 3 || strings.TrimSpace(r.Output) != "hello" {
		t.Fatalf("exec: %+v", r)
	}

	if _, err := core.MachineStatus("nobody"); err == nil || !strings.Contains(err.Error(), "box") {
		t.Fatalf("an unknown machine should say which there are: %v", err)
	}

	t.Setenv("SUPERAI_HOST_CONTROL", "off")
	if _, err := core.MachineExec("box", "true", 5); err == nil || !strings.Contains(err.Error(), "SUPERAI_HOST_CONTROL") {
		t.Fatalf("exec with control off: %v", err)
	}
	if _, err := core.MachineService("box", "x", "restart"); err == nil {
		t.Fatal("a service action ran with control off")
	}
	if h, _ := core.MachineStatus("box"); h.Control {
		t.Fatal("status still says the machine takes commands")
	}
}

func TestMachineToolsCheckTheirArguments(t *testing.T) {
	a := NewApp()
	if _, err := a.machineInspect(map[string]any{"machine": "core", "what": "logs"}); err == nil {
		t.Fatal("logs without a target")
	}
	if _, err := a.machineAct(map[string]any{"machine": "core", "action": "reboot"}); err == nil {
		t.Fatal("an action that is not one")
	}
	if _, err := a.HostService("a b; rm -rf /", "restart"); err == nil {
		t.Fatal("a service name with a shell in it")
	}
	if _, err := a.HostLogs("../etc/passwd", 10); err == nil {
		t.Fatal("a relative path read as a log")
	}
	out, err := a.machineInspect(map[string]any{"machine": "core", "what": "status"})
	if err != nil || out.(HostInfo).Name != "core" {
		t.Fatalf("core's own status: %v %v", out, err)
	}
}

func TestParseDF(t *testing.T) {
	linux := `Filesystem     1024-blocks      Used Available Capacity Mounted on
udev               4000000         0   4000000       0% /dev
/dev/sda1         98000000  70000000  28000000      72% /
/dev/sda15          106832      6186    100646       6% /boot/efi
/dev/loop0           65536     65536         0     100% /snap/core20/1
/dev/sdb1        200000000 100000000 100000000      50% /mnt/data disk
`
	d := parseDF(linux, "linux")
	if len(d) != 2 || d[0].Mount != "/" || d[0].Used != 70000000*1024 || d[1].Mount != "/mnt/data disk" {
		t.Fatalf("linux: %+v", d)
	}
	mac := `Filesystem     1024-blocks      Used Available Capacity  Mounted on
/dev/disk3s1s1   971350180  10500000 500000000     3%    /
/dev/disk3s6     971350180   2097172 500000000     1%    /System/Volumes/VM
/dev/disk3s5     971350180 450000000 500000000    48%    /System/Volumes/Data
map auto_home            0         0         0   100%    /System/Volumes/Data/home
`
	d = parseDF(mac, "darwin")
	if len(d) != 2 || d[1].Mount != "/System/Volumes/Data" {
		t.Fatalf("mac: %+v", d)
	}
}

func TestParseServices(t *testing.T) {
	sys := `cron.service loaded active running Regular background program processing daemon
k3s.service loaded failed failed Lightweight Kubernetes
apt-daily.service loaded inactive dead Daily apt download activities
ghost.service not-found inactive dead ghost.service
`
	s := parseSystemctl(sys)
	if len(s) != 2 || s[0].Name != "k3s" || s[0].State != "failed" || s[1].State != "running" || s[1].About == "" {
		t.Fatalf("systemctl: %+v", s)
	}
	ld := `PID	Status	Label
-	0	com.apple.Finder
1149	0	com.anywhere.anywhered
-	78	cn.superleo.broken
-	0	cn.superleo.idle
`
	s = parseLaunchctl(ld)
	if len(s) != 3 || s[0].Name != "cn.superleo.broken" || s[0].State != "failed" || s[1].PID != 1149 || s[2].State != "stopped" {
		t.Fatalf("launchctl: %+v", s)
	}
}

func TestTailFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	b.WriteString("the end\n")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tailFile(p, 3)
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(out, "\n")
	if len(ls) != 3 || ls[2] != "the end" {
		t.Fatalf("tail: %q", out)
	}
}
